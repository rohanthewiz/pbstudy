package store

import (
	"database/sql"
	"strings"

	"github.com/rohanthewiz/serr"
)

// One bytdb limit still shapes this file: there is no `ALTER TABLE ADD PRIMARY
// KEY`. Every table's primary key is declared inline at CREATE time, and there
// is no second chance to add one — which is why the full study schema ships
// here from the start rather than growing a phase at a time.
//
// Idempotency is the DDL's own job. Every statement below carries
// `IF NOT EXISTS`, so bootstrapping an already-populated database is a no-op
// per relation: bytdb checks the name, notices, and leaves existing rows and
// index definitions untouched. The guard covers the name only — it does not
// reconcile a changed column list — so a genuine schema change needs a real
// migration, not an edit here.
//
// This needs bytdb >= v0.9.1, which extended the guard clause from tables to
// `CREATE [UNIQUE] INDEX`; go.mod holds the floor. Earlier versions forced the
// bootstrap to probe `information_schema.tables` and `pg_class` and create only
// the difference.

// bibleSchema is the scripture cache.
//
// verses uses a composite natural primary key rather than a surrogate SERIAL
// with a secondary index over the same four columns. bytdb stores rows in
// primary-key order in one key space and pushes prefix predicates down to
// bounded key scans, so:
//
//	(translation, book_num, chapter, verse)
//	 └── WHERE translation=$1 AND book_num=$2 AND chapter=$3
//	     becomes one ordered range scan, already sorted by verse.
//
// A SERIAL PK would put rows in insert order and need a parallel index
// maintained on every one of the ~31k inserts per translation — twice the
// write work for a strictly worse read path. The natural key is also what
// makes re-downloading a translation idempotent (see bible.Download).
var bibleSchema = []string{
	`CREATE TABLE IF NOT EXISTS translations (
		abbrev TEXT PRIMARY KEY,
		name TEXT,
		lang TEXT,
		downloaded_at TIMESTAMP
	)`,

	`CREATE TABLE IF NOT EXISTS books (
		book_num INT PRIMARY KEY,
		name TEXT,
		osis TEXT,
		blb_abbrev TEXT,
		testament TEXT,
		chapter_count INT
	)`,

	`CREATE TABLE IF NOT EXISTS verses (
		translation TEXT,
		book_num INT,
		chapter INT,
		verse INT,
		body TEXT,
		PRIMARY KEY (translation, book_num, chapter, verse)
	)`,

	// Scripture text search is an ILIKE scan; no index helps it. This index
	// serves the reverse lookup "which translations have this verse", used
	// by the parallel-translation verse hub.
	`CREATE INDEX IF NOT EXISTS idx_verses_ref ON verses (book_num, chapter, verse)`,
}

// studySchema is the user's own data.
//
// Every syncable row carries the same three columns, and they are load-bearing
// for the sync engine rather than decoration:
//
//	id          TEXT   — a UUID, not a sequence. Two machines offline at once
//	                     must be able to mint IDs that never collide.
//	updated_at  TIMESTAMP — the last-writer-wins clock. Compared across hosts.
//	deleted_at  TIMESTAMP — a tombstone. Rows are never physically deleted,
//	                     because a delete that leaves no trace is
//	                     indistinguishable from "the other machine hasn't seen
//	                     this row yet", and would resurrect on next import.
var studySchema = []string{
	// body_md is Markdown. It may embed [[G26]] Strong's shortcodes, which
	// render as Blue Letter Bible lexicon links.
	`CREATE TABLE IF NOT EXISTS notes (
		id TEXT PRIMARY KEY,
		title TEXT,
		body_md TEXT,
		created_at TIMESTAMP,
		updated_at TIMESTAMP,
		deleted_at TIMESTAMP
	)`,

	// note_refs anchors a note to a verse range. These are children of a
	// note, not independent entities: on sync import a note's refs are
	// replaced wholesale, so they carry no tombstone of their own.
	`CREATE TABLE IF NOT EXISTS note_refs (
		id TEXT PRIMARY KEY,
		note_id TEXT,
		book_num INT,
		chapter INT,
		verse_start INT,
		verse_end INT,
		updated_at TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS idx_note_refs_note ON note_refs (note_id)`,
	// Drives the reader's per-verse "has notes" indicator dots: one scan of
	// a chapter's range rather than a lookup per verse.
	`CREATE INDEX IF NOT EXISTS idx_note_refs_loc ON note_refs (book_num, chapter, verse_start)`,

	// Tag identity across machines is the NAME, not the id — two machines
	// that independently create "Grace" must converge on one tag. The
	// unique index enforces that locally; the sync importer matches on name.
	`CREATE TABLE IF NOT EXISTS tags (
		id TEXT PRIMARY KEY,
		name TEXT,
		descrip TEXT,
		updated_at TIMESTAMP,
		deleted_at TIMESTAMP
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_tags_name ON tags (name)`,

	`CREATE TABLE IF NOT EXISTS note_tags (
		id TEXT PRIMARY KEY,
		note_id TEXT,
		tag_id TEXT,
		updated_at TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS idx_note_tags_note ON note_tags (note_id)`,
	`CREATE INDEX IF NOT EXISTS idx_note_tags_tag ON note_tags (tag_id)`,

	// The scripture-correlation feature: "this passage speaks to that one".
	// Indexed in both directions because the verse hub shows references
	// pointing out of a verse AND references pointing into it — a link the
	// user drew from Romans to Genesis must surface while reading Genesis.
	`CREATE TABLE IF NOT EXISTS cross_refs (
		id TEXT PRIMARY KEY,
		from_book INT,
		from_chapter INT,
		from_verse INT,
		to_book INT,
		to_chapter INT,
		to_verse_start INT,
		to_verse_end INT,
		comment TEXT,
		created_at TIMESTAMP,
		updated_at TIMESTAMP,
		deleted_at TIMESTAMP
	)`,
	`CREATE INDEX IF NOT EXISTS idx_xrefs_from ON cross_refs (from_book, from_chapter, from_verse)`,
	`CREATE INDEX IF NOT EXISTS idx_xrefs_to ON cross_refs (to_book, to_chapter, to_verse_start)`,

	// outline is an ordered JSONB array of sections
	// ([{kind: heading|passage|note|point, ...}]) rather than a child table.
	// A sermon outline is only ever read and written as a whole, and its
	// ordering is intrinsic; a child table would buy nothing but joins and
	// a position column to keep consistent.
	`CREATE TABLE IF NOT EXISTS sermons (
		id TEXT PRIMARY KEY,
		title TEXT,
		outline JSONB,
		draft_md TEXT,
		status TEXT,
		created_at TIMESTAMP,
		updated_at TIMESTAMP,
		deleted_at TIMESTAMP
	)`,
}

func bootstrapBible(db *sql.DB) error { return applySchema(db, bibleSchema) }

func bootstrapStudy(db *sql.DB) error { return applySchema(db, studySchema) }

// applySchema runs the schema statements in declaration order, which matters:
// a CREATE INDEX names a table that must already exist, since IF NOT EXISTS
// guards the index name and not its table.
//
// Anything that fails here is a real schema bug — a typo, a bad column type, a
// statement out of order — and stops startup rather than leaving the app to
// discover a missing relation one query at a time.
func applySchema(db *sql.DB, schema []string) error {
	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			return serr.Wrap(err, "cannot apply schema statement", "statement", firstLine(stmt))
		}
	}
	return nil
}

// firstLine trims a multi-line CREATE down to the part that identifies it, so
// a startup failure reads "CREATE TABLE IF NOT EXISTS notes (" rather than
// twelve lines of column definitions. Derived from the statement rather than
// stored alongside it, so it cannot drift out of step with what actually ran.
func firstLine(stmt string) string {
	line, _, _ := strings.Cut(stmt, "\n")
	return strings.TrimSpace(line)
}
