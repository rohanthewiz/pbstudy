// Package syncer keeps a study database and a directory of JSON files in
// agreement, so two machines pointed at the same synced folder converge.
//
// # The shape of the thing
//
//	~/.pbstudy/study.bytdb        the live database — local, locked, never copied
//	        ↓ export      ↑ import
//	<sync>/notes/<uuid>.json      one file per row, tombstones included
//	<sync>/tags/<uuid>.json
//	<sync>/xrefs/<uuid>.json
//	<sync>/sermons/<uuid>.json
//	<sync>/backups/study-<ts>.bytdb   engine snapshots (see store.BackupStudy)
//
// A file-sync daemon (iCloud, Syncthing, git) is what moves those files between
// machines. This package never talks to another machine and has no idea one
// exists; it only ever reconciles a directory with a database.
//
// # Why files rather than the database itself
//
// bytdb does no file locking and its file is append-only, so a daemon that
// copies study.bytdb mid-append captures a torn tail. One small JSON file per
// row is the opposite: a daemon can copy each one at any moment, the worst case
// is a file that arrives a moment late, and a conflict is resolved by comparing
// two timestamps instead of two binary logs. cfg.checkDirsDisjoint is what
// keeps the live file out of that directory in the first place.
//
// # Merge rule
//
// Last writer wins on updated_at, per row, in both directions:
//
//	file newer than row (or no row)   → import: the file overwrites the row
//	row newer than file (or no file)  → export: the row overwrites the file
//	equal                             → nothing happens
//
// Import runs first and export second, over a freshly re-read database, so a
// row that was just imported exports back only if it actually changed. The
// consequence worth knowing: two machines editing the same note while both
// offline lose one side's edit. That is the accepted trade for a single-user
// tool — and it is why backups exist.
package syncer

import (
	"database/sql"
	"sync"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"

	"github.com/rohanthewiz/pbstudy/study"
)

// DebounceDelay is how long a write is left to settle before an automatic
// export runs.
//
// Editing a note is one save, but adding three outline sections is three POSTs
// in a few seconds. Debouncing turns a burst into one export instead of one per
// click, which matters when every export writes files a sync daemon is watching.
const DebounceDelay = 2 * time.Second

// Syncer reconciles one study database with one sync directory.
//
// It is safe for concurrent use: every reconciliation takes runMu, so a
// debounced auto-export cannot interleave with a manual run from the settings
// page. The lock is held for the whole pass rather than per entity — a pass is
// milliseconds on this data, and a half-applied merge is much harder to reason
// about than a queued one.
type Syncer struct {
	db  *sql.DB
	dir string

	runMu sync.Mutex

	// timerMu guards the debounce timer only, so Touch never waits on a run
	// in progress — it is called from request handlers.
	timerMu sync.Mutex
	timer   *time.Timer
	stopped bool
}

// New creates a syncer over dir, creating the directory tree if needed.
func New(db *sql.DB, dir string) (*Syncer, error) {
	if db == nil {
		return nil, serr.New("syncer needs a study database")
	}
	if dir == "" {
		return nil, serr.New("syncer needs a sync directory")
	}

	s := &Syncer{db: db, dir: dir}
	if err := s.ensureDirs(); err != nil {
		return nil, err
	}
	return s, nil
}

// Dir is the directory being reconciled.
func (s *Syncer) Dir() string { return s.dir }

// Run performs a full two-way reconciliation and returns what it did.
//
// The returned Report is the whole point of the call: a sync that silently
// "worked" tells the user nothing about whether the note they wrote on the
// other machine actually arrived.
func (s *Syncer) Run() (Report, error) {
	return s.run(true, true)
}

// ImportAll applies incoming files without writing any. This is what runs at
// startup: whatever the daemon delivered while the app was closed lands before
// the first page is served, and nothing is written until the user does
// something.
func (s *Syncer) ImportAll() (Report, error) {
	return s.run(true, false)
}

// ExportAll writes local rows out without reading incoming files. This is the
// debounced automatic direction and the shutdown flush — publishing what was
// just written here, without the surprise of a page's worth of remote edits
// arriving in the middle of a session.
func (s *Syncer) ExportAll() (Report, error) {
	return s.run(false, true)
}

// Touch schedules a debounced export. Cheap and non-blocking: it is called from
// request handlers, which must not wait on a sync.
func (s *Syncer) Touch() {
	s.timerMu.Lock()
	defer s.timerMu.Unlock()

	if s.stopped {
		return
	}
	if s.timer != nil {
		// Resetting rather than adding a second timer is what makes a burst of
		// edits collapse into one export.
		s.timer.Reset(DebounceDelay)
		return
	}
	s.timer = time.AfterFunc(DebounceDelay, func() {
		rep, err := s.ExportAll()
		if err != nil {
			logger.LogErr(err, "automatic sync export failed", "dir", s.dir)
			return
		}
		if rep.Exported() > 0 {
			logger.Info("sync export", "files", rep.Exported(), "dir", s.dir)
		}
	})
}

// pendingExport reports whether a debounced export is scheduled. Exists for
// the tests, which must read the timer under the same lock Touch writes it
// under — the race detector is right to object otherwise.
func (s *Syncer) pendingExport() bool {
	s.timerMu.Lock()
	defer s.timerMu.Unlock()
	return s.timer != nil
}

// Flush cancels any pending debounce and exports synchronously. Called on
// graceful shutdown, where "the timer would have fired in another second" is
// not good enough.
func (s *Syncer) Flush() (Report, error) {
	s.timerMu.Lock()
	s.stopped = true
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.timerMu.Unlock()

	return s.ExportAll()
}

// run is the one reconciliation path; the exported methods differ only in which
// directions they enable.
func (s *Syncer) run(doImport, doExport bool) (Report, error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()

	started := time.Now()
	rep := Report{Dir: s.dir, StartedAt: started.UTC()}

	if err := s.ensureDirs(); err != nil {
		return rep, err
	}

	// The four entities are independent, and each one's failure is recorded in
	// its own line of the report rather than abandoning the pass: a malformed
	// sermon file must not stop notes from arriving.
	rep.Entities = append(rep.Entities,
		syncEntity(s, study.SyncKindNote, "notes", doImport, doExport,
			study.ExportNotes,
			func(rec study.NoteRecord) (applied, error) {
				return applied{Yes: true}, study.ApplyNote(s.db, rec)
			}),

		syncEntity(s, study.SyncKindTag, "tags", doImport, doExport,
			study.ExportTags,
			func(rec study.TagRecord) (applied, error) {
				outcome, err := study.ApplyTag(s.db, rec)
				switch outcome {
				case study.TagMerged:
					return applied{Yes: true, Note: "merged \"" + rec.Name +
						"\" into the tag of that name already here"}, err
				case study.TagUnchanged:
					// Either it failed, or the local tag of this name was
					// already at least this new. Neither is an import.
					return applied{}, err
				}
				return applied{Yes: true}, err
			}),

		syncEntity(s, study.SyncKindXref, "xrefs", doImport, doExport,
			study.ExportXrefs,
			func(rec study.XrefRecord) (applied, error) {
				return applied{Yes: true}, study.ApplyXref(s.db, rec)
			}),

		syncEntity(s, study.SyncKindSermon, "sermons", doImport, doExport,
			study.ExportSermons,
			func(rec study.SermonRecord) (applied, error) {
				return applied{Yes: true}, study.ApplySermon(s.db, rec)
			}),
	)

	rep.Duration = time.Since(started)
	return rep, nil
}

// applied is what an apply function reports back: whether the record actually
// changed anything here, and anything about it worth telling the user.
//
// The second look matters for tags, whose identity across machines is the name
// rather than the id — the clock comparison this function makes cannot see a
// local row filed under a different id, so ApplyTag makes its own and says
// whether it did anything. Counting a no-op as an import would leave every
// sync claiming work it did not do.
type applied struct {
	Yes  bool
	Note string
}

// syncEntity reconciles one entity's folder with one entity's table.
//
// Generic over the record type because the four passes differ only in which
// table is read and which apply function is called — everything else (the
// decode, the version check, the clock comparison, the atomic write, the
// counting) is identical, and four copies of it would drift.
func syncEntity[T study.Syncable](s *Syncer, kind, folder string,
	doImport, doExport bool,
	export func(*sql.DB) ([]T, error),
	apply func(T) (applied, error)) EntityReport {

	rep := EntityReport{Kind: kind}
	dir := s.entityDir(folder)

	if doImport {
		files, err := readRecords[T](dir, kind, &rep)
		if err != nil {
			rep.fail("cannot read " + folder + ": " + err.Error())
			return rep
		}

		local, err := export(s.db)
		if err != nil {
			rep.fail("cannot read local " + folder + ": " + err.Error())
			return rep
		}
		stamps := stampsOf(local)

		for _, rec := range files {
			// A row we already have and have not changed since the file was
			// written is the common case by far — most of a sync dir is
			// unchanged on any given run.
			if have, ok := stamps[rec.SyncID()]; ok && !newer(rec.SyncStamp(), have) {
				rep.Skipped++
				continue
			}
			did, err := apply(rec)
			if err != nil {
				rep.fail(err.Error())
				continue
			}
			if !did.Yes {
				rep.Skipped++
				continue
			}
			rep.Imported++
			if did.Note != "" {
				rep.Note(did.Note)
			}
		}
	}

	if doExport {
		// Re-read after importing: a row that just took an incoming edit must
		// not then be written back out as though it were a local change.
		local, err := export(s.db)
		if err != nil {
			rep.fail("cannot read local " + folder + ": " + err.Error())
			return rep
		}
		files, err := readStamps(dir)
		if err != nil {
			rep.fail("cannot read " + folder + ": " + err.Error())
			return rep
		}

		for _, rec := range local {
			if have, ok := files[rec.SyncID()]; ok && !newer(rec.SyncStamp(), have) {
				rep.Skipped++
				continue
			}
			if err := writeRecord(dir, rec); err != nil {
				rep.fail(err.Error())
				continue
			}
			rep.Exported++
		}
	}

	return rep
}

// stampsOf indexes records by id for the clock comparison.
func stampsOf[T study.Syncable](recs []T) map[string]time.Time {
	out := make(map[string]time.Time, len(recs))
	for _, r := range recs {
		out[r.SyncID()] = r.SyncStamp()
	}
	return out
}

// newer answers "does a win over b", at the precision the storage actually
// keeps.
//
// bytdb stores TIMESTAMP to microseconds, so a time that went into the database
// with nanoseconds comes back truncated. Comparing raw values would make a
// freshly imported row look permanently older than the file it came from, and
// every run would re-import it. Truncating both sides makes the comparison mean
// what it says. Ties lose: an equal clock is not evidence of a change.
func newer(a, b time.Time) bool {
	return a.Truncate(time.Microsecond).After(b.Truncate(time.Microsecond))
}
