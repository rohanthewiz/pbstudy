package study

import (
	"database/sql"
	"testing"
	"time"

	"github.com/rohanthewiz/pbstudy/bible"
)

// countRows answers the question every test in this file is really asking: is
// the row physically gone? Every read in the package filters tombstones, so
// GetNote returning "not found" proves nothing about compaction.
func countRows(t *testing.T, db *sql.DB, table, id string) int {
	t.Helper()

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE id = $1`, id).
		Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func countChildren(t *testing.T, db *sql.DB, table, column, id string) int {
	t.Helper()

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+column+` = $1`, id).
		Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestPurgeNotesRemovesTheRowAndItsChildren is the load-bearing case: a
// tombstoned note leaves anchors and tag links behind on purpose, and this is
// the only pass that clears them. A purge that took the row but not the children
// would leave note_refs rows pointing at nothing — invisible to every query, and
// exported on every sync.
func TestPurgeNotesRemovesTheRowAndItsChildren(t *testing.T) {
	db := openSyncTestDB(t)

	id, err := CreateNote(db, NoteDraft{
		Title:    "Demonstrated, not declared",
		BodyMD:   "God showed his love.",
		Refs:     []bible.Ref{{BookNum: 45, Chapter: 5, VerseStart: 8, VerseEnd: 8}},
		TagNames: []string{"Grace"},
	})
	if err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}
	if err := DeleteNote(db, id); err != nil {
		t.Fatalf("DeleteNote() error: %v", err)
	}

	// The tombstone is what keeps the children alive until now.
	if n := countChildren(t, db, "note_refs", "note_id", id); n != 1 {
		t.Fatalf("after delete: note_refs = %d, want 1 (a tombstone keeps its anchors)", n)
	}

	// A cutoff in the future makes every tombstone expired, which is how these
	// tests avoid either sleeping or writing timestamps behind the API's back.
	dead, err := ExpiredNotes(db, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("ExpiredNotes() error: %v", err)
	}
	if len(dead) != 1 || dead[0] != id {
		t.Fatalf("ExpiredNotes() = %v, want [%s]", dead, id)
	}

	removed, err := PurgeNotes(db, dead)
	if err != nil {
		t.Fatalf("PurgeNotes() error: %v", err)
	}
	if len(removed) != 1 || removed[0] != id {
		t.Fatalf("PurgeNotes() = %v, want [%s]", removed, id)
	}

	if n := countRows(t, db, "notes", id); n != 0 {
		t.Errorf("notes row survived the purge (%d rows)", n)
	}
	if n := countChildren(t, db, "note_refs", "note_id", id); n != 0 {
		t.Errorf("note_refs survived the purge (%d rows)", n)
	}
	if n := countChildren(t, db, "note_tags", "note_id", id); n != 0 {
		t.Errorf("note_tags survived the purge (%d rows)", n)
	}

	// The tag itself is a separate entity with its own life; purging a note
	// must not take it.
	tags, err := ListTags(db)
	if err != nil {
		t.Fatalf("ListTags() error: %v", err)
	}
	if len(tags) != 1 {
		t.Errorf("tags after purging a note = %d, want 1", len(tags))
	}
}

// TestPurgeRefusesLiveRows is the guard that makes the id list safe to accept.
// The syncer assembles that list from a sync folder as well as from this
// database, so a hostile or merely stale file must not be able to talk this
// package into hard-deleting something the user can still see.
func TestPurgeRefusesLiveRows(t *testing.T) {
	db := openSyncTestDB(t)

	live, err := CreateNote(db, NoteDraft{
		Title:    "Still here",
		Refs:     []bible.Ref{{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 16}},
		TagNames: []string{"Grace"},
	})
	if err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}

	dead, err := CreateNote(db, NoteDraft{Title: "Gone"})
	if err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}
	if err := DeleteNote(db, dead); err != nil {
		t.Fatalf("DeleteNote() error: %v", err)
	}

	// Both ids handed over, as a confused caller would.
	removed, err := PurgeNotes(db, []string{live, dead})
	if err != nil {
		t.Fatalf("PurgeNotes() error: %v", err)
	}
	if len(removed) != 1 || removed[0] != dead {
		t.Fatalf("PurgeNotes() = %v, want only the tombstone [%s]", removed, dead)
	}

	if n := countRows(t, db, "notes", live); n != 1 {
		t.Error("the live note was hard-deleted")
	}
	// Its children must survive with it: the narrowing has to happen before
	// anything is deleted, not after.
	if n := countChildren(t, db, "note_refs", "note_id", live); n != 1 {
		t.Errorf("the live note lost its anchors (%d left)", n)
	}
	if n := countChildren(t, db, "note_tags", "note_id", live); n != 1 {
		t.Errorf("the live note lost its tag links (%d left)", n)
	}
}

// TestExpiredIsStrictlyBeforeTheCutoff pins the tie rule. An equal clock is the
// same tie syncer.newer refuses to break, and compaction is not the place to
// start breaking it.
func TestExpiredIsStrictlyBeforeTheCutoff(t *testing.T) {
	db := openSyncTestDB(t)

	id, err := CreateNote(db, NoteDraft{Title: "Gone"})
	if err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}
	if err := DeleteNote(db, id); err != nil {
		t.Fatalf("DeleteNote() error: %v", err)
	}

	// Read back the tombstone at the precision the storage keeps it, so the
	// comparison under test is the one the database will actually make.
	var deleted time.Time
	if err := db.QueryRow(`SELECT deleted_at FROM notes WHERE id = $1`, id).
		Scan(&deleted); err != nil {
		t.Fatalf("read deleted_at: %v", err)
	}
	deleted = deleted.UTC()

	if dead, err := ExpiredNotes(db, deleted); err != nil {
		t.Fatalf("ExpiredNotes() error: %v", err)
	} else if len(dead) != 0 {
		t.Errorf("a tombstone exactly at the cutoff was expired: %v", dead)
	}

	if dead, err := ExpiredNotes(db, deleted.Add(time.Microsecond)); err != nil {
		t.Fatalf("ExpiredNotes() error: %v", err)
	} else if len(dead) != 1 {
		t.Errorf("a tombstone before the cutoff was not expired: %v", dead)
	}
}

// TestPurgeTagsClearsLinks covers the belt-and-braces half of PurgeTags: the
// links are normally gone already, but a link written after the tombstone must
// not outlive the row it points at.
func TestPurgeTagsClearsLinks(t *testing.T) {
	db := openSyncTestDB(t)

	noteID, err := CreateNote(db, NoteDraft{Title: "Grace", TagNames: []string{"Grace"}})
	if err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}
	tags, err := ListTags(db)
	if err != nil || len(tags) != 1 {
		t.Fatalf("ListTags() = %v, %v; want one tag", tags, err)
	}
	tagID := tags[0].ID

	if err := DeleteTag(db, tagID); err != nil {
		t.Fatalf("DeleteTag() error: %v", err)
	}
	// Put a link back, standing in for an import that landed after the delete.
	if _, err := db.Exec(
		`INSERT INTO note_tags (id, note_id, tag_id, updated_at) VALUES ($1, $2, $3, $4)`,
		"link-after-delete", noteID, tagID, nowUTC()); err != nil {
		t.Fatalf("re-link: %v", err)
	}

	dead, err := ExpiredTags(db, time.Now().UTC().Add(time.Hour))
	if err != nil {
		t.Fatalf("ExpiredTags() error: %v", err)
	}
	if _, err := PurgeTags(db, dead); err != nil {
		t.Fatalf("PurgeTags() error: %v", err)
	}

	if n := countRows(t, db, "tags", tagID); n != 0 {
		t.Errorf("tag row survived the purge (%d rows)", n)
	}
	if n := countChildren(t, db, "note_tags", "tag_id", tagID); n != 0 {
		t.Errorf("note_tags survived the purge (%d rows)", n)
	}
}

// TestPurgeXrefsAndSermons covers the two entities with no children, including
// the property that matters most for the sync folder: after a purge, the export
// no longer carries the record at all.
func TestPurgeXrefsAndSermons(t *testing.T) {
	db := openSyncTestDB(t)

	xrefID, err := CreateXref(db,
		bible.Ref{BookNum: 43, Chapter: 3, VerseStart: 16},
		bible.Ref{BookNum: 45, Chapter: 5, VerseStart: 8, VerseEnd: 8}, "same love")
	if err != nil {
		t.Fatalf("CreateXref() error: %v", err)
	}
	sermonID, err := CreateSermon(db, "The love of God")
	if err != nil {
		t.Fatalf("CreateSermon() error: %v", err)
	}
	if err := DeleteXref(db, xrefID); err != nil {
		t.Fatalf("DeleteXref() error: %v", err)
	}
	if err := DeleteSermon(db, sermonID); err != nil {
		t.Fatalf("DeleteSermon() error: %v", err)
	}

	cutoff := time.Now().UTC().Add(time.Hour)

	deadX, err := ExpiredXrefs(db, cutoff)
	if err != nil {
		t.Fatalf("ExpiredXrefs() error: %v", err)
	}
	if _, err := PurgeXrefs(db, deadX); err != nil {
		t.Fatalf("PurgeXrefs() error: %v", err)
	}
	deadS, err := ExpiredSermons(db, cutoff)
	if err != nil {
		t.Fatalf("ExpiredSermons() error: %v", err)
	}
	if _, err := PurgeSermons(db, deadS); err != nil {
		t.Fatalf("PurgeSermons() error: %v", err)
	}

	// The export is what the sync folder is written from. A purged row that
	// still exports would be written straight back out again.
	xrefs, err := ExportXrefs(db)
	if err != nil {
		t.Fatalf("ExportXrefs() error: %v", err)
	}
	if len(xrefs) != 0 {
		t.Errorf("purged cross-reference still exports: %d record(s)", len(xrefs))
	}
	sermons, err := ExportSermons(db)
	if err != nil {
		t.Fatalf("ExportSermons() error: %v", err)
	}
	if len(sermons) != 0 {
		t.Errorf("purged sermon still exports: %d record(s)", len(sermons))
	}
}

// TestPurgeEmptyListIsANoOp guards the boundary an IN list cannot express: an
// empty placeholder list would be `IN ()`, which is a syntax error, so the
// caller-side early return has to hold.
func TestPurgeEmptyListIsANoOp(t *testing.T) {
	db := openSyncTestDB(t)

	id, err := CreateNote(db, NoteDraft{Title: "Untouched"})
	if err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}

	for _, purge := range []func(*sql.DB, []string) ([]string, error){
		PurgeNotes, PurgeTags, PurgeXrefs, PurgeSermons,
	} {
		removed, err := purge(db, nil)
		if err != nil {
			t.Fatalf("purge(nil) error: %v", err)
		}
		if len(removed) != 0 {
			t.Errorf("purge(nil) removed %v", removed)
		}
	}

	if n := countRows(t, db, "notes", id); n != 1 {
		t.Error("an empty purge touched a row")
	}
}
