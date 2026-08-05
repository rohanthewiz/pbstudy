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
package main

import (
	"fmt"
	"os"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"

	// Registers the "bytdb" database/sql driver. Imported for the side
	// effect only; store/ opens databases through database/sql.
	_ "github.com/rohanthewiz/bytdb/stdlib"

	"pbstudy/bible"
	"pbstudy/cfg"
	"pbstudy/store"
	"pbstudy/web"
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

Environment:
  PBSTUDY_DATA_DIR    where the databases live      (default ~/.pbstudy)
  PBSTUDY_SYNC_DIR    sync/backup directory         (default: sync disabled)
  PBSTUDY_PORT        HTTP listen port              (default 8000)
  ANTHROPIC_API_KEY   enables AI sermon drafting    (optional)
`)
}

// cmdServe starts the web app.
func cmdServe(conf cfg.Config, st *store.Store) error {
	// Seed the canon table so anything reading the database directly sees a
	// populated books table even before scripture is downloaded.
	if err := bible.SeedBooks(st.Bible); err != nil {
		return err
	}

	srv, err := web.New(conf, st)
	if err != nil {
		return err
	}

	fmt.Printf("pbstudy serving on http://localhost:%d  (data: %s)\n",
		conf.Port, conf.DataDir)
	return srv.Run()
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
