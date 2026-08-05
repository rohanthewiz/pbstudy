package bible

import (
	"database/sql"
	"strconv"
	"strings"

	"github.com/rohanthewiz/serr"
)

// DefaultTranslation is used when no translation is named in a URL or form.
const DefaultTranslation = "kjv"

// Verse is one verse row as read from the cache.
type Verse struct {
	Translation string
	BookNum     int
	Chapter     int
	Num         int
	Body        string
}

// Ref returns the verse's location as a single-verse Ref.
func (v Verse) Ref() Ref {
	return Ref{BookNum: v.BookNum, Chapter: v.Chapter, VerseStart: v.Num, VerseEnd: v.Num}
}

// Translation is a downloaded translation as recorded in the cache.
type Translation struct {
	Abbrev string
	Name   string
	Lang   string
}

// Known lists the translations pbstudy can download, in preference order.
// All three are public domain, which is the whole reason for this set.
var Known = []Translation{
	{"kjv", "King James Version", "English"},
	{"web", "World English Bible", "English"},
	{"asv", "American Standard Version", "English"},
}

// IsKnownTranslation reports whether abbrev names a translation we can fetch.
func IsKnownTranslation(abbrev string) bool {
	for _, t := range Known {
		if t.Abbrev == abbrev {
			return true
		}
	}
	return false
}

// Downloaded lists the translations actually present in the cache, in the
// order of Known so the UI's translation switcher is stable.
func Downloaded(db *sql.DB) ([]Translation, error) {
	rows, err := db.Query(`SELECT abbrev, name, lang FROM translations`)
	if err != nil {
		return nil, serr.Wrap(err, "cannot list translations")
	}
	defer rows.Close()

	present := map[string]Translation{}
	for rows.Next() {
		var t Translation
		if err := rows.Scan(&t.Abbrev, &t.Name, &t.Lang); err != nil {
			return nil, serr.Wrap(err, "cannot scan translation row")
		}
		present[t.Abbrev] = t
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate translations")
	}

	out := make([]Translation, 0, len(present))
	for _, k := range Known {
		if t, ok := present[k.Abbrev]; ok {
			out = append(out, t)
		}
	}
	// Anything downloaded outside the Known set still deserves to show up.
	for abbrev, t := range present {
		if !IsKnownTranslation(abbrev) {
			out = append(out, t)
		}
	}
	return out, nil
}

// Chapter returns every verse of one chapter, in verse order.
//
// The WHERE clause is a prefix of the verses primary key
// (translation, book_num, chapter, verse), so bytdb serves this as a single
// bounded key scan that is already in verse order — the ORDER BY is free.
func Chapter(db *sql.DB, translation string, bookNum, chapter int) ([]Verse, error) {
	rows, err := db.Query(
		`SELECT verse, body FROM verses
		  WHERE translation = $1 AND book_num = $2 AND chapter = $3
		  ORDER BY verse`,
		translation, bookNum, chapter)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read chapter",
			"translation", translation, "book", strconv.Itoa(bookNum),
			"chapter", strconv.Itoa(chapter))
	}
	defer rows.Close()

	var out []Verse
	for rows.Next() {
		v := Verse{Translation: translation, BookNum: bookNum, Chapter: chapter}
		if err := rows.Scan(&v.Num, &v.Body); err != nil {
			return nil, serr.Wrap(err, "cannot scan verse row")
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate chapter rows")
	}
	return out, nil
}

// VerseRange returns verses start..end of a chapter (inclusive). An end of 0
// or below start is treated as a single verse.
func VerseRange(db *sql.DB, translation string, bookNum, chapter, start, end int) ([]Verse, error) {
	if end < start {
		end = start
	}
	rows, err := db.Query(
		`SELECT verse, body FROM verses
		  WHERE translation = $1 AND book_num = $2 AND chapter = $3
		    AND verse >= $4 AND verse <= $5
		  ORDER BY verse`,
		translation, bookNum, chapter, start, end)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read verse range",
			"translation", translation, "book", strconv.Itoa(bookNum),
			"chapter", strconv.Itoa(chapter))
	}
	defer rows.Close()

	var out []Verse
	for rows.Next() {
		v := Verse{Translation: translation, BookNum: bookNum, Chapter: chapter}
		if err := rows.Scan(&v.Num, &v.Body); err != nil {
			return nil, serr.Wrap(err, "cannot scan verse row")
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate verse range rows")
	}
	return out, nil
}

// One returns a single verse. A missing verse is reported as an error rather
// than a zero value so handlers can render a proper 404.
func One(db *sql.DB, translation string, bookNum, chapter, verse int) (Verse, error) {
	v := Verse{Translation: translation, BookNum: bookNum, Chapter: chapter, Num: verse}
	err := db.QueryRow(
		`SELECT body FROM verses
		  WHERE translation = $1 AND book_num = $2 AND chapter = $3 AND verse = $4`,
		translation, bookNum, chapter, verse).Scan(&v.Body)
	if err != nil {
		return v, serr.Wrap(err, "verse not found", "translation", translation,
			"book", strconv.Itoa(bookNum), "chapter", strconv.Itoa(chapter),
			"verse", strconv.Itoa(verse))
	}
	return v, nil
}

// AllTranslationsOf returns the same verse across every downloaded
// translation, in Known order — the parallel-translation view of the verse
// hub.
func AllTranslationsOf(db *sql.DB, bookNum, chapter, verse int) ([]Verse, error) {
	rows, err := db.Query(
		`SELECT translation, body FROM verses
		  WHERE book_num = $1 AND chapter = $2 AND verse = $3`,
		bookNum, chapter, verse)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read verse across translations")
	}
	defer rows.Close()

	byAbbrev := map[string]Verse{}
	for rows.Next() {
		v := Verse{BookNum: bookNum, Chapter: chapter, Num: verse}
		if err := rows.Scan(&v.Translation, &v.Body); err != nil {
			return nil, serr.Wrap(err, "cannot scan verse row")
		}
		byAbbrev[v.Translation] = v
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate verse rows")
	}

	out := make([]Verse, 0, len(byAbbrev))
	for _, k := range Known {
		if v, ok := byAbbrev[k.Abbrev]; ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// SearchResult is one hit from a scripture text search.
type SearchResult struct {
	Verse
	// Ref is precomputed for display so templates need no lookup.
	Ref Ref
}

// SearchText finds verses whose body matches q, case-insensitively.
//
// bytdb has no full-text index, so this is an ILIKE scan of the translation's
// ~31k rows. Measured at well under a millisecond on this data size, which is
// why no inverted index was built: it would be pure complexity for a latency
// the user cannot perceive.
//
// limit caps the result set; pass 0 for the default.
func SearchText(db *sql.DB, translation, q string, limit int) ([]SearchResult, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 200
	}

	// Escape the LIKE metacharacters so a literal '%' in the query does not
	// turn into a wildcard.
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)

	rows, err := db.Query(
		`SELECT book_num, chapter, verse, body FROM verses
		  WHERE translation = $1 AND body ILIKE $2
		  ORDER BY book_num, chapter, verse
		  LIMIT $3`,
		translation, "%"+esc+"%", limit)
	if err != nil {
		return nil, serr.Wrap(err, "scripture search failed", "q", q)
	}
	defer rows.Close()

	var out []SearchResult
	for rows.Next() {
		v := Verse{Translation: translation}
		if err := rows.Scan(&v.BookNum, &v.Chapter, &v.Num, &v.Body); err != nil {
			return nil, serr.Wrap(err, "cannot scan search row")
		}
		out = append(out, SearchResult{Verse: v, Ref: v.Ref()})
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate search rows")
	}
	return out, nil
}

// CountVerses returns how many verses are cached for a translation. Used by
// the settings page and by the download command's summary line.
func CountVerses(db *sql.DB, translation string) (int, error) {
	var n int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM verses WHERE translation = $1`, translation).Scan(&n); err != nil {
		return 0, serr.Wrap(err, "cannot count verses", "translation", translation)
	}
	return n, nil
}

// ChapterExists reports whether a chapter has any cached verses — the check
// behind the reader's prev/next links and its 404 path.
func ChapterExists(db *sql.DB, translation string, bookNum, chapter int) bool {
	var n int
	err := db.QueryRow(
		`SELECT COUNT(*) FROM verses
		  WHERE translation = $1 AND book_num = $2 AND chapter = $3`,
		translation, bookNum, chapter).Scan(&n)
	return err == nil && n > 0
}
