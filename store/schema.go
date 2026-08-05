package store

import (
	"database/sql"
	"strings"

	"github.com/rohanthewiz/serr"
)

// bytdb has no `CREATE TABLE IF NOT EXISTS` and no `ALTER TABLE ADD PRIMARY
// KEY`, which shapes this whole file:
//
//   - Every table's primary key is declared inline at CREATE time. There is no
//     second chance to add one.
//   - Idempotency is achieved by asking the catalog what already exists and
//     creating only the difference, rather than by a swallow-the-error retry.
//     Probing keeps genuine DDL errors (a typo, a bad type) loud instead of
//     silently classifying them as "already there".
//
// The catalog queries below are the two bytdb actually serves:
// information_schema.tables for relations, pg_class for indexes.

// ddl pairs a relation name with the statement that creates it, so the
// bootstrap can skip the ones already present.
type ddl struct {
	name string // relation name as it appears in the catalog
	stmt string
}

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
var bibleSchema = []ddl{
	{"translations", `CREATE TABLE translations (
		abbrev TEXT PRIMARY KEY,
		name TEXT,
		lang TEXT,
		downloaded_at TIMESTAMP
	)`},

	{"books", `CREATE TABLE books (
		book_num INT PRIMARY KEY,
		name TEXT,
		osis TEXT,
		blb_abbrev TEXT,
		testament TEXT,
		chapter_count INT
	)`},

	{"verses", `CREATE TABLE verses (
		translation TEXT,
		book_num INT,
		chapter INT,
		verse INT,
		body TEXT,
		PRIMARY KEY (translation, book_num, chapter, verse)
	)`},

	// Scripture text search is an ILIKE scan; no index helps it. This index
	// serves the reverse lookup "which translations have this verse", used
	// by the parallel-translation verse hub.
	{"idx_verses_ref", `CREATE INDEX idx_verses_ref ON verses (book_num, chapter, verse)`},
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
var studySchema = []ddl{
	// body_md is Markdown. It may embed [[G26]] Strong's shortcodes, which
	// render as Blue Letter Bible lexicon links.
	{"notes", `CREATE TABLE notes (
		id TEXT PRIMARY KEY,
		title TEXT,
		body_md TEXT,
		created_at TIMESTAMP,
		updated_at TIMESTAMP,
		deleted_at TIMESTAMP
	)`},

	// note_refs anchors a note to a verse range. These are children of a
	// note, not independent entities: on sync import a note's refs are
	// replaced wholesale, so they carry no tombstone of their own.
	{"note_refs", `CREATE TABLE note_refs (
		id TEXT PRIMARY KEY,
		note_id TEXT,
		book_num INT,
		chapter INT,
		verse_start INT,
		verse_end INT,
		updated_at TIMESTAMP
	)`},
	{"idx_note_refs_note", `CREATE INDEX idx_note_refs_note ON note_refs (note_id)`},
	// Drives the reader's per-verse "has notes" indicator dots: one scan of
	// a chapter's range rather than a lookup per verse.
	{"idx_note_refs_loc", `CREATE INDEX idx_note_refs_loc ON note_refs (book_num, chapter, verse_start)`},

	// Tag identity across machines is the NAME, not the id — two machines
	// that independently create "Grace" must converge on one tag. The
	// unique index enforces that locally; the sync importer matches on name.
	{"tags", `CREATE TABLE tags (
		id TEXT PRIMARY KEY,
		name TEXT,
		descrip TEXT,
		updated_at TIMESTAMP,
		deleted_at TIMESTAMP
	)`},
	{"idx_tags_name", `CREATE UNIQUE INDEX idx_tags_name ON tags (name)`},

	{"note_tags", `CREATE TABLE note_tags (
		id TEXT PRIMARY KEY,
		note_id TEXT,
		tag_id TEXT,
		updated_at TIMESTAMP
	)`},
	{"idx_note_tags_note", `CREATE INDEX idx_note_tags_note ON note_tags (note_id)`},
	{"idx_note_tags_tag", `CREATE INDEX idx_note_tags_tag ON note_tags (tag_id)`},

	// The scripture-correlation feature: "this passage speaks to that one".
	// Indexed in both directions because the verse hub shows references
	// pointing out of a verse AND references pointing into it — a link the
	// user drew from Romans to Genesis must surface while reading Genesis.
	{"cross_refs", `CREATE TABLE cross_refs (
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
	)`},
	{"idx_xrefs_from", `CREATE INDEX idx_xrefs_from ON cross_refs (from_book, from_chapter, from_verse)`},
	{"idx_xrefs_to", `CREATE INDEX idx_xrefs_to ON cross_refs (to_book, to_chapter, to_verse_start)`},

	// outline is an ordered JSONB array of sections
	// ([{kind: heading|passage|note|point, ...}]) rather than a child table.
	// A sermon outline is only ever read and written as a whole, and its
	// ordering is intrinsic; a child table would buy nothing but joins and
	// a position column to keep consistent.
	{"sermons", `CREATE TABLE sermons (
		id TEXT PRIMARY KEY,
		title TEXT,
		outline JSONB,
		draft_md TEXT,
		status TEXT,
		created_at TIMESTAMP,
		updated_at TIMESTAMP,
		deleted_at TIMESTAMP
	)`},
}

func bootstrapBible(db *sql.DB) error { return applySchema(db, bibleSchema) }

func bootstrapStudy(db *sql.DB) error { return applySchema(db, studySchema) }

// applySchema creates whichever relations in schema are not already present.
// Order matters: a CREATE INDEX must follow its table, so the slice is walked
// in declaration order.
func applySchema(db *sql.DB, schema []ddl) error {
	existing, err := existingRelations(db)
	if err != nil {
		return err
	}

	for _, d := range schema {
		if existing[d.name] {
			continue
		}
		if _, err := db.Exec(d.stmt); err != nil {
			// Tolerate the race/edge case where the catalog probe and the
			// create disagree (e.g. a relation kind the probe doesn't
			// list); anything else is a real schema bug.
			if isAlreadyExists(err) {
				continue
			}
			return serr.Wrap(err, "cannot create relation", "relation", d.name)
		}
	}
	return nil
}

// existingRelations returns the set of table and index names already in the
// database. Tables come from information_schema.tables; indexes are not listed
// there, so pg_class supplies them (it lists tables, their implicit *_pkey
// entries, and secondary indexes alike).
func existingRelations(db *sql.DB) (map[string]bool, error) {
	found := make(map[string]bool)

	for _, q := range []string{
		`SELECT table_name FROM information_schema.tables`,
		`SELECT relname FROM pg_class`,
	} {
		rows, err := db.Query(q)
		if err != nil {
			return nil, serr.Wrap(err, "cannot read catalog", "query", q)
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				_ = rows.Close()
				return nil, serr.Wrap(err, "cannot scan catalog row")
			}
			found[name] = true
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, serr.Wrap(err, "cannot iterate catalog", "query", q)
		}
		_ = rows.Close()
	}

	return found, nil
}

// isAlreadyExists matches bytdb's duplicate-relation errors ("table already
// exists", "index already exists", `relation "x" already exists`). bytdb does
// not expose these as sentinel values, so string matching is the only option;
// it is kept narrow and used only as a backstop to the catalog probe.
func isAlreadyExists(err error) bool {
	return strings.Contains(err.Error(), "already exists")
}
