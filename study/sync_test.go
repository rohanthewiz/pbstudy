package study

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/rohanthewiz/bytdb/stdlib"

	"github.com/rohanthewiz/pbstudy/bible"
)

// openSyncTestDB creates a study database with just the tables the sync path
// touches. It duplicates the DDL in store/schema.go on purpose: importing store
// from here would make the study package's tests depend on the whole app's
// startup, lock included.
func openSyncTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("bytdb", filepath.Join(t.TempDir(), "study.bytdb"))
	if err != nil {
		t.Fatalf("sql.Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE notes (id TEXT PRIMARY KEY, title TEXT, body_md TEXT,
		    created_at TIMESTAMP, updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
		`CREATE TABLE note_refs (id TEXT PRIMARY KEY, note_id TEXT, book_num INT,
		    chapter INT, verse_start INT, verse_end INT, updated_at TIMESTAMP)`,
		`CREATE TABLE tags (id TEXT PRIMARY KEY, name TEXT, descrip TEXT,
		    updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
		`CREATE UNIQUE INDEX idx_tags_name ON tags (name)`,
		`CREATE TABLE note_tags (id TEXT PRIMARY KEY, note_id TEXT, tag_id TEXT,
		    updated_at TIMESTAMP)`,
		`CREATE TABLE cross_refs (id TEXT PRIMARY KEY, from_book INT, from_chapter INT,
		    from_verse INT, to_book INT, to_chapter INT, to_verse_start INT,
		    to_verse_end INT, comment TEXT, created_at TIMESTAMP,
		    updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
		`CREATE TABLE sermons (id TEXT PRIMARY KEY, title TEXT, outline JSONB,
		    draft_md TEXT, status TEXT, created_at TIMESTAMP, updated_at TIMESTAMP,
		    deleted_at TIMESTAMP)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("schema: %v", err)
		}
	}
	return db
}

// TestApplyNoteRoundTrip is the load-bearing property of the whole sync layer:
// what comes out of ExportNotes, put back in by ApplyNote, produces the same
// record. If that is not true, two machines cannot converge no matter how
// carefully the clocks are compared.
func TestApplyNoteRoundTrip(t *testing.T) {
	src := openSyncTestDB(t)
	dst := openSyncTestDB(t)

	if _, err := CreateNote(src, NoteDraft{
		Title:    "Demonstrated, not declared",
		BodyMD:   "God **showed** his love.\n\nSee [[G26]].",
		Refs:     []bible.Ref{{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 18}},
		TagNames: []string{"Grace", "Love of God"},
	}); err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}

	exported, err := ExportNotes(src)
	if err != nil {
		t.Fatalf("ExportNotes() error: %v", err)
	}
	if len(exported) != 1 {
		t.Fatalf("exported %d notes, want 1", len(exported))
	}

	if err := ApplyNote(dst, exported[0]); err != nil {
		t.Fatalf("ApplyNote() error: %v", err)
	}
	// The tags travelled as names, so they have to exist on the far side as
	// rows before the note can link to them.
	for _, rec := range mustExportTags(t, src) {
		if _, err := ApplyTag(dst, rec); err != nil {
			t.Fatalf("ApplyTag() error: %v", err)
		}
	}

	got, err := ExportNotes(dst)
	if err != nil {
		t.Fatalf("ExportNotes(dst) error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("destination holds %d notes, want 1", len(got))
	}

	want := exported[0]
	have := got[0]
	if have.ID != want.ID || have.Title != want.Title || have.BodyMD != want.BodyMD {
		t.Errorf("note differs after round trip:\n got %+v\nwant %+v", have, want)
	}
	if !have.UpdatedAt.Equal(want.UpdatedAt) || !have.CreatedAt.Equal(want.CreatedAt) {
		t.Errorf("timestamps differ after round trip: got %s/%s want %s/%s",
			have.CreatedAt, have.UpdatedAt, want.CreatedAt, want.UpdatedAt)
	}
	if len(have.Refs) != 1 || have.Refs[0] != want.Refs[0] {
		t.Errorf("refs after round trip = %+v, want %+v", have.Refs, want.Refs)
	}
	if len(have.Tags) != 2 {
		t.Errorf("tags after round trip = %v, want 2", have.Tags)
	}
	if have.DeletedAt != nil {
		t.Errorf("a live note came back tombstoned: %v", have.DeletedAt)
	}
}

// TestApplyNoteTombstone checks that a nil tombstone really writes NULL and a
// set one really writes a time — the two states the whole delete-propagation
// story rests on, through a driver that has its own ideas about parameters.
func TestApplyNoteTombstone(t *testing.T) {
	db := openSyncTestDB(t)
	when := time.Now().UTC().Truncate(time.Microsecond)

	rec := NoteRecord{
		Kind: SyncKindNote, V: SyncVersion, ID: "n1", Title: "Gone",
		CreatedAt: when, UpdatedAt: when, DeletedAt: &when,
	}
	if err := ApplyNote(db, rec); err != nil {
		t.Fatalf("ApplyNote() error: %v", err)
	}
	if _, err := GetNote(db, "n1"); err == nil {
		t.Error("a tombstoned record imported as a live note")
	}

	out, err := ExportNotes(db)
	if err != nil {
		t.Fatalf("ExportNotes() error: %v", err)
	}
	if len(out) != 1 || out[0].DeletedAt == nil {
		t.Fatalf("tombstone did not survive the round trip: %+v", out)
	}
	if !out[0].DeletedAt.Equal(when) {
		t.Errorf("tombstone = %s, want %s", out[0].DeletedAt, when)
	}

	// ...and an undelete clears it, which is what an incoming record with no
	// deletedAt field has to mean.
	rec.DeletedAt = nil
	rec.UpdatedAt = when.Add(time.Second)
	if err := ApplyNote(db, rec); err != nil {
		t.Fatalf("ApplyNote() undelete error: %v", err)
	}
	if _, err := GetNote(db, "n1"); err != nil {
		t.Errorf("note did not come back: %v", err)
	}
}

// TestApplyTagOutcomes covers the three answers ApplyTag can give, including
// the one that keeps a repeated sync from finding work forever.
func TestApplyTagOutcomes(t *testing.T) {
	db := openSyncTestDB(t)
	t0 := time.Now().UTC().Truncate(time.Microsecond)

	// A tag this machine has never seen: written under its own id.
	incoming := TagRecord{
		Kind: SyncKindTag, V: SyncVersion, ID: "remote-1", Name: "Grace",
		Descrip: "unmerited favour", UpdatedAt: t0,
	}
	if got, err := ApplyTag(db, incoming); err != nil || got != TagWritten {
		t.Fatalf("ApplyTag(new) = %q, %v; want %q", got, err, TagWritten)
	}

	// The same file again, unchanged: nothing to do.
	if got, err := ApplyTag(db, incoming); err != nil || got != TagWritten {
		// Matching by id still counts as written — the row exists under this
		// id, so the clock comparison the syncer already made is the guard.
		t.Fatalf("ApplyTag(same id) = %q, %v", got, err)
	}

	// A DIFFERENT machine's id for the same name, older than what is here.
	// The unique index on tags.name makes an insert impossible, and the local
	// row is newer, so the honest answer is "nothing changed".
	older := TagRecord{
		Kind: SyncKindTag, V: SyncVersion, ID: "remote-2", Name: "grace",
		Descrip: "stale", UpdatedAt: t0.Add(-time.Minute),
	}
	if got, err := ApplyTag(db, older); err != nil || got != TagUnchanged {
		t.Fatalf("ApplyTag(older, other id) = %q, %v; want %q", got, err, TagUnchanged)
	}

	// The same shape, but newer: merged into the local row, id and spelling
	// kept.
	newer := older
	newer.Descrip = "the newer description"
	newer.UpdatedAt = t0.Add(time.Minute)
	if got, err := ApplyTag(db, newer); err != nil || got != TagMerged {
		t.Fatalf("ApplyTag(newer, other id) = %q, %v; want %q", got, err, TagMerged)
	}

	tags, err := ListTags(db)
	if err != nil {
		t.Fatalf("ListTags() error: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("%d tags after the merge, want 1", len(tags))
	}
	if tags[0].ID != "remote-1" || tags[0].Name != "Grace" {
		t.Errorf("merge did not keep the local identity: %+v", tags[0])
	}
	if tags[0].Descrip != "the newer description" {
		t.Errorf("merge did not take the newer description: %q", tags[0].Descrip)
	}
}

// TestApplySermonNormalizesStatus pins the one field that must not be believed.
// "drafting" describes a generator running in a process, and that process is on
// the other machine — possibly one that crashed.
func TestApplySermonNormalizesStatus(t *testing.T) {
	db := openSyncTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	cases := []struct {
		id, status, draft string
		want              string
	}{
		{"s1", StatusDrafting, "", StatusOutline},
		{"s2", StatusDrafting, "# A half-written draft", StatusDrafted},
		{"s3", StatusDrafted, "# Done", StatusDrafted},
		{"s4", "", "", StatusOutline},
	}

	for _, c := range cases {
		if err := ApplySermon(db, SermonRecord{
			Kind: SyncKindSermon, V: SyncVersion, ID: c.id, Title: c.id,
			Status: c.status, DraftMD: c.draft,
			Outline:   []Section{{ID: "a", Kind: KindPoint, Text: "one"}},
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("ApplySermon(%s) error: %v", c.id, err)
		}

		got, err := GetSermon(db, c.id)
		if err != nil {
			t.Fatalf("GetSermon(%s) error: %v", c.id, err)
		}
		if got.Status != c.want {
			t.Errorf("%s: status %q imported as %q, want %q", c.id, c.status, got.Status, c.want)
		}
		if len(got.Outline) != 1 || got.Outline[0].Text != "one" {
			t.Errorf("%s: outline did not survive: %+v", c.id, got.Outline)
		}
	}
}

// TestApplyXrefFillsSingleVerseTarget guards a field that is absent from the
// file by design: a single-verse target writes no verseEnd (omitempty), and a
// zero end would make XrefsTo's containment test match nothing.
func TestApplyXrefFillsSingleVerseTarget(t *testing.T) {
	db := openSyncTestDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := ApplyXref(db, XrefRecord{
		Kind: SyncKindXref, V: SyncVersion, ID: "x1",
		From:      bible.Ref{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 16},
		To:        bible.Ref{BookNum: 45, Chapter: 5, VerseStart: 8}, // no VerseEnd
		Comment:   "the same love",
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("ApplyXref() error: %v", err)
	}

	in, err := XrefsTo(db, 45, 5, 8)
	if err != nil {
		t.Fatalf("XrefsTo() error: %v", err)
	}
	if len(in) != 1 {
		t.Fatalf("Romans 5:8 shows %d inbound cross-references, want 1", len(in))
	}
	if in[0].To.VerseEnd != 8 {
		t.Errorf("target end = %d, want 8", in[0].To.VerseEnd)
	}
}

func mustExportTags(t *testing.T, db *sql.DB) []TagRecord {
	t.Helper()

	recs, err := ExportTags(db)
	if err != nil {
		t.Fatalf("ExportTags() error: %v", err)
	}
	return recs
}
