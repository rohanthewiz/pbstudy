// Package store owns database lifecycle: opening the two bytdb files,
// bootstrapping their schemas, and taking snapshot backups.
//
// # Why two databases
//
//	bible.bytdb  — ~31k rows per translation of public-domain scripture.
//	               Bulky, wholly rebuildable from getbible.net, and therefore
//	               never synced and never backed up.
//	study.bytdb  — notes, tags, cross-references, sermons. Small, irreplaceable.
//	               This is what gets exported to JSON and snapshotted.
//
// Splitting them means a backup is small enough to take often, and a corrupt
// or stale scripture cache can be deleted without a second thought.
package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"time"

	"github.com/rohanthewiz/bytdb/stdlib"
	"github.com/rohanthewiz/serr"

	"pbstudy/cfg"
)

// Store holds the open database handles for the process lifetime.
//
// bytdb takes the database file for itself — one path is one engine per
// process — so these handles are opened once at startup and shared. *sql.DB
// is safe for concurrent use; each pooled connection gets its own bytdb
// session.
type Store struct {
	// Bible is the scripture cache (read-heavy; written only by `download`).
	Bible *sql.DB

	// Study is the notes/tags/xrefs/sermons database.
	Study *sql.DB

	// lock keeps a second pbstudy process out of this data directory.
	lock *dirLock

	cfg cfg.Config
}

// Open opens both databases and brings their schemas up to date.
//
// The bible DB is opened with sync=never: every row in it can be re-fetched
// with `pbstudy download`, so trading crash durability for a materially
// faster 31k-row bulk import is the right side of that bargain. The study DB
// keeps bytdb's default fsync behaviour — that data has no upstream.
func Open(conf cfg.Config) (*Store, error) {
	st := &Store{cfg: conf}

	// Take the data-directory lock before touching either database. bytdb
	// does no locking of its own, so this is the only thing standing between
	// a stray second process and interleaved appends into study.bytdb.
	lock, err := acquireDirLock(conf.DataDir)
	if err != nil {
		return nil, err
	}
	st.lock = lock

	bibleDB, err := sql.Open("bytdb", conf.BibleDBPath()+"?sync=never")
	if err != nil {
		_ = st.Close()
		return nil, serr.Wrap(err, "cannot open bible db", "path", conf.BibleDBPath())
	}
	st.Bible = bibleDB

	studyDB, err := sql.Open("bytdb", conf.StudyDBPath())
	if err != nil {
		_ = st.Close()
		return nil, serr.Wrap(err, "cannot open study db", "path", conf.StudyDBPath())
	}
	st.Study = studyDB

	if err := bootstrapBible(bibleDB); err != nil {
		_ = st.Close()
		return nil, serr.Wrap(err, "cannot bootstrap bible schema")
	}
	if err := bootstrapStudy(studyDB); err != nil {
		_ = st.Close()
		return nil, serr.Wrap(err, "cannot bootstrap study schema")
	}

	return st, nil
}

// Close releases both database handles and the data-directory lock.
//
// Every step is attempted even if an earlier one fails — the first error is
// reported, but a failed database close must not leave the lock held for the
// life of the process.
func (s *Store) Close() error {
	var firstErr error
	fail := func(err error, what string) {
		if err != nil && firstErr == nil {
			firstErr = serr.Wrap(err, what)
		}
	}

	if s.Bible != nil {
		fail(s.Bible.Close(), "closing bible db")
		s.Bible = nil
	}
	if s.Study != nil {
		fail(s.Study.Close(), "closing study db")
		s.Study = nil
	}
	if s.lock != nil {
		fail(s.lock.release(), "releasing data directory lock")
		s.lock = nil
	}
	return firstErr
}

// BackupStudy writes a consistent snapshot of the study database to destDir,
// named study-<RFC3339-ish timestamp>.bytdb, and returns the path written.
//
// This is the ONLY safe way to get a copy of a live bytdb file. bytdb does no
// file locking, so a sync daemon (or a plain `cp`) that reads the live file
// mid-append captures a torn tail. Engine.Backup takes an internal snapshot
// and writes a self-consistent file, which is safe to hand to iCloud.
func (s *Store) BackupStudy(destDir string) (string, error) {
	if destDir == "" {
		return "", serr.New("no backup destination configured")
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", serr.Wrap(err, "cannot create backup dir", "dir", destDir)
	}

	// Reach past database/sql to the native engine — Backup() has no
	// SQL-level equivalent.
	eng, err := stdlib.Engine(context.Background(), s.Study)
	if err != nil {
		return "", serr.Wrap(err, "cannot reach bytdb engine for backup")
	}

	// Colons are legal on the filesystems we care about but make paths
	// awkward to type and illegal on FAT/exFAT sync volumes.
	stamp := time.Now().UTC().Format("20060102-150405")
	dest := filepath.Join(destDir, "study-"+stamp+".bytdb")

	if err := eng.Backup(dest); err != nil {
		return "", serr.Wrap(err, "backup failed", "dest", dest)
	}
	return dest, nil
}
