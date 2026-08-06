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
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

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

Environment:
  PBSTUDY_DATA_DIR    where the databases live      (default ~/.pbstudy)
  PBSTUDY_SYNC_DIR    sync/backup directory         (default: sync disabled)
  PBSTUDY_PORT        HTTP listen port              (default 8000)
  ANTHROPIC_API_KEY   enables AI sermon drafting    (optional)

The sync directory must not contain, or live inside, the data directory:
the live databases are unlocked and append-only, so a sync daemon that
copies one mid-write captures a torn file.
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
		} else if rep.Imported() > 0 || rep.Problems() > 0 {
			fmt.Printf("Sync: %s\n", rep.Headline())
		}

		// rweb's Run() does not return on a signal, so the export that must
		// happen before exit is hung off the signal itself.
		installShutdownFlush(sync, st)
	}

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
func installShutdownFlush(sync *syncer.Syncer, st *store.Store) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		fmt.Printf("\nStopping (%s); writing pending changes to the sync directory ...\n", sig)

		if rep, err := sync.Flush(); err != nil {
			logger.LogErr(err, "final sync export failed")
		} else if rep.Exported() > 0 {
			fmt.Printf("Sync: %d file(s) written\n", rep.Exported())
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
