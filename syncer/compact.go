package syncer

import (
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/rohanthewiz/pbstudy/study"
)

// This file is the sync engine's garbage collection: the pass that finally
// removes tombstones once they have done their job.
//
// # Why a tombstone cannot simply be deleted
//
// A delete that leaves no trace is undone by the next import, which creates any
// record it has no local row for. So a delete sets deleted_at and the row's file
// keeps sitting in the sync folder announcing "this is gone" to every machine
// that reads the folder. Both the row and the file have to outlive the last
// machine that had not yet heard.
//
// # Why compaction reconciles first
//
// Compaction removes a file as well as a row, which throws away the only record
// of an event. Anything the folder is still holding must therefore be applied
// here before that happens — an undelete written on the other machine, or a
// delete this machine has not seen. An import pass makes the database the
// complete picture, so every question compaction then asks can be answered from
// it. Importing is not itself destructive; it is what `serve` does at startup.
//
// The import and the removals happen under one hold of runMu. A debounced export
// slipping in between them would rewrite files this pass had already decided to
// delete.
//
// # What is still true afterwards
//
// Compaction is per machine. A machine that has not compacted still holds the
// tombstone row, exports its file again, and this machine imports it back —
// re-creating a tombstone row that its own next compaction removes again. That
// churn is bounded, converges the moment both machines compact, and never
// resurrects live data: what comes back is a tombstone, and a tombstone is
// invisible to every read in study/.
//
// The genuine hazard is a machine that has been offline longer than the
// retention window: it still holds the row alive, has never seen the delete, and
// will export it as a live record. That is what the window is sized against,
// and why the default is generous.

// DefaultRetention is how long a tombstone is kept before compaction may take
// it away.
//
// Ninety days is chosen against the machine, not the data: a laptop that spent a
// season in a drawer should still learn about a delete when it comes back, and a
// sync daemon that has been silently broken since spring should be noticed by a
// human before its evidence is thrown out. The cost of waiting is a few hundred
// bytes per deleted row, so there is nothing to buy by being aggressive.
const DefaultRetention = 90 * 24 * time.Hour

// CompactReport is what one compaction pass did.
//
// Like Report, this is the whole user interface for an operation whose success
// is otherwise invisible — and unlike a sync, this one cannot be repeated to
// recover from a misunderstanding. It names the cutoff it used, so the number
// the user typed and the date it turned into are both on the screen.
type CompactReport struct {
	Dir       string
	Cutoff    time.Time
	DryRun    bool
	StartedAt time.Time
	Duration  time.Duration

	// Reconcile is the import pass that ran first. Kept whole rather than
	// reduced to a count: if it could not read a file, that file was not a
	// candidate for removal either, and the user should hear about it here
	// rather than wonder why a tombstone survived.
	Reconcile Report

	Entities []EntityCompaction
}

// EntityCompaction is one entity's share of a compaction pass.
type EntityCompaction struct {
	// Kind is a study.SyncKind* value.
	Kind string

	Rows  int // tombstoned rows removed from the database
	Files int // records removed from the sync folder
	Kept  int // tombstones still inside the retention window

	// DryRun carries the pass's tense down to the line the user reads. Without
	// it a rehearsal reports "1 row removed", which is a receipt for something
	// that did not happen.
	DryRun bool

	// Problems are the files this pass could not read or could not remove.
	// Collected rather than returned for the same reason a sync collects
	// them: one stuck file must not stop the rest, and must not vanish.
	Problems []string
}

func (e *EntityCompaction) fail(msg string) { e.Problems = append(e.Problems, msg) }

// Label is the entity's plural name for display.
func (e EntityCompaction) Label() string { return kindLabel(e.Kind) }

// Changed reports whether this entity had anything removed.
func (e EntityCompaction) Changed() bool { return e.Rows > 0 || e.Files > 0 }

// Summary is the one-line description of what happened to this entity.
func (e EntityCompaction) Summary() string {
	if !e.Changed() && len(e.Problems) == 0 {
		if e.Kept == 0 {
			return "nothing to remove"
		}
		return "nothing to remove (" + plural(e.Kept, "tombstone") + " still recent)"
	}

	done := " removed"
	if e.DryRun {
		done = " to remove"
	}

	var parts []string
	if e.Rows > 0 {
		parts = append(parts, plural(e.Rows, "row")+done)
	}
	if e.Files > 0 {
		parts = append(parts, plural(e.Files, "file")+done)
	}
	if e.Kept > 0 {
		parts = append(parts, plural(e.Kept, "tombstone")+" still recent")
	}
	if len(e.Problems) > 0 {
		parts = append(parts, strconv.Itoa(len(e.Problems))+" could not be removed")
	}
	return strings.Join(parts, " · ")
}

// Rows totals the tombstones removed from the database.
func (r CompactReport) Rows() int { return r.totalOf(func(e EntityCompaction) int { return e.Rows }) }

// Files totals the records removed from the sync folder.
func (r CompactReport) Files() int { return r.totalOf(func(e EntityCompaction) int { return e.Files }) }

// Kept totals the tombstones still inside the retention window.
func (r CompactReport) Kept() int { return r.totalOf(func(e EntityCompaction) int { return e.Kept }) }

// Problems totals what this pass could not remove.
func (r CompactReport) Problems() int {
	return r.totalOf(func(e EntityCompaction) int { return len(e.Problems) })
}

func (r CompactReport) totalOf(of func(EntityCompaction) int) int {
	n := 0
	for _, e := range r.Entities {
		n += of(e)
	}
	return n
}

// Headline is the sentence a compaction leads with.
func (r CompactReport) Headline() string {
	verb := "Removed "
	if r.DryRun {
		verb = "Would remove "
	}

	var s string
	switch {
	case r.Rows() == 0 && r.Files() == 0:
		s = "Nothing was old enough to remove"
		if r.Kept() > 0 {
			s += " (" + plural(r.Kept(), "tombstone") + " still inside the window)"
		}
		s += "."
	default:
		s = verb + plural(r.Rows(), "tombstone") + " and " +
			plural(r.Files(), "file") + "."
	}

	if n := r.Problems(); n > 0 {
		s += " " + plural(n, "problem") + "."
	}
	return s
}

// Compact removes tombstones older than retention, and their files, from the
// database and the sync folder.
//
// A dry run reports exactly what a real one would remove and removes nothing —
// but it still performs the reconciliation described at the top of this file,
// because the answer is only meaningful against a database that has taken in
// what the folder is holding. That import is the same one `serve` runs at
// startup, and it never removes anything.
func (s *Syncer) Compact(retention time.Duration, dryRun bool) (CompactReport, error) {
	s.runMu.Lock()
	defer s.runMu.Unlock()

	started := time.Now()

	// Truncated to the precision bytdb actually stores, so the cutoff means the
	// same thing to the SQL comparison as it does to the record comparison —
	// the reason syncer.newer truncates as well.
	cutoff := started.UTC().Add(-retention).Truncate(time.Microsecond)

	rep := CompactReport{
		Dir:       s.dir,
		Cutoff:    cutoff,
		DryRun:    dryRun,
		StartedAt: started.UTC(),
	}

	if err := s.ensureDirs(); err != nil {
		return rep, err
	}

	rep.Reconcile = s.runLocked(true, false)

	rep.Entities = append(rep.Entities,
		compactEntity(s, study.SyncKindNote, "notes", cutoff, dryRun,
			study.ExportNotes, study.ExpiredNotes, study.PurgeNotes),

		compactEntity(s, study.SyncKindTag, "tags", cutoff, dryRun,
			study.ExportTags, study.ExpiredTags, study.PurgeTags),

		compactEntity(s, study.SyncKindXref, "xrefs", cutoff, dryRun,
			study.ExportXrefs, study.ExpiredXrefs, study.PurgeXrefs),

		compactEntity(s, study.SyncKindSermon, "sermons", cutoff, dryRun,
			study.ExportSermons, study.ExpiredSermons, study.PurgeSermons),
	)

	rep.Duration = time.Since(started)
	return rep, nil
}

// compactEntity compacts one entity's table and one entity's folder.
//
// Generic over the record type for the same reason syncEntity is: the four
// passes differ only in which table is read and which purge is called.
//
// Three sets are in play, and the difference between them is the whole logic:
//
//	expired  ids of rows here whose tombstone is older than the cutoff
//	local    ids of rows here at all, alive or not
//	files    records in the folder, decoded
//
// A row in expired takes its file with it. A file whose id is in neither expired
// nor local is a leftover — see the orphan comment below — and goes on its own
// if its own record says it was tombstoned before the cutoff.
func compactEntity[T study.Syncable](s *Syncer, kind, folder string,
	cutoff time.Time, dryRun bool,
	export func(*sql.DB) ([]T, error),
	expired func(*sql.DB, time.Time) ([]string, error),
	purge func(*sql.DB, []string) ([]string, error)) EntityCompaction {

	rep := EntityCompaction{Kind: kind, DryRun: dryRun}
	dir := s.entityDir(folder)

	local, err := export(s.db)
	if err != nil {
		rep.fail("cannot read local " + folder + ": " + err.Error())
		return rep
	}

	dead, err := expired(s.db, cutoff)
	if err != nil {
		rep.fail("cannot find expired " + folder + ": " + err.Error())
		return rep
	}

	files, err := readRecords[T](dir, kind, &rep)
	if err != nil {
		rep.fail("cannot read " + folder + ": " + err.Error())
		return rep
	}

	here := make(map[string]bool, len(local))
	for _, rec := range local {
		here[rec.SyncID()] = true

		// Everything tombstoned that the cutoff did not reach. Counted so a
		// pass that removes nothing can say whether that is because there is
		// nothing to remove or because the window has not passed yet.
		if d := rec.SyncDeleted(); !d.IsZero() && !d.Before(cutoff) {
			rep.Kept++
		}
	}

	// Orphaned files: a tombstone in the folder with no row of that id here.
	//
	// The reconciliation above imported every file it could, so a file that
	// still has no row is one of two things — a tag that ApplyTag folded into a
	// local tag of the same name under a different id (the redundant file every
	// machine leaves behind), or a record a previous compaction already removed
	// here whose file came back from a machine that has not compacted. Both are
	// finished business, and the record's own tombstone is the only clock there
	// is to judge them by.
	expiring := make(map[string]bool, len(dead))
	for _, id := range dead {
		expiring[id] = true
	}

	orphans := make([]string, 0, 4)
	for _, rec := range files {
		id := rec.SyncID()
		if here[id] || expiring[id] {
			continue
		}
		d := rec.SyncDeleted()
		if d.IsZero() {
			continue // a live record with no row here is a sync problem, not a compaction one
		}
		if !d.Before(cutoff) {
			rep.Kept++
			continue
		}
		orphans = append(orphans, id)
	}

	if dryRun {
		// Report what a real pass would take. The file count is the number of
		// files that are actually there, not the number of ids — a row whose
		// file was never written, or was already removed by hand, must not be
		// counted as a deletion that will happen.
		present, err := readStamps(dir)
		if err != nil {
			rep.fail("cannot read " + folder + ": " + err.Error())
			return rep
		}
		rep.Rows = len(dead)
		for _, id := range dead {
			if _, ok := present[id]; ok {
				rep.Files++
			}
		}
		rep.Files += len(orphans)
		return rep
	}

	// The row goes before its file. If the process dies between the two, what is
	// left is a file with no row — which the next reconciliation re-imports as a
	// tombstone and the next compaction removes again. The other order would
	// leave a row with no file, which exports itself straight back and looks
	// like the compaction never happened.
	removed, err := purge(s.db, dead)
	if err != nil {
		rep.fail("cannot remove expired " + folder + ": " + err.Error())
		return rep
	}
	rep.Rows = len(removed)

	// A fresh slice rather than appending onto removed: nothing reads removed
	// afterwards today, but an append that writes into its spare capacity is a
	// trap to leave lying around.
	stale := make([]string, 0, len(removed)+len(orphans))
	stale = append(stale, removed...)
	stale = append(stale, orphans...)

	for _, id := range stale {
		gone, err := removeRecord(dir, id)
		if err != nil {
			rep.fail(err.Error())
			continue
		}
		if gone {
			rep.Files++
		}
	}

	return rep
}
