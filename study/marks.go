package study

import (
	"database/sql"
	"strconv"

	"github.com/rohanthewiz/serr"

	"github.com/rohanthewiz/pbstudy/bible"
)

// Marks counts what the user has attached to one verse.
type Marks struct {
	Notes int
	Xrefs int
}

// Any reports whether anything is attached — the test the reader uses to
// decide whether to draw an indicator at all.
func (m Marks) Any() bool { return m.Notes > 0 || m.Xrefs > 0 }

// ChapterVerseKey is the map key ChapterMarks uses for marks that belong to
// the chapter as a whole rather than to any single verse. It is zero because
// that is exactly how a whole-chapter anchor is stored (verse_start = 0), so
// the same value flows from the schema to the map to the view unchanged.
const ChapterVerseKey = 0

// ChapterMarks returns the per-verse note and cross-reference counts for one
// chapter, keyed by verse number, with ChapterVerseKey holding whatever is
// attached to the chapter as a whole.
//
// This is what the reader's indicator dots are drawn from, and it is why it
// exists as a bulk query rather than as a lookup per verse: a chapter render
// asks once, not 176 times for Psalm 119.
//
// Ranges are expanded here rather than in SQL. A note on John 3:16-18 marks
// three verses, so the view can test one map key per verse it renders:
//
//	note_refs row              marks
//	(43, 3, 16, 18)      ->    16, 17, 18
//	(43, 3,  0,  0)      ->    ChapterVerseKey
func ChapterMarks(db *sql.DB, bookNum, chapter int) (map[int]Marks, error) {
	marks := map[int]Marks{}

	if err := markNotes(db, marks, bookNum, chapter); err != nil {
		return nil, err
	}
	if err := markXrefs(db, marks, bookNum, chapter); err != nil {
		return nil, err
	}
	return marks, nil
}

// markNotes adds the note counts.
//
// The join back to notes is what keeps tombstoned notes off the page.
// DeleteNote leaves note_refs rows in place on purpose (see its doc comment),
// so this filter is not optional — without it, deleted notes would keep
// showing dots in the reader forever.
func markNotes(db *sql.DB, marks map[int]Marks, bookNum, chapter int) error {
	rows, err := db.Query(
		`SELECT r.verse_start, r.verse_end
		   FROM note_refs r INNER JOIN notes n ON n.id = r.note_id
		  WHERE r.book_num = $1 AND r.chapter = $2 AND n.deleted_at IS NULL`,
		bookNum, chapter)
	if err != nil {
		return serr.Wrap(err, "cannot read chapter note marks",
			"book", strconv.Itoa(bookNum), "chapter", strconv.Itoa(chapter))
	}
	defer rows.Close()

	for rows.Next() {
		var start, end int
		if err := rows.Scan(&start, &end); err != nil {
			return serr.Wrap(err, "cannot scan note mark row")
		}
		for _, v := range expand(start, end) {
			m := marks[v]
			m.Notes++
			marks[v] = m
		}
	}
	if err := rows.Err(); err != nil {
		return serr.Wrap(err, "cannot iterate note mark rows")
	}
	return nil
}

// markXrefs adds the cross-reference counts.
//
// Both ends are fetched in ONE query rather than one query per direction, and
// each row is counted once per verse it touches. A cross-reference whose two
// ends land in the same chapter would otherwise be counted twice on the verses
// they share — a link from Genesis 1:1 to Genesis 1:3 is one correlation, not
// two.
func markXrefs(db *sql.DB, marks map[int]Marks, bookNum, chapter int) error {
	rows, err := db.Query(
		`SELECT from_book, from_chapter, from_verse,
		        to_book, to_chapter, to_verse_start, to_verse_end
		   FROM cross_refs
		  WHERE ((from_book = $1 AND from_chapter = $2)
		      OR (to_book = $1 AND to_chapter = $2))
		    AND deleted_at IS NULL`,
		bookNum, chapter)
	if err != nil {
		return serr.Wrap(err, "cannot read chapter cross-reference marks",
			"book", strconv.Itoa(bookNum), "chapter", strconv.Itoa(chapter))
	}
	defer rows.Close()

	for rows.Next() {
		var fromBook, fromChap, fromVerse int
		var toBook, toChap, toStart, toEnd int
		if err := rows.Scan(&fromBook, &fromChap, &fromVerse,
			&toBook, &toChap, &toStart, &toEnd); err != nil {
			return serr.Wrap(err, "cannot scan cross-reference mark row")
		}

		touched := map[int]bool{}
		if fromBook == bookNum && fromChap == chapter {
			touched[fromVerse] = true
		}
		if toBook == bookNum && toChap == chapter {
			for _, v := range expand(toStart, toEnd) {
				touched[v] = true
			}
		}
		for v := range touched {
			m := marks[v]
			m.Xrefs++
			marks[v] = m
		}
	}
	if err := rows.Err(); err != nil {
		return serr.Wrap(err, "cannot iterate cross-reference mark rows")
	}
	return nil
}

// expand turns a stored verse range into the verse numbers it covers.
//
// A whole-chapter anchor (0, 0) yields ChapterVerseKey. The upper bound is
// clamped to the longest chapter in scripture so a hand-edited range cannot
// make this allocate without limit.
func expand(start, end int) []int {
	if start <= 0 {
		return []int{ChapterVerseKey}
	}
	if end < start {
		end = start
	}
	if end > bible.MaxChapterVerses {
		end = bible.MaxChapterVerses
	}

	out := make([]int, 0, end-start+1)
	for v := start; v <= end; v++ {
		out = append(out, v)
	}
	return out
}
