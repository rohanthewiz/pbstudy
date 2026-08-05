package bible

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/rohanthewiz/serr"
)

// getbible.net v2 serves one JSON document per book:
//
//	https://api.getbible.net/v2/kjv/43.json  -> the Gospel of John
//
// Per-book rather than whole-Bible: 66 requests of ~200KB each instead of one
// 5MB blob, which keeps memory flat and makes a failed download resumable at
// book granularity.
const getBibleBaseURL = "https://api.getbible.net/v2"

// interRequestDelay paces the downloader. getbible.net is a free service run
// as a courtesy; 66 requests at 10/sec is polite and still finishes a whole
// translation in well under a minute.
const interRequestDelay = 100 * time.Millisecond

// downloadTimeout bounds a single book fetch.
const downloadTimeout = 30 * time.Second

// bookDoc mirrors the getbible.net v2 per-book response. Only the fields we
// consume are declared; the API also returns direction/encoding/language,
// which we ignore.
//
// Note that chapters and verses are JSON ARRAYS, not objects keyed by number
// — a v1-vs-v2 difference that silently yields zero verses if modelled wrong.
type bookDoc struct {
	Translation  string `json:"translation"`
	Abbreviation string `json:"abbreviation"`
	Lang         string `json:"lang"`
	Language     string `json:"language"`
	Nr           int    `json:"nr"`
	Name         string `json:"name"`
	Chapters     []struct {
		Chapter int `json:"chapter"`
		Verses  []struct {
			Chapter int    `json:"chapter"`
			Verse   int    `json:"verse"`
			Text    string `json:"text"`
		} `json:"verses"`
	} `json:"chapters"`
}

// Progress reports download advancement to the caller (the CLI prints it).
type Progress func(bookNum int, bookName string, verses int)

// Download fetches a whole translation into the cache.
//
// Idempotent by construction: each book's existing rows are deleted and
// rewritten inside one transaction, so re-running after a partial failure —
// or to pick up an upstream text correction — converges rather than
// duplicating or half-updating. The verses primary key
// (translation, book_num, chapter, verse) is what makes that delete a single
// bounded range operation.
func Download(db *sql.DB, abbrev string, progress Progress) (int, error) {
	if !IsKnownTranslation(abbrev) {
		return 0, serr.New("unknown translation", "translation", abbrev,
			"known", "kjv, web, asv")
	}

	if err := SeedBooks(db); err != nil {
		return 0, err
	}

	client := &http.Client{Timeout: downloadTimeout}
	total := 0
	var meta bookDoc

	for i := range Books {
		bk := &Books[i]

		// Pace ourselves between books, but not before the first one.
		if i > 0 {
			time.Sleep(interRequestDelay)
		}

		doc, err := fetchBook(client, abbrev, bk.Num)
		if err != nil {
			return total, serr.Wrap(err, "download failed",
				"translation", abbrev, "book", bk.Name)
		}
		if meta.Abbreviation == "" {
			meta = *doc // first book carries the translation metadata
		}

		n, err := storeBook(db, abbrev, bk.Num, doc)
		if err != nil {
			return total, serr.Wrap(err, "cannot store book",
				"translation", abbrev, "book", bk.Name)
		}
		total += n

		if progress != nil {
			progress(bk.Num, bk.Name, n)
		}
	}

	if err := recordTranslation(db, abbrev, &meta); err != nil {
		return total, err
	}
	return total, nil
}

// fetchBook retrieves and decodes one book document.
func fetchBook(client *http.Client, abbrev string, bookNum int) (*bookDoc, error) {
	url := fmt.Sprintf("%s/%s/%d.json", getBibleBaseURL, abbrev, bookNum)

	resp, err := client.Get(url)
	if err != nil {
		return nil, serr.Wrap(err, "request failed", "url", url)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read a little of the body to make the error actionable without
		// dumping a whole error page into the log.
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, serr.New("unexpected status from getbible.net",
			"url", url, "status", resp.Status, "body", string(snippet))
	}

	var doc bookDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, serr.Wrap(err, "cannot decode book json", "url", url)
	}
	if len(doc.Chapters) == 0 {
		return nil, serr.New("book document has no chapters", "url", url)
	}
	return &doc, nil
}

// storeBook replaces one book's verses for one translation, atomically.
func storeBook(db *sql.DB, abbrev string, bookNum int, doc *bookDoc) (int, error) {
	tx, err := db.Begin()
	if err != nil {
		return 0, serr.Wrap(err, "cannot begin transaction")
	}
	// Rollback on any early return; a no-op once Commit has succeeded.
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(
		`DELETE FROM verses WHERE translation = $1 AND book_num = $2`,
		abbrev, bookNum); err != nil {
		return 0, serr.Wrap(err, "cannot clear existing verses")
	}

	stmt, err := tx.Prepare(
		`INSERT INTO verses (translation, book_num, chapter, verse, body)
		 VALUES ($1, $2, $3, $4, $5)`)
	if err != nil {
		return 0, serr.Wrap(err, "cannot prepare verse insert")
	}
	defer stmt.Close()

	n := 0
	for _, ch := range doc.Chapters {
		for _, v := range ch.Verses {
			// Prefer the chapter number on the chapter object; the verse
			// objects repeat it, but the chapter is the authority.
			if _, err := stmt.Exec(abbrev, bookNum, ch.Chapter, v.Verse, v.Text); err != nil {
				return 0, serr.Wrap(err, "cannot insert verse",
					"chapter", strconv.Itoa(ch.Chapter), "verse", strconv.Itoa(v.Verse))
			}
			n++
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, serr.Wrap(err, "cannot commit book")
	}
	return n, nil
}

// recordTranslation writes the translations row, replacing any prior entry so
// downloaded_at reflects the most recent successful download.
func recordTranslation(db *sql.DB, abbrev string, meta *bookDoc) error {
	name := meta.Translation
	if name == "" {
		// Fall back to our own table if the API omitted the field.
		for _, k := range Known {
			if k.Abbrev == abbrev {
				name = k.Name
			}
		}
	}
	lang := meta.Language
	if lang == "" {
		lang = meta.Lang
	}

	tx, err := db.Begin()
	if err != nil {
		return serr.Wrap(err, "cannot begin transaction")
	}
	defer func() { _ = tx.Rollback() }()

	// bytdb supports ON CONFLICT, but delete-then-insert expresses "replace
	// this row" with no dependence on upsert semantics, and the table has
	// at most three rows.
	if _, err := tx.Exec(`DELETE FROM translations WHERE abbrev = $1`, abbrev); err != nil {
		return serr.Wrap(err, "cannot clear translation row")
	}
	if _, err := tx.Exec(
		`INSERT INTO translations (abbrev, name, lang, downloaded_at) VALUES ($1, $2, $3, $4)`,
		abbrev, name, lang, time.Now().UTC()); err != nil {
		return serr.Wrap(err, "cannot record translation")
	}
	// serr.Wrap complains about a nil error, so commits are checked rather
	// than wrapped unconditionally.
	if err := tx.Commit(); err != nil {
		return serr.Wrap(err, "cannot commit translation row")
	}
	return nil
}

// SeedBooks writes the canon table if it is not already populated.
//
// The data is compiled into the binary (see Books), so this is a convenience
// for SQL-level joins and for anything reading the database directly — not a
// source of truth the Go code depends on.
func SeedBooks(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM books`).Scan(&n); err != nil {
		return serr.Wrap(err, "cannot count books")
	}
	if n == len(Books) {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return serr.Wrap(err, "cannot begin transaction")
	}
	defer func() { _ = tx.Rollback() }()

	// Rewrite wholesale rather than reconciling: 66 rows, and a partial
	// table from an interrupted earlier run should not survive.
	if _, err := tx.Exec(`DELETE FROM books`); err != nil {
		return serr.Wrap(err, "cannot clear books")
	}
	stmt, err := tx.Prepare(
		`INSERT INTO books (book_num, name, osis, blb_abbrev, testament, chapter_count)
		 VALUES ($1, $2, $3, $4, $5, $6)`)
	if err != nil {
		return serr.Wrap(err, "cannot prepare book insert")
	}
	defer stmt.Close()

	for i := range Books {
		bk := &Books[i]
		if _, err := stmt.Exec(bk.Num, bk.Name, bk.OSIS, bk.BLBAbbrev,
			bk.Testament, bk.ChapterCount); err != nil {
			return serr.Wrap(err, "cannot insert book", "book", bk.Name)
		}
	}
	if err := tx.Commit(); err != nil {
		return serr.Wrap(err, "cannot commit books")
	}
	return nil
}

// VerifyChapterCounts cross-checks the compiled chapter counts against what
// was actually downloaded. A mismatch means either the Books table has a typo
// or the download was truncated — both worth surfacing, neither worth
// aborting a download over.
func VerifyChapterCounts(db *sql.DB, translation string) []string {
	var problems []string
	for i := range Books {
		bk := &Books[i]
		var got int
		err := db.QueryRow(
			`SELECT COUNT(DISTINCT chapter) FROM verses WHERE translation = $1 AND book_num = $2`,
			translation, bk.Num).Scan(&got)
		if err != nil {
			problems = append(problems, bk.Name+": "+err.Error())
			continue
		}
		if got != bk.ChapterCount {
			problems = append(problems, bk.Name+": table says "+
				strconv.Itoa(bk.ChapterCount)+" chapters, downloaded "+strconv.Itoa(got))
		}
	}
	return problems
}
