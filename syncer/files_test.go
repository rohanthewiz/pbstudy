package syncer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/rohanthewiz/bytdb/stdlib"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/study"
)

// TestRecordFileNameRejectsPaths guards the one place a value from a file
// becomes a path. Ids are UUIDs this app minted — until one arrives from a
// machine, or a hand edit, that this app did not write.
func TestRecordFileNameRejectsPaths(t *testing.T) {
	ok := []string{
		"3f2b1c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d",
		"plain_id-1",
	}
	for _, id := range ok {
		if got := recordFileName(id); got != id+".json" {
			t.Errorf("recordFileName(%q) = %q, want %q", id, got, id+".json")
		}
	}

	bad := []string{
		"",
		"../../.ssh/id_rsa",
		"/etc/passwd",
		"a/b",
		"has space",
		"dot.dot",
		strings.Repeat("x", 129),
	}
	for _, id := range bad {
		if got := recordFileName(id); got != "" {
			t.Errorf("recordFileName(%q) = %q, want it refused", id, got)
		}
	}
}

// TestImportIgnoresWhatItShouldNotRead covers the three files that must not
// become rows: a half-delivered .tmp, a record in the wrong folder, and a file
// from a future version of this program.
func TestImportIgnoresWhatItShouldNotRead(t *testing.T) {
	syncDir := t.TempDir()
	h := newHost(t, "A", syncDir)

	notes := filepath.Join(syncDir, "notes")
	stamp := time.Now().UTC()

	// A file the daemon is still writing. Only .json is ever read, so the
	// half-written content never gets a chance to fail a decode.
	write(t, filepath.Join(notes, "half.json.tmp"), `{"kind":"note","v":1,"id":"half-note"`)

	// A tag record filed under notes/. Naming the mistake beats forcing it
	// through the note importer, which would happily make an untitled note.
	write(t, filepath.Join(notes, "misfiled.json"), mustJSON(t, study.TagRecord{
		Kind: study.SyncKindTag, V: study.SyncVersion, ID: "misfiled", Name: "Grace",
		UpdatedAt: stamp,
	}))

	// A record from a newer pbstudy. Skipped rather than half-read: importing
	// what we can parse would drop what we cannot, and the export half of the
	// same run would write that loss back over the file.
	write(t, filepath.Join(notes, "future.json"), mustJSON(t, study.NoteRecord{
		Kind: study.SyncKindNote, V: study.SyncVersion + 1, ID: "future",
		Title: "From next year", UpdatedAt: stamp,
	}))

	// And one good one, to prove the bad files did not stop the pass.
	write(t, filepath.Join(notes, "good.json"), mustJSON(t, study.NoteRecord{
		Kind: study.SyncKindNote, V: study.SyncVersion, ID: "good",
		Title: "Readable", BodyMD: "yes", CreatedAt: stamp, UpdatedAt: stamp,
	}))

	rep, err := h.sync.ImportAll()
	if err != nil {
		t.Fatalf("ImportAll() error: %v", err)
	}
	if rep.Imported() != 1 {
		t.Errorf("imported %d records, want 1", rep.Imported())
	}
	if rep.Problems() != 2 {
		t.Errorf("reported %d problems, want 2 (misfiled + future): %s", rep.Problems(), rep.Headline())
	}
	if _, err := study.GetNote(h.store.Study, "good"); err != nil {
		t.Errorf("the readable note did not import: %v", err)
	}
	if _, err := study.GetNote(h.store.Study, "future"); err == nil {
		t.Error("a record from a newer format was imported")
	}

	// The rejected files are left exactly as they were. A file this version
	// cannot read is not a file it may overwrite.
	for _, name := range []string{"future.json", "misfiled.json", "half.json.tmp"} {
		if _, err := os.Stat(filepath.Join(notes, name)); err != nil {
			t.Errorf("%s was removed or replaced: %v", name, err)
		}
	}
}

// TestWriteRecordIsAtomic checks that a record replaces its file by rename and
// leaves no temporary behind — a daemon watching this folder must never pick up
// half a record.
func TestWriteRecordIsAtomic(t *testing.T) {
	dir := t.TempDir()
	rec := study.NoteRecord{
		Kind: study.SyncKindNote, V: study.SyncVersion, ID: "note-1",
		Title: "First", UpdatedAt: time.Now().UTC(),
	}

	if err := writeRecord(dir, rec); err != nil {
		t.Fatalf("writeRecord() error: %v", err)
	}
	rec.Title = "Second"
	if err := writeRecord(dir, rec); err != nil {
		t.Fatalf("writeRecord() rewrite error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "note-1.json" {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("folder holds %v, want just note-1.json", names)
	}

	body, err := os.ReadFile(filepath.Join(dir, "note-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"Second"`) {
		t.Errorf("file was not replaced: %s", body)
	}
	// Indented and newline-terminated: these land in git repositories and
	// iCloud folders where a person reads the diff.
	if !strings.Contains(string(body), "\n  \"id\"") || !strings.HasSuffix(string(body), "\n") {
		t.Errorf("file is not the formatting a human diff wants: %q", body)
	}
}

// TestExportedNoteShape pins the wire format. These files outlive any one
// version of this program — someone's iCloud folder holds them for years — so
// the field names are a contract, not an implementation detail.
func TestExportedNoteShape(t *testing.T) {
	syncDir := t.TempDir()
	h := newHost(t, "A", syncDir)

	if _, err := study.CreateNote(h.store.Study, study.NoteDraft{
		Title:    "Demonstrated",
		BodyMD:   "God showed his love.",
		Refs:     []bible.Ref{{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 18}},
		TagNames: []string{"Grace"},
	}); err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}
	if _, err := h.sync.ExportAll(); err != nil {
		t.Fatalf("ExportAll() error: %v", err)
	}

	names := recordNames(t, filepath.Join(syncDir, "notes"))
	if len(names) != 1 {
		t.Fatalf("%d note files written, want 1", len(names))
	}
	body, err := os.ReadFile(filepath.Join(syncDir, "notes", names[0]))
	if err != nil {
		t.Fatal(err)
	}

	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("exported note is not JSON: %v", err)
	}
	for _, field := range []string{"kind", "v", "id", "title", "bodyMd", "createdAt", "updatedAt"} {
		if _, ok := got[field]; !ok {
			t.Errorf("exported note has no %q field: %s", field, body)
		}
	}
	// A live note carries no tombstone at all, rather than a null or a zero
	// time somebody has to know means "alive".
	if _, ok := got["deletedAt"]; ok {
		t.Errorf("a live note carries a deletedAt field: %s", body)
	}
	// Tags travel as names — the only identity two machines can agree on.
	tags, ok := got["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != "Grace" {
		t.Errorf("tags = %v, want [Grace]", got["tags"])
	}
}

// --- helpers ---------------------------------------------------------------

func write(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestConflictCopiesAreNotRecords covers the files a sync daemon leaves behind
// when the same note was edited on two machines while both were offline.
//
// Syncthing keeps the losing side as <id>.sync-conflict-<stamp>.json and iCloud
// as "<id> 2.json". Both still end in .json and both decode to the id of the
// real record, so reading them as records gave one id two or three files:
//
//	<id> 2.json                  newer stamp, stray
//	<id>.json                    the record
//	<id>.sync-conflict-….json    older stamp, stray
//
// Applied in directory order against stamps taken before the loop, the last
// file won whichever was newest, and a stray could also bring back a note
// whose row and real file had since been compacted away. The rule pinned here
// is the one fileExt's comment always claimed: a file not named for the id it
// carries is left alone. It is named in the report, because it is the only
// evidence that an edit lost a conflict and the only place its text survives.
func TestConflictCopiesAreNotRecords(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)

	id, err := study.CreateNote(a.store.Study, study.NoteDraft{Title: "Winner", BodyMD: "kept"})
	if err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}
	a.run(t)

	notes := filepath.Join(syncDir, "notes")
	now := time.Now().UTC().Truncate(time.Microsecond)
	older, newerStamp := now.Add(-time.Hour), now.Add(time.Hour)

	syncthing := id + ".sync-conflict-20261118-120000-ABCDEFG.json"
	icloud := id + " 2.json"
	write(t, filepath.Join(notes, syncthing), mustJSON(t, study.NoteRecord{
		Kind: study.SyncKindNote, V: study.SyncVersion, ID: id,
		Title: "Loser", BodyMD: "the other machine's edit", CreatedAt: older, UpdatedAt: older,
	}))
	write(t, filepath.Join(notes, icloud), mustJSON(t, study.NoteRecord{
		Kind: study.SyncKindNote, V: study.SyncVersion, ID: id,
		Title: "Stray but newer", BodyMD: "x", CreatedAt: older, UpdatedAt: newerStamp,
	}))

	// A second machine meets all three files with no row of its own, which is
	// the case where every one of them used to be applied.
	b := newHost(t, "B", syncDir)
	for pass := 1; pass <= 2; pass++ {
		rep, err := b.sync.Run()
		if err != nil {
			t.Fatalf("pass %d: Run() error: %v", pass, err)
		}

		got, err := study.GetNote(b.store.Study, id)
		if err != nil {
			t.Fatalf("pass %d: the note did not import: %v", pass, err)
		}
		if got.Title != "Winner" {
			t.Errorf("pass %d: note title = %q, want the record named for its id (Winner)", pass, got.Title)
		}

		// Both strays are named, every pass, until someone deals with them.
		var problems []string
		for _, e := range rep.Entities {
			problems = append(problems, e.Problems...)
		}
		joined := strings.Join(problems, "\n")
		if len(problems) != 2 || !strings.Contains(joined, syncthing) || !strings.Contains(joined, icloud) {
			t.Errorf("pass %d: problems = %q, want one naming each conflict copy", pass, problems)
		}
		if rep.Exported() != 0 {
			t.Errorf("pass %d: exported %d files; a stray's stamp must not trigger a rewrite", pass, rep.Exported())
		}
	}

	// The strays are evidence, not garbage: nothing removes or rewrites them.
	for _, name := range []string{syncthing, icloud} {
		if _, err := os.Stat(filepath.Join(notes, name)); err != nil {
			t.Errorf("%s was removed: %v", name, err)
		}
	}
}

// TestStrayCopyCannotResurrectACompactedNote: once a note's row and its real
// file are gone, a leftover conflict copy must not import it back, alive or
// tombstoned. A live stray would un-delete the note; a tombstoned one would
// be re-imported and re-purged by every compaction from then on.
func TestStrayCopyCannotResurrectACompactedNote(t *testing.T) {
	syncDir := t.TempDir()
	h := newHost(t, "A", syncDir)

	notes := filepath.Join(syncDir, "notes")
	stamp := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Microsecond)

	write(t, filepath.Join(notes, "gone-live.sync-conflict-20260101-000000-AAAAAAA.json"),
		mustJSON(t, study.NoteRecord{
			Kind: study.SyncKindNote, V: study.SyncVersion, ID: "gone-live",
			Title: "Deleted long ago", CreatedAt: stamp, UpdatedAt: stamp,
		}))
	write(t, filepath.Join(notes, "gone-dead 2.json"),
		mustJSON(t, study.NoteRecord{
			Kind: study.SyncKindNote, V: study.SyncVersion, ID: "gone-dead",
			Title: "Tombstone", CreatedAt: stamp, UpdatedAt: stamp, DeletedAt: &stamp,
		}))

	if _, err := h.sync.Run(); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
	rep, err := h.sync.Compact(time.Hour, false)
	if err != nil {
		t.Fatalf("Compact() error: %v", err)
	}
	if rep.Rows() != 0 || rep.Files() != 0 {
		t.Errorf("compaction removed %d rows and %d files; strays must not become rows",
			rep.Rows(), rep.Files())
	}

	all, err := study.ExportNotes(h.store.Study)
	if err != nil {
		t.Fatalf("ExportNotes() error: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("the database holds %d note rows, want none: a stray copy was imported", len(all))
	}
}
