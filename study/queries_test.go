package study

import (
	"database/sql"
	"testing"

	"github.com/rohanthewiz/pbstudy/bible"
)

// The three queries exercised here are the only ones in the package that put a
// qualified ORDER BY under a SELECT DISTINCT, and that combination has failed
// once before: bytdb v0.8.0 rejected it outright at execution time, which
// emptied the verse hub and the tag page while the reader's indicator dots —
// served by a different query — kept working. The shape looked like a UI bug
// and was found by a human reading a server log, not by the suite.
//
// So these tests assert the unglamorous thing first: the query runs at all.
// The row-level assertions that follow are what make them worth keeping once
// the execution risk is covered.
//
// Ordering is asserted as "non-increasing", never as a fixed sequence. Two
// notes written in the same microsecond are indistinguishable to the storage
// (bytdb keeps TIMESTAMP at microsecond precision), and a test that demanded a
// particular winner of that tie would fail on a fast enough machine.

// seedNote writes a note and fails the test if it does not land, keeping the
// table-setup noise out of the assertions below.
func seedNote(t *testing.T, db *sql.DB, d NoteDraft) string {
	t.Helper()
	id, err := CreateNote(db, d)
	if err != nil {
		t.Fatalf("CreateNote(%q) error: %v", d.Title, err)
	}
	return id
}

// assertNewestFirst checks the ORDER BY actually ordered, tolerating ties.
func assertNewestFirst(t *testing.T, notes []Note, label string) {
	t.Helper()
	for i := 1; i < len(notes); i++ {
		if notes[i-1].UpdatedAt.Before(notes[i].UpdatedAt) {
			t.Errorf("%s: note %d (%s) is older than note %d (%s); want newest first",
				label, i-1, notes[i-1].UpdatedAt, i, notes[i].UpdatedAt)
		}
	}
}

// TestNotesForVerseCollapsesOverlappingAnchors covers the reason DISTINCT is in
// the query at all: one note carrying two anchors that both cover John 3:16
// must still appear once. Without DISTINCT the join returns it per anchor.
func TestNotesForVerseCollapsesOverlappingAnchors(t *testing.T) {
	db := openSyncTestDB(t)

	id := seedNote(t, db, NoteDraft{
		Title:  "Two anchors over one verse",
		BodyMD: "God showed his love.",
		Refs: []bible.Ref{
			{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 18},
			{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 16},
		},
	})

	notes, err := NotesForVerse(db, 43, 3, 16)
	if err != nil {
		t.Fatalf("NotesForVerse() error: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("NotesForVerse(John 3:16) returned %d notes, want 1 (DISTINCT should collapse the two anchors)", len(notes))
	}
	if notes[0].ID != id {
		t.Errorf("returned note id = %q, want %q", notes[0].ID, id)
	}

	// Containment, not equality: the 16-18 anchor has to surface at verse 17.
	mid, err := NotesForVerse(db, 43, 3, 17)
	if err != nil {
		t.Fatalf("NotesForVerse(John 3:17) error: %v", err)
	}
	if len(mid) != 1 {
		t.Errorf("NotesForVerse(John 3:17) returned %d notes, want 1", len(mid))
	}

	// A verse outside every anchor has nothing to show.
	none, err := NotesForVerse(db, 43, 3, 19)
	if err != nil {
		t.Fatalf("NotesForVerse(John 3:19) error: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("NotesForVerse(John 3:19) returned %d notes, want 0", len(none))
	}
}

// TestNotesForChapter pins the split between a whole-chapter anchor and a
// verse-level one. The two are stored in the same table and separated only by
// verse_start = 0, so a query that confused them would quietly show chapter
// notes on every verse of that chapter.
func TestNotesForChapter(t *testing.T) {
	db := openSyncTestDB(t)

	chapterNote := seedNote(t, db, NoteDraft{
		Title:  "On John 3 as a whole",
		BodyMD: "The night visit.",
		Refs:   []bible.Ref{{BookNum: 43, Chapter: 3}},
	})
	seedNote(t, db, NoteDraft{
		Title:  "On John 3:16 only",
		BodyMD: "The verse itself.",
		Refs:   []bible.Ref{{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 16}},
	})
	deleted := seedNote(t, db, NoteDraft{
		Title:  "Retracted",
		BodyMD: "Withdrawn.",
		Refs:   []bible.Ref{{BookNum: 43, Chapter: 3}},
	})
	if err := DeleteNote(db, deleted); err != nil {
		t.Fatalf("DeleteNote() error: %v", err)
	}

	notes, err := NotesForChapter(db, 43, 3)
	if err != nil {
		t.Fatalf("NotesForChapter() error: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("NotesForChapter(John 3) returned %d notes, want 1 (the chapter-level one, tombstone excluded)", len(notes))
	}
	if notes[0].ID != chapterNote {
		t.Errorf("returned note %q (%q), want the chapter-level note %q",
			notes[0].ID, notes[0].Title, chapterNote)
	}

	// The chapter anchor must not leak into a verse read.
	atVerse, err := NotesForVerse(db, 43, 3, 16)
	if err != nil {
		t.Fatalf("NotesForVerse() error: %v", err)
	}
	for _, n := range atVerse {
		if n.ID == chapterNote {
			t.Error("the whole-chapter note surfaced on John 3:16; chapter anchors are verse_start = 0 and should not match a verse")
		}
	}
}

// TestNotesForTag covers the tag page's list, including the tombstone filter —
// a deleted note keeps its note_tags rows on purpose (DeleteNote tombstones
// only the note row), so the join is the only thing keeping it off the page.
func TestNotesForTag(t *testing.T) {
	db := openSyncTestDB(t)

	kept := seedNote(t, db, NoteDraft{
		Title:    "Grace abounding",
		BodyMD:   "Where sin abounded.",
		Refs:     []bible.Ref{{BookNum: 45, Chapter: 5, VerseStart: 20, VerseEnd: 20}},
		TagNames: []string{"Grace"},
	})
	alsoKept := seedNote(t, db, NoteDraft{
		Title:    "Grace and truth",
		BodyMD:   "Came by Jesus Christ.",
		Refs:     []bible.Ref{{BookNum: 43, Chapter: 1, VerseStart: 17, VerseEnd: 17}},
		TagNames: []string{"Grace"},
	})
	untagged := seedNote(t, db, NoteDraft{
		Title:  "Unrelated",
		BodyMD: "No tag here.",
		Refs:   []bible.Ref{{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 16}},
	})
	removed := seedNote(t, db, NoteDraft{
		Title:    "Retracted",
		BodyMD:   "Withdrawn.",
		Refs:     []bible.Ref{{BookNum: 45, Chapter: 6, VerseStart: 1, VerseEnd: 1}},
		TagNames: []string{"Grace"},
	})
	if err := DeleteNote(db, removed); err != nil {
		t.Fatalf("DeleteNote() error: %v", err)
	}

	tags, err := ListTags(db)
	if err != nil {
		t.Fatalf("ListTags() error: %v", err)
	}
	var graceID string
	for _, tg := range tags {
		if tg.Name == "Grace" {
			graceID = tg.ID
		}
	}
	if graceID == "" {
		t.Fatalf("tag %q was not created by CreateNote; got %+v", "Grace", tags)
	}

	notes, err := NotesForTag(db, graceID)
	if err != nil {
		t.Fatalf("NotesForTag() error: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("NotesForTag(Grace) returned %d notes, want 2 (tombstone and untagged excluded)", len(notes))
	}
	assertNewestFirst(t, notes, "NotesForTag")

	got := map[string]bool{}
	for _, n := range notes {
		got[n.ID] = true
	}
	for _, want := range []string{kept, alsoKept} {
		if !got[want] {
			t.Errorf("note %q missing from the tag page", want)
		}
	}
	for _, unwanted := range []string{untagged, removed} {
		if got[unwanted] {
			t.Errorf("note %q should not appear on the tag page", unwanted)
		}
	}
}
