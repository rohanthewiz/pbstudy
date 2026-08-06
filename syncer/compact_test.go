package syncer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/rohanthewiz/bytdb/stdlib"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/study"
)

// The compaction tests reach a cutoff without waiting for one. A negative
// retention puts the cutoff in the future, so every tombstone is expired; a
// positive one puts it in the past, so none is. That is the whole of the time
// dimension here, and it beats sleeping or writing timestamps behind the API's
// back — the rows under test are then the ones the real code paths made.
const (
	everything = -time.Second // cutoff a second from now: expire it all
	nothingYet = time.Hour    // cutoff an hour ago: expire none of it
)

// compact runs a compaction and fails the test on any reported problem, the way
// host.run does for a sync — problems are collected, not returned, so a test
// that ignored them would pass while nothing was being removed.
func (h *host) compact(t *testing.T, retention time.Duration, dryRun bool) CompactReport {
	t.Helper()

	rep, err := h.sync.Compact(retention, dryRun)
	if err != nil {
		t.Fatalf("%s: Compact() error: %v", h.name, err)
	}
	for _, e := range rep.Reconcile.Entities {
		for _, p := range e.Problems {
			t.Errorf("%s: compaction's reconcile reported a problem: %s", h.name, p)
		}
	}
	for _, e := range rep.Entities {
		for _, p := range e.Problems {
			t.Errorf("%s: compacting %s reported a problem: %s", h.name, e.Kind, p)
		}
	}
	return rep
}

// seed puts one of each entity on a host and returns their ids.
type seeded struct{ note, tag, xref, sermon string }

func seed(t *testing.T, h *host, title string) seeded {
	t.Helper()

	var s seeded
	var err error

	s.note, err = study.CreateNote(h.store.Study, study.NoteDraft{
		Title:    title,
		BodyMD:   "God showed his love.",
		Refs:     []bible.Ref{john316, rom58},
		TagNames: []string{title + " tag"},
	})
	if err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}

	tags, err := study.ListTags(h.store.Study)
	if err != nil {
		t.Fatalf("ListTags() error: %v", err)
	}
	for _, tag := range tags {
		if tag.Name == title+" tag" {
			s.tag = tag.ID
		}
	}
	if s.tag == "" {
		t.Fatalf("the tag for %q was not created", title)
	}

	s.xref, err = study.CreateXref(h.store.Study,
		bible.Ref{BookNum: 43, Chapter: 3, VerseStart: 16}, rom58, title)
	if err != nil {
		t.Fatalf("CreateXref() error: %v", err)
	}
	s.sermon, err = study.CreateSermon(h.store.Study, title)
	if err != nil {
		t.Fatalf("CreateSermon() error: %v", err)
	}
	return s
}

// deleteAll tombstones every entity in a seeded set.
func deleteAll(t *testing.T, h *host, s seeded) {
	t.Helper()

	if err := study.DeleteNote(h.store.Study, s.note); err != nil {
		t.Fatalf("DeleteNote() error: %v", err)
	}
	if err := study.DeleteTag(h.store.Study, s.tag); err != nil {
		t.Fatalf("DeleteTag() error: %v", err)
	}
	if err := study.DeleteXref(h.store.Study, s.xref); err != nil {
		t.Fatalf("DeleteXref() error: %v", err)
	}
	if err := study.DeleteSermon(h.store.Study, s.sermon); err != nil {
		t.Fatalf("DeleteSermon() error: %v", err)
	}
}

// exportedIDs is what a host would write to the folder — tombstones included,
// which is exactly why it is the right thing to assert against. A row that has
// been purged does not appear here; a row that has merely been tombstoned does.
func exportedIDs(t *testing.T, h *host) map[string]bool {
	t.Helper()

	out := map[string]bool{}

	notes, err := study.ExportNotes(h.store.Study)
	if err != nil {
		t.Fatalf("ExportNotes() error: %v", err)
	}
	for _, r := range notes {
		out[r.ID] = true
	}
	tags, err := study.ExportTags(h.store.Study)
	if err != nil {
		t.Fatalf("ExportTags() error: %v", err)
	}
	for _, r := range tags {
		out[r.ID] = true
	}
	xrefs, err := study.ExportXrefs(h.store.Study)
	if err != nil {
		t.Fatalf("ExportXrefs() error: %v", err)
	}
	for _, r := range xrefs {
		out[r.ID] = true
	}
	sermons, err := study.ExportSermons(h.store.Study)
	if err != nil {
		t.Fatalf("ExportSermons() error: %v", err)
	}
	for _, r := range sermons {
		out[r.ID] = true
	}
	return out
}

// folderIDs is every record file in the sync folder, by id.
func folderIDs(t *testing.T, syncDir string) map[string]bool {
	t.Helper()

	out := map[string]bool{}
	for _, folder := range []string{"notes", "tags", "xrefs", "sermons"} {
		for _, name := range recordNames(t, filepath.Join(syncDir, folder)) {
			out[name[:len(name)-len(fileExt)]] = true
		}
	}
	return out
}

// TestCompactRemovesExpiredTombstones is the acceptance test for the feature:
// after every machine has seen a delete, compaction takes the row and its file,
// a later sync does not bring either back, and the live data beside them is
// untouched.
func TestCompactRemovesExpiredTombstones(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)
	b := newHost(t, "B", syncDir)

	dead := seed(t, a, "Doomed")
	live := seed(t, a, "Kept")
	a.run(t)
	b.run(t)

	deleteAll(t, a, dead)
	a.run(t)
	b.run(t) // B learns about the deletes; both machines now hold the tombstones

	rep := a.compact(t, everything, false)

	// Four rows: the note, tag, cross-reference and sermon that were deleted.
	//
	// FIVE files, and the fifth is the point of the orphan branch. B minted its
	// own id for "Doomed tag" — ApplyNote's writeTagLinks creates a tag by name
	// before the tag's own file is ever read, which is the redundant file per
	// machine per name the sync design accepts — so the folder holds two
	// tombstoned files for the one retired tag. A has no row for B's id and
	// never will, so only compaction can ever remove it.
	if rep.Rows() != 4 {
		t.Errorf("compaction removed %d rows, want 4: %s", rep.Rows(), rep.Headline())
	}
	if rep.Files() != 5 {
		t.Errorf("compaction removed %d files, want 5 (4 records + B's duplicate "+
			"file for the retired tag): %s", rep.Files(), rep.Headline())
	}

	gone := []string{dead.note, dead.tag, dead.xref, dead.sermon}
	kept := []string{live.note, live.tag, live.xref, live.sermon}

	rows := exportedIDs(t, a)
	files := folderIDs(t, syncDir)
	for _, id := range gone {
		if rows[id] {
			t.Errorf("A still has a row for %s", id)
		}
		if files[id] {
			t.Errorf("the sync folder still has a file for %s", id)
		}
	}
	for _, id := range kept {
		if !rows[id] {
			t.Errorf("A lost the live row %s", id)
		}
		if !files[id] {
			t.Errorf("the sync folder lost the live file %s", id)
		}
	}

	// The live note must still be a whole note, children included: the purge
	// runs DELETE over note_refs and note_tags, and getting the id list wrong
	// there would be silent.
	note, err := study.GetNote(a.store.Study, live.note)
	if err != nil {
		t.Fatalf("GetNote() error: %v", err)
	}
	if len(note.Refs) != 2 || len(note.Tags) != 1 {
		t.Errorf("the live note came out of compaction with %d refs and %d tags, want 2 and 1",
			len(note.Refs), len(note.Tags))
	}

	// --- convergence -------------------------------------------------------
	//
	// B has not compacted, so it still holds the tombstone rows and writes their
	// files back. That churn is expected and bounded: what returns is a
	// tombstone, invisible to every read, and it goes for good once B compacts
	// too.
	b.run(t)
	a.run(t)
	b.compact(t, everything, false)

	// One more pass each to let the two settle, then nothing should move.
	a.compact(t, everything, false)
	b.run(t)

	if rep := a.run(t); rep.Imported() != 0 || rep.Exported() != 0 {
		t.Errorf("A is not settled after both machines compacted: %s", rep.Headline())
	}
	if rep := b.run(t); rep.Imported() != 0 || rep.Exported() != 0 {
		t.Errorf("B is not settled after both machines compacted: %s", rep.Headline())
	}

	files = folderIDs(t, syncDir)
	for _, id := range gone {
		if files[id] {
			t.Errorf("%s came back to the sync folder after both machines compacted", id)
		}
	}
	// The four live records, plus B's duplicate file for the live "Kept tag" —
	// which is alive, so compaction leaves it exactly where it is. Retiring
	// that tag is what would finally take it, on both machines.
	if len(files) != 5 {
		t.Errorf("the settled sync folder holds %d records, want 5 "+
			"(4 live records + B's duplicate file for the live tag)", len(files))
	}
}

// TestCompactKeepsRecentTombstones is the other half of the contract, and the
// one that protects a machine that has been away: a tombstone inside the
// retention window is evidence still in use, and compaction must not touch it.
func TestCompactKeepsRecentTombstones(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)

	dead := seed(t, a, "Recent")
	a.run(t)
	deleteAll(t, a, dead)
	a.run(t)

	rep := a.compact(t, nothingYet, false)

	if rep.Rows() != 0 || rep.Files() != 0 {
		t.Errorf("a fresh tombstone was compacted: %s", rep.Headline())
	}
	if rep.Kept() != 4 {
		t.Errorf("report counted %d tombstones still recent, want 4", rep.Kept())
	}

	files := folderIDs(t, syncDir)
	for _, id := range []string{dead.note, dead.tag, dead.xref, dead.sermon} {
		if !files[id] {
			t.Errorf("the file for %s was removed inside the retention window", id)
		}
		if !exportedIDs(t, a)[id] {
			t.Errorf("the row for %s was removed inside the retention window", id)
		}
	}
}

// TestCompactDryRunRemovesNothing checks that the rehearsal reports the same
// numbers as the performance. A dry run whose counts did not match the real pass
// would be worse than no dry run at all.
func TestCompactDryRunRemovesNothing(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)

	dead := seed(t, a, "Doomed")
	seed(t, a, "Kept")
	a.run(t)
	deleteAll(t, a, dead)
	a.run(t)

	before := folderIDs(t, syncDir)

	dry := a.compact(t, everything, true)
	if dry.Rows() != 4 || dry.Files() != 4 {
		t.Errorf("dry run predicted %d rows and %d files, want 4 and 4",
			dry.Rows(), dry.Files())
	}
	if !dry.DryRun {
		t.Error("the report does not say it was a dry run")
	}

	after := folderIDs(t, syncDir)
	if len(after) != len(before) {
		t.Errorf("the dry run removed files: %d before, %d after", len(before), len(after))
	}
	for _, id := range []string{dead.note, dead.tag, dead.xref, dead.sermon} {
		if !exportedIDs(t, a)[id] {
			t.Errorf("the dry run removed the row for %s", id)
		}
	}

	real := a.compact(t, everything, false)
	if real.Rows() != dry.Rows() || real.Files() != dry.Files() {
		t.Errorf("the real pass removed %d rows and %d files; the dry run said %d and %d",
			real.Rows(), real.Files(), dry.Rows(), dry.Files())
	}
}

// TestCompactRemovesOrphanedTombstoneFiles covers the one case the row-driven
// path cannot reach: a tag file whose id this machine has no row for, because
// ApplyTag folded it into a local tag of the same name. Those files are the
// residue the sync design deliberately leaves behind, and an expired tombstone
// among them is finished business.
func TestCompactRemovesOrphanedTombstoneFiles(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)

	// A local tag, alive and current.
	if _, err := study.CreateNote(a.store.Study, study.NoteDraft{
		Title: "Grace abounding", TagNames: []string{"Grace"},
	}); err != nil {
		t.Fatalf("CreateNote() error: %v", err)
	}
	a.run(t)

	// A tombstoned tag of the same name under another machine's id, deleted
	// long enough ago that it loses the clock comparison in ApplyTag — so the
	// import leaves no row behind for it, which is what makes it an orphan.
	old := time.Now().UTC().Add(-24 * time.Hour).Truncate(time.Microsecond)
	orphanID := "0000aaaa-0000-4000-8000-00000000dead"
	body, err := json.MarshalIndent(study.TagRecord{
		Kind: study.SyncKindTag, V: study.SyncVersion, ID: orphanID,
		Name: "Grace", UpdatedAt: old, DeletedAt: &old,
	}, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	path := filepath.Join(syncDir, "tags", orphanID+fileExt)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}

	rep := a.compact(t, everything, false)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the orphaned tombstone file survived compaction (stat err %v)", err)
	}
	if rep.Files() != 1 {
		t.Errorf("compaction removed %d files, want 1: %s", rep.Files(), rep.Headline())
	}
	if rep.Rows() != 0 {
		t.Errorf("compaction removed %d rows, want 0 — there was no row to remove", rep.Rows())
	}

	// The live tag it was folded into must be untouched, and still the only one.
	tags, err := study.ListTags(a.store.Study)
	if err != nil {
		t.Fatalf("ListTags() error: %v", err)
	}
	if len(tags) != 1 || tags[0].Name != "Grace" {
		t.Errorf("tags after compaction = %v, want just Grace", tags)
	}
}

// TestCompactLeavesALiveFolderAlone is the paranoid case: a machine with no
// deletes at all runs compaction and loses nothing, whatever the retention.
func TestCompactLeavesALiveFolderAlone(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)

	seed(t, a, "First")
	seed(t, a, "Second")
	a.run(t)

	before := folderIDs(t, syncDir)

	rep := a.compact(t, everything, false)
	if rep.Rows() != 0 || rep.Files() != 0 {
		t.Errorf("compaction removed something from a folder with no deletes: %s",
			rep.Headline())
	}
	if got := folderIDs(t, syncDir); len(got) != len(before) {
		t.Errorf("the folder holds %d records, want %d", len(got), len(before))
	}
	if got := len(exportedIDs(t, a)); got != len(before) {
		t.Errorf("the database holds %d rows, want %d", got, len(before))
	}
}

// TestCompactNeverWritesTheLiveDatabase re-asserts the invariant every pass in
// this package has to keep: the sync folder is JSON, and the .bytdb files stay
// out of it apart from deliberate snapshots.
func TestCompactNeverWritesTheLiveDatabase(t *testing.T) {
	syncDir := t.TempDir()
	a := newHost(t, "A", syncDir)

	dead := seed(t, a, "Doomed")
	a.run(t)
	deleteAll(t, a, dead)
	a.run(t)
	a.compact(t, everything, false)

	assertNoLiveDB(t, syncDir)

	// And nothing half-written was left behind.
	err := filepath.Walk(syncDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == tmpExt {
			t.Errorf("compaction left a temporary file: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}
