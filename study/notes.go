package study

import (
	"database/sql"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/rohanthewiz/serr"

	"github.com/rohanthewiz/pbstudy/bible"
)

// Note is one study note.
//
// Refs and Tags are loaded alongside the note rather than lazily, because
// every place a note is displayed shows them — a note with no visible anchor
// is just a text file, and the anchor is the whole point of this app.
type Note struct {
	ID        string
	Title     string
	BodyMD    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Refs      []NoteRef
	Tags      []Tag
}

// NoteRef anchors a note to a verse range.
//
// The location is carried as a bible.Ref rather than four loose ints so the
// reference formatting, the whole-chapter convention (VerseStart == 0) and the
// range rules live in exactly one place.
type NoteRef struct {
	ID     string
	NoteID string
	Ref    bible.Ref
}

// NoteDraft is what the editor submits — the mutable half of a Note. Ids and
// timestamps are the database's business, so they are absent here and a
// handler cannot accidentally forge them.
type NoteDraft struct {
	Title    string
	BodyMD   string
	Refs     []bible.Ref
	TagNames []string
}

// DefaultNoteLimit caps the notes list. Generous enough that a personal study
// database will rarely reach it, small enough that the page stays renderable
// if it does.
const DefaultNoteLimit = 500

// CreateNote writes a new note with its anchors and tags, and returns its id.
//
// The whole save is one transaction: a note that landed without its verse
// anchors would be invisible everywhere it matters (the verse hub, the reader
// dots) while still looking fine on the notes list — a failure mode that is
// worse than no save at all.
func CreateNote(db *sql.DB, d NoteDraft) (string, error) {
	d.normalize()

	tx, err := db.Begin()
	if err != nil {
		return "", serr.Wrap(err, "cannot begin transaction")
	}
	defer func() { _ = tx.Rollback() }()

	id := newID()
	now := nowUTC()

	if _, err := tx.Exec(
		`INSERT INTO notes (id, title, body_md, created_at, updated_at, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		id, d.Title, d.BodyMD, now, now, nil); err != nil {
		return "", serr.Wrap(err, "cannot insert note")
	}

	if err := writeRefs(tx, id, d.Refs, now); err != nil {
		return "", err
	}
	if err := writeTagLinks(tx, id, d.TagNames, now); err != nil {
		return "", err
	}

	if err := tx.Commit(); err != nil {
		return "", serr.Wrap(err, "cannot commit note")
	}
	return id, nil
}

// UpdateNote replaces a note's content, anchors and tags.
//
// Anchors and tag links are rewritten wholesale rather than diffed. They have
// no identity of their own — nothing links to a note_refs row — so a diff
// would buy nothing but the chance to get it wrong, and the row counts are in
// single digits.
func UpdateNote(db *sql.DB, id string, d NoteDraft) error {
	d.normalize()

	tx, err := db.Begin()
	if err != nil {
		return serr.Wrap(err, "cannot begin transaction")
	}
	defer func() { _ = tx.Rollback() }()

	now := nowUTC()

	res, err := tx.Exec(
		`UPDATE notes SET title = $1, body_md = $2, updated_at = $3
		  WHERE id = $4 AND deleted_at IS NULL`,
		d.Title, d.BodyMD, now, id)
	if err != nil {
		return serr.Wrap(err, "cannot update note", "note", id)
	}
	// A zero row count means the id is unknown or already tombstoned. Report
	// it rather than committing a transaction that silently did nothing.
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return serr.New("no such note", "note", id)
	}

	if _, err := tx.Exec(`DELETE FROM note_refs WHERE note_id = $1`, id); err != nil {
		return serr.Wrap(err, "cannot clear note refs", "note", id)
	}
	if _, err := tx.Exec(`DELETE FROM note_tags WHERE note_id = $1`, id); err != nil {
		return serr.Wrap(err, "cannot clear note tags", "note", id)
	}

	if err := writeRefs(tx, id, d.Refs, now); err != nil {
		return err
	}
	if err := writeTagLinks(tx, id, d.TagNames, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return serr.Wrap(err, "cannot commit note update")
	}
	return nil
}

// DeleteNote tombstones a note.
//
// The row survives with deleted_at set, and its refs and tag links survive
// untouched. Two reasons, in order of importance:
//
//  1. The tombstone is what propagates the delete during sync. A physically
//     removed row looks identical to a row the other machine has not seen yet,
//     and would be resurrected on the next import.
//  2. Keeping the children means an undelete is a one-column UPDATE away if
//     one is ever wanted. Every read path in this package joins back to notes
//     and filters on deleted_at IS NULL, so the surviving children are inert
//     rather than stale — they cannot surface on the verse hub or the reader.
func DeleteNote(db *sql.DB, id string) error {
	now := nowUTC()
	res, err := db.Exec(
		`UPDATE notes SET deleted_at = $1, updated_at = $1 WHERE id = $2 AND deleted_at IS NULL`,
		now, id)
	if err != nil {
		return serr.Wrap(err, "cannot delete note", "note", id)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return serr.New("no such note", "note", id)
	}
	return nil
}

// GetNote loads one live note with its anchors and tags.
func GetNote(db *sql.DB, id string) (Note, error) {
	var n Note
	err := db.QueryRow(
		`SELECT id, title, body_md, created_at, updated_at FROM notes
		  WHERE id = $1 AND deleted_at IS NULL`, id).
		Scan(&n.ID, &n.Title, &n.BodyMD, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return n, serr.Wrap(err, "note not found", "note", id)
	}

	notes := []Note{n}
	if err := attachRefsAndTags(db, notes); err != nil {
		return n, err
	}
	return notes[0], nil
}

// ListNotes returns live notes, most recently edited first.
//
// Recency order rather than title order: a study session revisits what it was
// just working on far more often than it goes looking alphabetically.
func ListNotes(db *sql.DB, limit int) ([]Note, error) {
	if limit <= 0 {
		limit = DefaultNoteLimit
	}
	rows, err := db.Query(
		`SELECT id, title, body_md, created_at, updated_at FROM notes
		  WHERE deleted_at IS NULL
		  ORDER BY updated_at DESC
		  LIMIT $1`, limit)
	if err != nil {
		return nil, serr.Wrap(err, "cannot list notes")
	}
	notes, err := scanNotes(rows)
	if err != nil {
		return nil, err
	}
	if err := attachRefsAndTags(db, notes); err != nil {
		return nil, err
	}
	return notes, nil
}

// NotesForVerse returns every live note anchored to a range covering the
// given verse.
//
// The predicate is containment, not equality: a note on John 3:16-18 must
// surface while reading verse 17. A whole-chapter anchor is stored as
// verse_start = verse_end = 0 and therefore does NOT match a specific verse —
// see NotesForChapter for that case.
//
// # Two details the query cannot do without
//
// DISTINCT is load-bearing, not decoration. One note may carry several anchors
// covering the same verse (say John 3:16 and John 3:16-18), and the join would
// otherwise return it once per matching anchor.
//
// The ORDER BY key is written UNQUALIFIED on purpose. bytdb requires an
// ORDER BY expression under DISTINCT to match a name in the select list, and
// it matches on the output column name — "updated_at", not "n.updated_at".
// Qualifying it is rejected outright at execution time. The same applies to
// NotesForChapter and NotesForTag.
func NotesForVerse(db *sql.DB, bookNum, chapter, verse int) ([]Note, error) {
	rows, err := db.Query(
		`SELECT DISTINCT n.id, n.title, n.body_md, n.created_at, n.updated_at
		   FROM note_refs r INNER JOIN notes n ON n.id = r.note_id
		  WHERE r.book_num = $1 AND r.chapter = $2
		    AND r.verse_start <= $3 AND r.verse_end >= $3
		    AND n.deleted_at IS NULL
		  ORDER BY updated_at DESC`,
		bookNum, chapter, verse)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read notes for verse",
			"book", strconv.Itoa(bookNum), "chapter", strconv.Itoa(chapter),
			"verse", strconv.Itoa(verse))
	}
	notes, err := scanNotes(rows)
	if err != nil {
		return nil, err
	}
	if err := attachRefsAndTags(db, notes); err != nil {
		return nil, err
	}
	return notes, nil
}

// NotesForChapter returns live notes anchored to the chapter as a whole
// (verse_start = 0), as distinct from notes on individual verses in it.
func NotesForChapter(db *sql.DB, bookNum, chapter int) ([]Note, error) {
	rows, err := db.Query(
		`SELECT DISTINCT n.id, n.title, n.body_md, n.created_at, n.updated_at
		   FROM note_refs r INNER JOIN notes n ON n.id = r.note_id
		  WHERE r.book_num = $1 AND r.chapter = $2 AND r.verse_start = 0
		    AND n.deleted_at IS NULL
		  ORDER BY updated_at DESC`,
		bookNum, chapter)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read chapter notes",
			"book", strconv.Itoa(bookNum), "chapter", strconv.Itoa(chapter))
	}
	notes, err := scanNotes(rows)
	if err != nil {
		return nil, err
	}
	if err := attachRefsAndTags(db, notes); err != nil {
		return nil, err
	}
	return notes, nil
}

// RefsAcross collects the distinct passages a set of notes is anchored to, in
// canonical scripture order.
//
// This is what turns a tag from a list of notes into a topical study: the
// passages a topic actually touches, gathered from notes written weeks apart,
// ordered as they sit in the canon rather than as they were written about. It
// takes loaded notes rather than querying, because every caller already has
// them in hand — the tag page, and later the sermon builder.
//
// Identical ranges collapse; overlapping ones do not. "John 3:16" and
// "John 3:16-18" are two different citations of the same text and merging them
// would silently discard one note's idea of what the passage is.
func RefsAcross(notes []Note) []bible.Ref {
	seen := make(map[bible.Ref]bool)
	var out []bible.Ref

	for _, n := range notes {
		for _, r := range n.Refs {
			if seen[r.Ref] {
				continue
			}
			seen[r.Ref] = true
			out = append(out, r.Ref)
		}
	}

	slices.SortFunc(out, func(a, b bible.Ref) int { return a.Compare(b) })
	return out
}

// NotesByIDs loads the named live notes, keyed by id.
//
// Returns a map rather than a slice because every caller is resolving
// references that may have gone stale: the sermon assembler walks an outline
// whose note sections can point at notes since deleted, and a map answers
// "is this one still here" without a scan. Ids that no longer resolve are
// simply absent — the caller decides what a missing note means.
func NotesByIDs(db *sql.DB, ids []string) (map[string]Note, error) {
	out := map[string]Note{}
	if len(ids) == 0 {
		return out, nil
	}

	list, args := placeholders(ids)
	rows, err := db.Query(
		`SELECT id, title, body_md, created_at, updated_at FROM notes
		  WHERE id IN (`+list+`) AND deleted_at IS NULL`, args...)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read notes by id")
	}
	notes, err := scanNotes(rows)
	if err != nil {
		return nil, err
	}
	if err := attachRefsAndTags(db, notes); err != nil {
		return nil, err
	}

	for _, n := range notes {
		out[n.ID] = n
	}
	return out, nil
}

// CountNotes returns how many live notes exist. Used by the dashboard.
func CountNotes(db *sql.DB) (int, error) {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notes WHERE deleted_at IS NULL`).
		Scan(&n); err != nil {
		return 0, serr.Wrap(err, "cannot count notes")
	}
	return n, nil
}

// --- internals -------------------------------------------------------------

// normalize cleans a draft in place: whitespace trimmed, an empty title
// derived from the body, and unusable anchors dropped.
//
// Deriving the title matters more than it looks. A note is most often created
// from the verse hub in one motion — type a thought, save — and forcing a
// title first would break that flow. The first line of the body is what the
// user would have typed anyway.
func (d *NoteDraft) normalize() {
	d.Title = strings.TrimSpace(d.Title)
	d.BodyMD = strings.TrimSpace(d.BodyMD)

	if d.Title == "" {
		first, _, _ := strings.Cut(d.BodyMD, "\n")
		// Strip a leading Markdown heading marker so "# Grace" titles as
		// "Grace" rather than as its own markup.
		first = strings.TrimSpace(strings.TrimLeft(first, "#> \t"))
		if len(first) > 80 {
			first = strings.TrimSpace(first[:80]) + "…"
		}
		d.Title = first
	}
	if d.Title == "" {
		d.Title = "Untitled note"
	}

	live := d.Refs[:0]
	for _, r := range d.Refs {
		if r.BookNum >= 1 && r.BookNum <= len(bible.Books) && r.Chapter >= 1 {
			live = append(live, r)
		}
	}
	d.Refs = live
}

// writeRefs inserts a note's verse anchors.
//
// A single-verse Ref arrives with VerseEnd == VerseStart; a whole-chapter Ref
// arrives with both zero. Both are stored verbatim, so the containment
// predicate in NotesForVerse works without a special case.
func writeRefs(tx *sql.Tx, noteID string, refs []bible.Ref, now time.Time) error {
	if len(refs) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(
		`INSERT INTO note_refs (id, note_id, book_num, chapter, verse_start, verse_end, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`)
	if err != nil {
		return serr.Wrap(err, "cannot prepare note ref insert")
	}
	defer stmt.Close()

	for _, r := range refs {
		end := r.VerseEnd
		if end < r.VerseStart {
			end = r.VerseStart
		}
		if _, err := stmt.Exec(newID(), noteID, r.BookNum, r.Chapter,
			r.VerseStart, end, now); err != nil {
			return serr.Wrap(err, "cannot insert note ref", "ref", r.String())
		}
	}
	return nil
}

// scanNotes drains a note result set. It always closes rows, including on the
// error paths, so no caller has to remember a defer.
func scanNotes(rows *sql.Rows) ([]Note, error) {
	defer rows.Close()

	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.Title, &n.BodyMD, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, serr.Wrap(err, "cannot scan note row")
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate note rows")
	}
	return out, nil
}

// attachRefsAndTags fills in the children of an already-loaded batch of notes.
//
// Two queries total regardless of batch size, both keyed on an IN list of the
// note ids in hand. The alternative — a query per note — turns a 50-note list
// page into 101 round trips.
func attachRefsAndTags(db *sql.DB, notes []Note) error {
	if len(notes) == 0 {
		return nil
	}

	ids := make([]string, len(notes))
	byID := make(map[string]*Note, len(notes))
	for i := range notes {
		ids[i] = notes[i].ID
		byID[notes[i].ID] = &notes[i]
	}

	if err := attachRefs(db, ids, byID); err != nil {
		return err
	}
	return attachTags(db, ids, byID)
}

func attachRefs(db *sql.DB, ids []string, byID map[string]*Note) error {
	list, args := placeholders(ids)

	rows, err := db.Query(
		`SELECT id, note_id, book_num, chapter, verse_start, verse_end
		   FROM note_refs
		  WHERE note_id IN (`+list+`)
		  ORDER BY book_num, chapter, verse_start`, args...)
	if err != nil {
		return serr.Wrap(err, "cannot read note refs")
	}
	defer rows.Close()

	for rows.Next() {
		var r NoteRef
		if err := rows.Scan(&r.ID, &r.NoteID, &r.Ref.BookNum, &r.Ref.Chapter,
			&r.Ref.VerseStart, &r.Ref.VerseEnd); err != nil {
			return serr.Wrap(err, "cannot scan note ref row")
		}
		if n, ok := byID[r.NoteID]; ok {
			n.Refs = append(n.Refs, r)
		}
	}
	if err := rows.Err(); err != nil {
		return serr.Wrap(err, "cannot iterate note ref rows")
	}
	return nil
}
