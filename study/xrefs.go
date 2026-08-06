package study

import (
	"database/sql"
	"strconv"
	"strings"
	"time"

	"github.com/rohanthewiz/serr"

	"github.com/rohanthewiz/pbstudy/bible"
)

// CrossRef is a user-drawn link between two places in scripture: "this verse
// speaks to that passage".
//
// The link is directional — From is where the user was standing when they drew
// it — but it is displayed from both ends. Reading Genesis 3:15 should surface
// the link someone drew to it from Romans 16:20, or the correlation is only
// half recorded.
type CrossRef struct {
	ID        string
	From      bible.Ref // always a single verse
	To        bible.Ref // a verse or a range
	Comment   string
	CreatedAt time.Time
	UpdatedAt time.Time

	// inbound records which end this row was loaded from, so the view can
	// label the direction and link to the far end without re-deriving it by
	// comparing refs against the page's own location. Unexported because only
	// the loader is entitled to decide it.
	inbound bool
}

// Inbound reports whether this cross-reference was found by looking at its To
// end — that is, whether it points at the verse being viewed rather than away
// from it.
func (x CrossRef) Inbound() bool { return x.inbound }

// Other returns the end of the link the viewer is NOT standing on: the far
// side of the correlation, which is what the view links to.
func (x CrossRef) Other() bible.Ref {
	if x.inbound {
		return x.From
	}
	return x.To
}

// CreateXref records a cross-reference from one verse to a verse or range.
func CreateXref(db *sql.DB, from, to bible.Ref, comment string) (string, error) {
	if from.BookNum < 1 || from.Chapter < 1 || from.VerseStart < 1 {
		return "", serr.New("cross-reference needs a source verse")
	}
	if to.BookNum < 1 || to.Chapter < 1 {
		return "", serr.New("cross-reference needs a target passage")
	}

	toEnd := to.VerseEnd
	if toEnd < to.VerseStart {
		toEnd = to.VerseStart
	}

	id := newID()
	now := nowUTC()

	if _, err := db.Exec(
		`INSERT INTO cross_refs (id, from_book, from_chapter, from_verse,
		     to_book, to_chapter, to_verse_start, to_verse_end,
		     comment, created_at, updated_at, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		id, from.BookNum, from.Chapter, from.VerseStart,
		to.BookNum, to.Chapter, to.VerseStart, toEnd,
		strings.TrimSpace(comment), now, now, nil); err != nil {
		return "", serr.Wrap(err, "cannot create cross-reference",
			"from", from.String(), "to", to.String())
	}
	return id, nil
}

// DeleteXref tombstones a cross-reference. Same reasoning as DeleteNote: the
// row has to survive for the delete to propagate during sync.
func DeleteXref(db *sql.DB, id string) error {
	now := nowUTC()
	res, err := db.Exec(
		`UPDATE cross_refs SET deleted_at = $1, updated_at = $1
		  WHERE id = $2 AND deleted_at IS NULL`, now, id)
	if err != nil {
		return serr.Wrap(err, "cannot delete cross-reference", "xref", id)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return serr.New("no such cross-reference", "xref", id)
	}
	return nil
}

// GetXref loads one live cross-reference — used to resolve the "back to" link
// after a delete.
func GetXref(db *sql.DB, id string) (CrossRef, error) {
	row := db.QueryRow(
		`SELECT id, from_book, from_chapter, from_verse,
		        to_book, to_chapter, to_verse_start, to_verse_end,
		        comment, created_at, updated_at
		   FROM cross_refs WHERE id = $1 AND deleted_at IS NULL`, id)

	var x CrossRef
	if err := row.Scan(&x.ID, &x.From.BookNum, &x.From.Chapter, &x.From.VerseStart,
		&x.To.BookNum, &x.To.Chapter, &x.To.VerseStart, &x.To.VerseEnd,
		&x.Comment, &x.CreatedAt, &x.UpdatedAt); err != nil {
		return x, serr.Wrap(err, "cross-reference not found", "xref", id)
	}
	x.From.VerseEnd = x.From.VerseStart
	return x, nil
}

// XrefsFrom returns cross-references the user drew *from* this verse.
func XrefsFrom(db *sql.DB, bookNum, chapter, verse int) ([]CrossRef, error) {
	rows, err := db.Query(
		`SELECT id, from_book, from_chapter, from_verse,
		        to_book, to_chapter, to_verse_start, to_verse_end,
		        comment, created_at, updated_at
		   FROM cross_refs
		  WHERE from_book = $1 AND from_chapter = $2 AND from_verse = $3
		    AND deleted_at IS NULL
		  ORDER BY to_book, to_chapter, to_verse_start`,
		bookNum, chapter, verse)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read outbound cross-references",
			"book", strconv.Itoa(bookNum), "chapter", strconv.Itoa(chapter),
			"verse", strconv.Itoa(verse))
	}
	return scanXrefs(rows, false)
}

// XrefsTo returns cross-references pointing *at* this verse — the other half
// of the correlation, which the user never explicitly created from here.
//
// Containment again, not equality: a link drawn to Romans 5:6-8 has to surface
// while reading verse 7.
func XrefsTo(db *sql.DB, bookNum, chapter, verse int) ([]CrossRef, error) {
	rows, err := db.Query(
		`SELECT id, from_book, from_chapter, from_verse,
		        to_book, to_chapter, to_verse_start, to_verse_end,
		        comment, created_at, updated_at
		   FROM cross_refs
		  WHERE to_book = $1 AND to_chapter = $2
		    AND to_verse_start <= $3 AND to_verse_end >= $3
		    AND deleted_at IS NULL
		  ORDER BY from_book, from_chapter, from_verse`,
		bookNum, chapter, verse)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read inbound cross-references",
			"book", strconv.Itoa(bookNum), "chapter", strconv.Itoa(chapter),
			"verse", strconv.Itoa(verse))
	}
	return scanXrefs(rows, true)
}

// CountXrefs returns how many live cross-references exist. Used by the
// dashboard.
func CountXrefs(db *sql.DB) (int, error) {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM cross_refs WHERE deleted_at IS NULL`).
		Scan(&n); err != nil {
		return 0, serr.Wrap(err, "cannot count cross-references")
	}
	return n, nil
}

// scanXrefs drains a cross-reference result set, closing rows on every path.
// inbound is the direction the caller queried from, stamped onto every row.
func scanXrefs(rows *sql.Rows, inbound bool) ([]CrossRef, error) {
	defer rows.Close()

	var out []CrossRef
	for rows.Next() {
		var x CrossRef
		if err := rows.Scan(&x.ID, &x.From.BookNum, &x.From.Chapter, &x.From.VerseStart,
			&x.To.BookNum, &x.To.Chapter, &x.To.VerseStart, &x.To.VerseEnd,
			&x.Comment, &x.CreatedAt, &x.UpdatedAt); err != nil {
			return nil, serr.Wrap(err, "cannot scan cross-reference row")
		}
		// The From end is always one verse; filling VerseEnd keeps Ref.String
		// from rendering it as an open range.
		x.From.VerseEnd = x.From.VerseStart
		x.inbound = inbound
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate cross-reference rows")
	}
	return out, nil
}
