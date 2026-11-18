// Command pbstudy is a personal Bible study application: a local web app for
// tracking notes, correlating scriptures, studying topics, and assembling
// sermons from the result.
//
// Everything runs on the machine it is launched from. Scripture is cached
// locally so study works offline; only the optional AI drafting feature and
// the Blue Letter Bible links reach the network.
//
// Usage:
//
//	pbstudy [serve]                    start the web app (default)
//	pbstudy download <kjv|web|asv|all> fetch scripture into the local cache
//	pbstudy backup [dir]               write a snapshot of the study database
//	pbstudy sync                       reconcile with the sync directory
//	pbstudy compact [days]             remove tombstones the sync no longer needs
package main

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"

	// Registers the "bytdb" database/sql driver. Imported for the side
	// effect only; store/ opens databases through database/sql.
	_ "github.com/rohanthewiz/bytdb/stdlib"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/cfg"
	"github.com/rohanthewiz/pbstudy/store"
	"github.com/rohanthewiz/pbstudy/syncer"
	"github.com/rohanthewiz/pbstudy/web"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		logger.LogErr(err, "pbstudy failed")
		os.Exit(1)
	}
}

// run dispatches the subcommand. Split out from main so every exit path funnels
// through one error report.
func run(args []string) error {
	command := "serve"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}

	// -h/--help before config loading: asking for help should never fail
	// because a data directory could not be created.
	switch command {
	case "-h", "--help", "help":
		usage()
		return nil
	}

	conf, err := cfg.Load()
	if err != nil {
		return err
	}

	st, err := store.Open(conf)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.LogErr(err, "error closing databases")
		}
	}()

	switch command {
	case "serve":
		return cmdServe(conf, st)
	case "download":
		return cmdDownload(st, args)
	case "backup":
		return cmdBackup(conf, st, args)
	case "sync":
		return cmdSync(conf, st)
	case "compact":
		return cmdCompact(conf, st, args)
	default:
		usage()
		return serr.New("unknown command", "command", command)
	}
}

func usage() {
	fmt.Print(`pbstudy — personal Bible study

Usage:
  pbstudy [serve]                      start the web app (default)
  pbstudy download <kjv|web|asv|all>   fetch scripture into the local cache
  pbstudy backup [dir]                 snapshot the study database
  pbstudy sync                         reconcile with the sync directory
  pbstudy compact [days] [--dry-run]   remove tombstones older than <days>
                                       (default 90) from the database and the
                                       sync directory, then shrink the
                                       database file

Environment:
  PBSTUDY_DATA_DIR    where the databases live      (default ~/.pbstudy)
  PBSTUDY_SYNC_DIR    sync/backup directory         (default: sync disabled)
  PBSTUDY_PORT        HTTP listen port              (default 8000)
  ANTHROPIC_API_KEY   enables AI sermon drafting    (optional)

The sync directory must not contain, or live inside, the data directory:
the live databases are written in place, and a sync daemon ignores their
locks, so one that copies a database mid-write captures a torn file.
`)
}

// cmdServe starts the web app.
func cmdServe(conf cfg.Config, st *store.Store) error {
	// Seed the canon table so anything reading the database directly sees a
	// populated books table even before scripture is downloaded.
	if err := bible.SeedBooks(st.Bible); err != nil {
		return err
	}

	sync, err := openSyncer(conf, st)
	if err != nil {
		return err
	}
	if sync != nil {
		// Import before the first page is served: whatever the sync daemon
		// delivered while this app was closed should already be here when the
		// user opens the notes list, not one manual button-press later.
		rep, err := sync.ImportAll()
		if err != nil {
			// A sync directory that cannot be read is not a reason to refuse to
			// open the user's own database. Say so and carry on local-only.
			logger.LogErr(err, "startup sync import failed", "dir", sync.Dir())
		} else {
			if rep.Imported() > 0 || rep.Problems() > 0 {
				fmt.Printf("Sync: %s\n", rep.Headline())
				// The headline only counts problems. Name them here too: a
				// file the import refused (a conflict copy, a record from a
				// newer version) is something to act on, and until now the
				// only way to learn which file was to press Sync now on the
				// settings page.
				for _, e := range rep.Entities {
					for _, p := range e.Problems {
						fmt.Printf("  ! %s: %s\n", e.Label(), p)
					}
				}
			}

			// Catch-up export. The automatic export is debounced, and the
			// shutdown flush only runs when the process is asked to stop, so a
			// crash, a power cut or a kill -9 inside that window leaves the
			// last edit in the database with no file for it:
			//
			//	edit saved ──(debounce, 2s)──▶ export        normal
			//	edit saved ──✗ process dies                  row newer than its file
			//	next start: import only                      ...and it stays that way
			//	                                             until some other edit
			//
			// Scheduling one export here closes that gap on the next start
			// instead of on the next edit. It goes through the same debounced
			// path as an edit (Touch), so it runs after the import above has
			// settled, off the startup path, and it writes only rows that are
			// newer than their files; on a clean previous exit that is none.
			// Skipped when the import failed: a folder that cannot be read is
			// not one to start writing into.
			sync.Touch()
		}
	}

	// rweb's Run() does not return on a signal, so the work that must happen
	// before exit (a final sync export, closing the databases) is hung off the
	// signal itself. Installed with or without a syncer: without one, SIGTERM
	// would otherwise kill the process before run()'s deferred st.Close().
	installShutdownFlush(sync, st)

	srv, err := web.New(conf, st, sync)
	if err != nil {
		return err
	}

	fmt.Printf("pbstudy serving on http://localhost:%d  (data: %s)\n",
		conf.Port, conf.DataDir)
	return srv.Run()
}

// openSyncer builds the syncer, or returns nil when no sync directory is
// configured. Sync is optional everywhere: a nil syncer is a working app.
func openSyncer(conf cfg.Config, st *store.Store) (*syncer.Syncer, error) {
	if !conf.SyncEnabled() {
		return nil, nil
	}
	sync, err := syncer.New(st.Study, conf.SyncDir)
	if err != nil {
		return nil, err
	}
	return sync, nil
}

// installShutdownFlush exports pending changes when the process is asked to
// stop.
//
// Automatic export is debounced by a couple of seconds (syncer.DebounceDelay),
// which means a note saved immediately before Ctrl-C has not been written out
// yet. Without this, the last edit of every session would sit in the database
// until the next run — precisely the edit most likely to be wanted on the other
// machine.
//
// The handler does the closing itself: os.Exit runs no deferred functions, and
// the alternative (asking rweb to stop) does not exist in its API.
//
// sync may be nil (no sync directory configured). The handler is still worth
// installing then, for the close alone. bytdb's default durability means an
// unclosed database loses nothing, and the kernel drops the flocks when the
// process dies, so this is tidiness rather than a fix for data loss: the
// databases are closed through their own Close, the same path every other
// command takes, instead of being abandoned mid-log by a signal.
func installShutdownFlush(sync *syncer.Syncer, st *store.Store) {
	sigs := make(chan os.Signal, 1)
	// SIGHUP is what a terminal sends when its window is closed, which is how
	// a server started by hand in a terminal usually ends. Left unhandled it
	// kills the process before the flush below, exactly like the SIGTERM case
	// this function was written for.
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		sig := <-sigs
		if sync != nil {
			fmt.Printf("\nStopping (%s); writing pending changes to the sync directory ...\n", sig)

			if rep, err := sync.Flush(); err != nil {
				logger.LogErr(err, "final sync export failed")
			} else if rep.Exported() > 0 {
				fmt.Printf("Sync: %d file(s) written\n", rep.Exported())
			}
		} else {
			fmt.Printf("\nStopping (%s) ...\n", sig)
		}

		if err := st.Close(); err != nil {
			logger.LogErr(err, "error closing databases")
		}
		os.Exit(0)
	}()
}

// cmdSync reconciles the sync directory from the command line.
//
// The same engine the settings page drives, reachable without starting the
// server — which is what makes it scriptable (a cron job, a git pre-commit
// hook on the sync repo). It cannot run while `serve` holds the data directory
// lock, and that is the correct answer: one writer at a time.
func cmdSync(conf cfg.Config, st *store.Store) error {
	if !conf.SyncEnabled() {
		return serr.New("sync is not configured", "hint", "set "+cfg.EnvSyncDir)
	}

	sync, err := syncer.New(st.Study, conf.SyncDir)
	if err != nil {
		return err
	}

	rep, err := sync.Run()
	if err != nil {
		return err
	}

	fmt.Printf("Sync with %s\n  %s\n", rep.Dir, rep.Headline())
	for _, e := range rep.Entities {
		fmt.Printf("  %-18s %s\n", e.Label(), e.Summary())
		for _, n := range e.Notes {
			fmt.Printf("      - %s\n", n)
		}
		for _, p := range e.Problems {
			fmt.Printf("      ! %s\n", p)
		}
	}
	return nil
}

// cmdCompact removes tombstones that have outlived their purpose.
//
// # Why this is a command and not a button
//
// Every other maintenance action in this app is reversible or additive. This one
// throws away the last evidence that something was deleted, on a schedule only
// the user knows is safe — how long a second machine has been away is not a
// thing this process can see. A command has to be typed, takes the window as an
// argument, and offers --dry-run; a button on the settings page would be one
// click between a user and an irreversible pass with a number they never chose.
//
// # Why it requires a sync directory
//
// A DELETE does not shrink bytdb's file: it appends to the log, and the space
// comes back only when bytdb rewrites the log — on its own once the log has
// grown enough, or on demand through VACUUM (bytdb v0.19+), which this command
// does not run. Compacting a database with no sync folder would therefore
// spend a destructive pass to save next to nothing. The sync folder is where
// the saving is: one JSON file per dead row, kept forever, in a directory a
// daemon re-uploads. Somebody wanting the database itself smaller wants
// `pbstudy backup`, which writes a fresh file.
func cmdCompact(conf cfg.Config, st *store.Store, args []string) error {
	if !conf.SyncEnabled() {
		return serr.New("compact needs a sync directory",
			"hint", "set "+cfg.EnvSyncDir+"; without one there are no tombstone "+
				"files to remove, and deleting rows does not shrink the database file")
	}

	retention := syncer.DefaultRetention
	dryRun := false

	// Hand-parsed rather than through flag: this binary has no flag set, and
	// two forms is not enough to justify starting one.
	for _, arg := range args {
		switch arg {
		case "-n", "--dry-run":
			dryRun = true
		default:
			days, err := strconv.Atoi(arg)
			if err != nil || days < 0 {
				return serr.New("compact takes a whole number of days",
					"got", arg, "usage", "pbstudy compact [days] [--dry-run]")
			}
			retention = time.Duration(days) * 24 * time.Hour
		}
	}

	sync, err := syncer.New(st.Study, conf.SyncDir)
	if err != nil {
		return err
	}

	rep, err := sync.Compact(retention, dryRun)
	if err != nil {
		return err
	}

	fmt.Printf("Compact %s\n", rep.Dir)
	fmt.Printf("  tombstones deleted before %s are eligible\n",
		rep.Cutoff.Format("2006-01-02 15:04 MST"))
	if rep.Reconcile.Imported() > 0 || rep.Reconcile.Problems() > 0 {
		// The import ran whether or not it found anything; only say so when it
		// did, since a silent "0 changes in" is noise on every run.
		fmt.Printf("  reconciled first: %s\n", rep.Reconcile.Headline())
		for _, e := range rep.Reconcile.Entities {
			for _, p := range e.Problems {
				fmt.Printf("      ! %s\n", p)
			}
		}
	}
	fmt.Printf("  %s\n", rep.Headline())

	for _, e := range rep.Entities {
		fmt.Printf("  %-18s %s\n", e.Label(), e.Summary())
		for _, p := range e.Problems {
			fmt.Printf("      ! %s\n", p)
		}
	}

	if dryRun {
		fmt.Println("\nThis was a dry run; nothing was removed.")
		return nil
	}

	// Removing rows appends their deletes to bytdb's log, so without this the
	// purge would leave study.bytdb larger than before it ran. Done on every
	// real pass, not only when rows went: a pass that removed nothing still
	// reclaims whatever ordinary edits have appended since the last
	// compaction, and on a database this size it costs well under a second.
	// The dry run skips it because it promises to change nothing.
	//
	// A failure here is reported as the command's error even though the purge
	// above succeeded: the rows are gone either way, and a non-zero exit is
	// the honest answer to "did compact do everything it says it does".
	before, after, err := st.CompactStudy()
	if err != nil {
		return err
	}
	fmt.Printf("  %-18s %d → %d bytes\n", "Study database", before, after)
	return nil
}

// cmdDownload fetches one or all translations into the scripture cache.
func cmdDownload(st *store.Store, args []string) error {
	if len(args) == 0 {
		return serr.New("download needs a translation",
			"usage", "pbstudy download <kjv|web|asv|all>")
	}

	targets := args[:1]
	if args[0] == "all" {
		targets = nil
		for _, t := range bible.Known {
			targets = append(targets, t.Abbrev)
		}
	}

	for _, abbrev := range targets {
		fmt.Printf("Downloading %s from getbible.net ...\n", abbrev)

		// Progress on one rewritten line: 66 books would otherwise scroll
		// the terminal for no benefit.
		total, err := bible.Download(st.Bible, abbrev,
			func(bookNum int, bookName string, verses int) {
				fmt.Printf("\r  [%2d/66] %-24s %5d verses", bookNum, bookName, verses)
			})
		fmt.Println()
		if err != nil {
			return err
		}

		fmt.Printf("  %s: %d verses cached\n", abbrev, total)

		// Cross-check the compiled chapter counts against what arrived. A
		// mismatch means either our canon table has a typo or the download
		// was truncated — worth reporting, not worth failing over, since
		// the text that did arrive is still usable.
		if problems := bible.VerifyChapterCounts(st.Bible, abbrev); len(problems) > 0 {
			fmt.Printf("  ! %d chapter-count mismatch(es):\n", len(problems))
			for _, p := range problems {
				fmt.Printf("      %s\n", p)
			}
		}
	}

	return nil
}

// cmdBackup writes a snapshot of the study database.
//
// The destination defaults to <SyncDir>/backups, which is the whole point:
// the live .bytdb must never be handed to a sync daemon, but a snapshot is
// safe to place in a synced directory.
func cmdBackup(conf cfg.Config, st *store.Store, args []string) error {
	dest := ""
	switch {
	case len(args) > 0:
		dest = args[0]
	case conf.SyncEnabled():
		dest = conf.SyncDir + "/backups"
	default:
		return serr.New("no backup destination",
			"hint", "pass a directory, or set "+cfg.EnvSyncDir)
	}

	path, err := st.BackupStudy(dest)
	if err != nil {
		return err
	}

	fmt.Printf("Study database snapshot written to %s\n", path)
	return nil
}
