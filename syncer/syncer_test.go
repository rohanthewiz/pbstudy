package syncer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/rohanthewiz/bytdb/stdlib"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/cfg"
	"github.com/rohanthewiz/pbstudy/store"
	"github.com/rohanthewiz/pbstudy/study"
)

// The tests in this file simulate two machines: two data directories, two
// stores, one shared sync directory standing in for the folder a sync daemon
// keeps in step. That is the only way to test the thing the package exists for
// — a round trip through files — and it is exactly the arrangement the plan
// asks for at this phase.

// host is one simulated machine.
type host struct {
	name  string
	store *store.Store
	sync  *Syncer
}

func newHost(t *testing.T, name, syncDir string) *host {
	t.Helper()

	st, err := store.Open(cfg.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("%s: store.Open() error: %v", name, err)
	}
	t.Cleanup(func() { _ = st.Close() })

	s, err := New(st.Study, syncDir)
	if err != nil {
		t.Fatalf("%s: syncer.New() error: %v", name, err)
	}
	return &host{name: name, store: st, sync: s}
}

// run reconciles and fails the test if the pass reported a problem. Problems
// are collected rather than returned by design, so a test that did not look at
// them would pass while every file was being rejected.
func (h *host) run(t *testing.T) Report {
	t.Helper()

	rep, err := h.sync.Run()
	if err != nil {
		t.Fatalf("%s: Run() error: %v", h.name, err)
	}
	for _, e := range rep.Entities {
		for _, p := range e.Problems {
			t.Errorf("%s: %s reported a problem: %s", h.name, e.Kind, p)
		}
	}
	return rep
}

var (
	john316 = bible.Ref{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 18}
	rom58   = bible.Ref{BookNum: 45, Chapter: 5, VerseStart: 8, VerseEnd: 8}
)

// TestTwoHostsRoundTrip is the phase's acceptance test: a note created on one
// machine arrives on the other, an edit made there wins back, and a delete
// propagates as a tombstone rather than as a resurrection.
func TestTwoHostsRoundTrip(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)
	b := newHost(t, "B", syncDir)

	// --- created on A ------------------------------------------------------
	noteID, err := study.CreateNote(a.store.Study, study.NoteDraft{
		Title:    "Demonstrated, not declared",
		BodyMD:   "God **showed** his love.\n\nSee [[G26]].",
		Refs:     []bible.Ref{john316, rom58},
		TagNames: []string{"Grace", "Love of God"},
	})
	if err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}
	xrefID, err := study.CreateXref(a.store.Study, bible.Ref{BookNum: 43, Chapter: 3, VerseStart: 16},
		rom58, "the same love, argued twice")
	if err != nil {
		t.Fatalf("CreateXref() error: %v", err)
	}
	sermonID, err := study.CreateSermon(a.store.Study, "The love of God")
	if err != nil {
		t.Fatalf("CreateSermon() error: %v", err)
	}
	if _, err := study.AppendSection(a.store.Study, sermonID,
		study.Section{Kind: study.KindPassage, Ref: john316}); err != nil {
		t.Fatalf("AppendSection() error: %v", err)
	}

	if rep := a.run(t); rep.Exported() == 0 {
		t.Fatal("A: first sync exported nothing")
	}

	// --- arrives on B ------------------------------------------------------
	if rep := b.run(t); rep.Imported() == 0 {
		t.Fatal("B: first sync imported nothing")
	}

	note, err := study.GetNote(b.store.Study, noteID)
	if err != nil {
		t.Fatalf("B: note did not arrive: %v", err)
	}
	if note.Title != "Demonstrated, not declared" {
		t.Errorf("B: note title = %q", note.Title)
	}
	if !strings.Contains(note.BodyMD, "**showed**") {
		t.Errorf("B: note body = %q", note.BodyMD)
	}
	if len(note.Refs) != 2 {
		t.Errorf("B: note has %d refs, want 2", len(note.Refs))
	}
	if len(note.Tags) != 2 {
		t.Errorf("B: note has %d tags, want 2", len(note.Tags))
	}
	// The anchors have to be real anchors on B, not just fields on a row: the
	// verse hub is the point of a note arriving at all.
	anchored, err := study.NotesForVerse(b.store.Study, 43, 3, 17)
	if err != nil || len(anchored) != 1 {
		t.Errorf("B: NotesForVerse(John 3:17) = %d notes, %v; want 1", len(anchored), err)
	}

	if _, err := study.GetXref(b.store.Study, xrefID); err != nil {
		t.Errorf("B: cross-reference did not arrive: %v", err)
	}
	sermon, err := study.GetSermon(b.store.Study, sermonID)
	if err != nil {
		t.Fatalf("B: sermon did not arrive: %v", err)
	}
	if len(sermon.Outline) != 1 || sermon.Outline[0].Ref != john316 {
		t.Errorf("B: sermon outline = %+v", sermon.Outline)
	}

	// --- an edit on B wins back on A --------------------------------------
	if err := study.UpdateNote(b.store.Study, noteID, study.NoteDraft{
		Title:    "Demonstrated at the cross",
		BodyMD:   "God **showed** his love while we were still sinners.",
		Refs:     []bible.Ref{rom58},
		TagNames: []string{"Grace"},
	}); err != nil {
		t.Fatalf("B: UpdateNote() error: %v", err)
	}
	b.run(t)
	a.run(t)

	note, err = study.GetNote(a.store.Study, noteID)
	if err != nil {
		t.Fatalf("A: note vanished: %v", err)
	}
	if note.Title != "Demonstrated at the cross" {
		t.Errorf("A: title after B's edit = %q", note.Title)
	}
	// Children are replaced wholesale, so the dropped anchor has to be gone —
	// a leftover would keep showing the note on John 3:16 forever.
	if len(note.Refs) != 1 || note.Refs[0].Ref != rom58 {
		t.Errorf("A: refs after B's edit = %+v", note.Refs)
	}
	stale, err := study.NotesForVerse(a.store.Study, 43, 3, 17)
	if err != nil || len(stale) != 0 {
		t.Errorf("A: John 3:17 still shows %d notes after the anchor was dropped", len(stale))
	}

	// --- a delete on A propagates as a tombstone --------------------------
	if err := study.DeleteNote(a.store.Study, noteID); err != nil {
		t.Fatalf("A: DeleteNote() error: %v", err)
	}
	a.run(t)
	b.run(t)

	if _, err := study.GetNote(b.store.Study, noteID); err == nil {
		t.Error("B: deleted note is still live")
	}

	// ...and the tombstone does not come back the next time round. This is the
	// failure mode tombstones exist to prevent: B's copy must not look to A
	// like a row A has never seen.
	b.run(t)
	a.run(t)
	if _, err := study.GetNote(a.store.Study, noteID); err == nil {
		t.Error("A: deleted note was resurrected by a later sync")
	}

	// --- a settled pair does nothing --------------------------------------
	// Convergence is not a nicety here: an export writes files a sync daemon
	// watches, so a pass that keeps finding work would keep waking it up.
	for _, h := range []*host{a, b} {
		rep := h.run(t)
		if rep.Imported() != 0 || rep.Exported() != 0 {
			t.Errorf("%s: settled sync still moved %d in / %d out (%s)",
				h.name, rep.Imported(), rep.Exported(), rep.Headline())
		}
	}

	// --- no live database ever lands in the sync directory ------------------
	assertNoLiveDB(t, syncDir)
}

// TestTagsConvergeByName covers the case that has no id-based answer: both
// machines create the same tag while offline, so the same idea exists under two
// ids and the local unique index on tags.name would reject the incoming one.
func TestTagsConvergeByName(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)
	b := newHost(t, "B", syncDir)

	// Same tag name, two machines, two ids — created before either has synced.
	if _, err := study.CreateNote(a.store.Study, study.NoteDraft{
		Title: "A's note", BodyMD: "a", TagNames: []string{"Grace"},
	}); err != nil {
		t.Fatalf("A: CreateNote() error: %v", err)
	}
	if _, err := study.CreateNote(b.store.Study, study.NoteDraft{
		Title: "B's note", BodyMD: "b", TagNames: []string{"grace"}, // note the case
	}); err != nil {
		t.Fatalf("B: CreateNote() error: %v", err)
	}

	// Two rounds each: the first exchanges, the second settles.
	a.run(t)
	b.run(t)
	a.run(t)
	b.run(t)

	for _, h := range []*host{a, b} {
		tags, err := study.ListTags(h.store.Study)
		if err != nil {
			t.Fatalf("%s: ListTags() error: %v", h.name, err)
		}
		if len(tags) != 1 {
			names := make([]string, 0, len(tags))
			for _, tag := range tags {
				names = append(names, tag.Name)
			}
			t.Fatalf("%s: %d tags after sync (%s), want 1", h.name, len(tags), strings.Join(names, ", "))
		}
		if tags[0].NoteCount != 2 {
			t.Errorf("%s: tag carries %d notes, want 2", h.name, tags[0].NoteCount)
		}
	}

	// And it stops moving: the name merge must not rewrite a timestamp on
	// every pass, or the two machines would trade the same tag forever.
	for _, h := range []*host{a, b} {
		rep := h.run(t)
		if rep.Imported() != 0 || rep.Exported() != 0 {
			t.Errorf("%s: settled tag sync still moved %d in / %d out",
				h.name, rep.Imported(), rep.Exported())
		}
	}
}

// TestTagDeletePropagates checks the asymmetry DeleteTag makes: the tag keeps a
// tombstone so the delete can travel, but its links to notes go for real on
// both machines.
func TestTagDeletePropagates(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)
	b := newHost(t, "B", syncDir)

	noteID, err := study.CreateNote(a.store.Study, study.NoteDraft{
		Title: "Tagged", BodyMD: "x", TagNames: []string{"Grace"},
	})
	if err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}
	a.run(t)
	b.run(t)

	tags, err := study.ListTags(a.store.Study)
	if err != nil || len(tags) != 1 {
		t.Fatalf("A: ListTags() = %d, %v", len(tags), err)
	}
	if err := study.DeleteTag(a.store.Study, tags[0].ID); err != nil {
		t.Fatalf("A: DeleteTag() error: %v", err)
	}

	a.run(t)
	b.run(t)

	bTags, err := study.ListTags(b.store.Study)
	if err != nil {
		t.Fatalf("B: ListTags() error: %v", err)
	}
	if len(bTags) != 0 {
		t.Errorf("B: %d tags still live after the delete propagated", len(bTags))
	}
	note, err := study.GetNote(b.store.Study, noteID)
	if err != nil {
		t.Fatalf("B: GetNote() error: %v", err)
	}
	if len(note.Tags) != 0 {
		t.Errorf("B: note still carries %d tags", len(note.Tags))
	}
}

// TestImportOnlyAndExportOnly pins the directions the startup and shutdown
// paths depend on: startup must not write, and the debounced export must not
// pull in a page of remote edits mid-session.
func TestImportOnlyAndExportOnly(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)
	b := newHost(t, "B", syncDir)

	if _, err := study.CreateNote(a.store.Study, study.NoteDraft{
		Title: "Only on A", BodyMD: "a",
	}); err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}
	if _, err := a.sync.ExportAll(); err != nil {
		t.Fatalf("A: ExportAll() error: %v", err)
	}

	if _, err := study.CreateNote(b.store.Study, study.NoteDraft{
		Title: "Only on B", BodyMD: "b",
	}); err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}

	rep, err := b.sync.ImportAll()
	if err != nil {
		t.Fatalf("B: ImportAll() error: %v", err)
	}
	if rep.Imported() != 1 {
		t.Errorf("B: ImportAll() imported %d, want 1", rep.Imported())
	}
	if rep.Exported() != 0 {
		t.Errorf("B: ImportAll() exported %d files; it must not write", rep.Exported())
	}

	// B's own note is still unpublished: import-only really is one direction.
	names := recordNames(t, filepath.Join(syncDir, "notes"))
	if len(names) != 1 {
		t.Errorf("sync dir holds %d note files after an import-only pass, want 1", len(names))
	}
}

// TestTouchDebouncedExport covers the automatic direction the web layer uses:
// a write happens, nothing is asked for, and the file appears on its own.
func TestTouchDebouncedExport(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)

	if _, err := study.CreateNote(a.store.Study, study.NoteDraft{
		Title: "Saved and forgotten", BodyMD: "x",
	}); err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}

	a.sync.Touch()
	a.sync.Touch() // a burst collapses; the second must not queue a second run

	// Flush is what shutdown does — it cancels the pending timer and exports
	// synchronously, so the test does not have to sleep out the debounce.
	rep, err := a.sync.Flush()
	if err != nil {
		t.Fatalf("Flush() error: %v", err)
	}
	if rep.Exported() != 1 {
		t.Errorf("Flush() exported %d, want 1", rep.Exported())
	}
	if names := recordNames(t, filepath.Join(syncDir, "notes")); len(names) != 1 {
		t.Errorf("notes folder holds %d files, want 1", len(names))
	}

	// After Flush the syncer is stopped: a Touch from a request still in
	// flight during shutdown must not schedule work nobody will wait for.
	a.sync.Touch()
	if a.sync.pendingExport() {
		t.Error("Touch() scheduled an export after Flush()")
	}
}

// --- helpers ---------------------------------------------------------------

func recordNames(t *testing.T, dir string) []string {
	t.Helper()

	names, err := recordFiles(dir)
	if err != nil {
		t.Fatalf("recordFiles(%s) error: %v", dir, err)
	}
	return names
}

// assertNoLiveDB walks the sync directory for database files. Only the backups
// folder may hold one, and only because those are engine snapshots rather than
// copies of a file being appended to.
func assertNoLiveDB(t *testing.T, syncDir string) {
	t.Helper()

	err := filepath.WalkDir(syncDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".bytdb" {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) != "backups" {
			t.Errorf("a database file reached the sync directory: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking sync dir: %v", err)
	}
}
