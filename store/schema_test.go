package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "github.com/rohanthewiz/bytdb/stdlib"
)

// The bootstrap runs on every single start, against a database that already
// holds the user's notes far more often than against an empty one. Its whole
// contract is therefore "changes nothing that is already there", and the
// interesting cases are the second run and the run against a database some
// earlier version of this program created.
//
// These tests exist because that contract moved: idempotency used to come from
// probing the catalog and creating only the difference, and now comes from
// IF NOT EXISTS on the statements themselves.

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("bytdb", filepath.Join(t.TempDir(), "s.bytdb"))
	if err != nil {
		t.Fatalf("sql.Open() error: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestBootstrapIsIdempotent is the property the whole file rests on: starting
// the app twice must not be different from starting it once.
func TestBootstrapIsIdempotent(t *testing.T) {
	for _, tc := range []struct {
		name string
		boot func(*sql.DB) error
	}{
		{"bible", bootstrapBible},
		{"study", bootstrapStudy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			for run := 1; run <= 3; run++ {
				if err := tc.boot(db); err != nil {
					t.Fatalf("bootstrap run %d error: %v", run, err)
				}
			}
		})
	}
}

// TestBootstrapPreservesData is the failure this guards against: a bootstrap
// that re-created a table would silently empty someone's study database on the
// next start. IF NOT EXISTS checks the name and skips, so the rows stay.
func TestBootstrapPreservesData(t *testing.T) {
	db := openTestDB(t)
	if err := bootstrapStudy(db); err != nil {
		t.Fatalf("first bootstrap error: %v", err)
	}

	if _, err := db.Exec(
		`INSERT INTO notes (id, title, body_md) VALUES ('n1', 'Kept', 'Body')`); err != nil {
		t.Fatalf("seed note error: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tags (id, name) VALUES ('t1', 'Grace')`); err != nil {
		t.Fatalf("seed tag error: %v", err)
	}

	if err := bootstrapStudy(db); err != nil {
		t.Fatalf("second bootstrap error: %v", err)
	}

	var title string
	if err := db.QueryRow(`SELECT title FROM notes WHERE id = 'n1'`).Scan(&title); err != nil {
		t.Fatalf("note did not survive the second bootstrap: %v", err)
	}
	if title != "Kept" {
		t.Errorf("note title = %q, want %q", title, "Kept")
	}

	// The unique index has to survive as an index, not just as a name: a
	// re-created-but-unenforced idx_tags_name would let "Grace" split into
	// two rows and quietly break tag convergence across machines.
	if _, err := db.Exec(`INSERT INTO tags (id, name) VALUES ('t2', 'Grace')`); err == nil {
		t.Error("duplicate tag name was accepted; the unique index on tags.name is not being enforced after re-bootstrap")
	}
}

// TestBootstrapOverLegacyDatabase covers the upgrade path. Databases in the
// wild were created by the previous bootstrap, which issued the same DDL
// without the guard clause. Starting the new binary on one of those must be a
// no-op, not an "already exists" failure.
func TestBootstrapOverLegacyDatabase(t *testing.T) {
	db := openTestDB(t)

	// The pre-guard statements, verbatim in shape: same relations, no
	// IF NOT EXISTS.
	for _, stmt := range []string{
		`CREATE TABLE notes (
			id TEXT PRIMARY KEY, title TEXT, body_md TEXT,
			created_at TIMESTAMP, updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
		`CREATE TABLE note_refs (
			id TEXT PRIMARY KEY, note_id TEXT, book_num INT, chapter INT,
			verse_start INT, verse_end INT, updated_at TIMESTAMP)`,
		`CREATE INDEX idx_note_refs_note ON note_refs (note_id)`,
		`CREATE TABLE tags (
			id TEXT PRIMARY KEY, name TEXT, descrip TEXT,
			updated_at TIMESTAMP, deleted_at TIMESTAMP)`,
		`CREATE UNIQUE INDEX idx_tags_name ON tags (name)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("legacy schema setup error: %v\n%s", err, stmt)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO notes (id, title, body_md) VALUES ('old', 'Written before the upgrade', 'Body')`); err != nil {
		t.Fatalf("seed legacy note error: %v", err)
	}

	// The new bootstrap must complete the schema (sermons, cross_refs and
	// the remaining indexes were never created above) without tripping over
	// the relations that are already there.
	if err := bootstrapStudy(db); err != nil {
		t.Fatalf("bootstrap over a legacy database error: %v", err)
	}

	var title string
	if err := db.QueryRow(`SELECT title FROM notes WHERE id = 'old'`).Scan(&title); err != nil {
		t.Fatalf("pre-upgrade note was lost: %v", err)
	}
	if title != "Written before the upgrade" {
		t.Errorf("note title = %q, want it unchanged", title)
	}

	// The relations the legacy database lacked must now exist and be usable.
	if _, err := db.Exec(
		`INSERT INTO sermons (id, title, status) VALUES ('s1', 'New table', 'outline')`); err != nil {
		t.Errorf("sermons was not created by the bootstrap: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO cross_refs (id, from_book, from_chapter, from_verse) VALUES ('x1', 45, 5, 8)`); err != nil {
		t.Errorf("cross_refs was not created by the bootstrap: %v", err)
	}
}

// TestBootstrapCreatesEveryRelation checks the schema is actually complete
// rather than merely error-free — a statement dropped from the slice would
// otherwise surface as a failed query much later, in whichever page needed it.
func TestBootstrapCreatesEveryRelation(t *testing.T) {
	bibleDB := openTestDB(t)
	if err := bootstrapBible(bibleDB); err != nil {
		t.Fatalf("bootstrapBible() error: %v", err)
	}
	studyDB := openTestDB(t)
	if err := bootstrapStudy(studyDB); err != nil {
		t.Fatalf("bootstrapStudy() error: %v", err)
	}

	for _, tc := range []struct {
		db    *sql.DB
		table string
	}{
		{bibleDB, "translations"},
		{bibleDB, "books"},
		{bibleDB, "verses"},
		{studyDB, "notes"},
		{studyDB, "note_refs"},
		{studyDB, "tags"},
		{studyDB, "note_tags"},
		{studyDB, "cross_refs"},
		{studyDB, "sermons"},
	} {
		var n int
		if err := tc.db.QueryRow(`SELECT COUNT(*) FROM ` + tc.table).Scan(&n); err != nil {
			t.Errorf("table %q is not queryable after bootstrap: %v", tc.table, err)
		}
	}
}

// TestFirstLine pins the error-context helper, which is the only thing naming
// the offending statement when startup fails.
func TestFirstLine(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"CREATE TABLE IF NOT EXISTS notes (\n\tid TEXT PRIMARY KEY\n)", "CREATE TABLE IF NOT EXISTS notes ("},
		{"CREATE INDEX IF NOT EXISTS idx ON t (c)", "CREATE INDEX IF NOT EXISTS idx ON t (c)"},
		{"", ""},
	} {
		if got := firstLine(tc.in); got != tc.want {
			t.Errorf("firstLine(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestApplySchemaReportsRealErrors makes sure the guard clauses did not turn
// the bootstrap into something that swallows genuine schema bugs.
func TestApplySchemaReportsRealErrors(t *testing.T) {
	db := openTestDB(t)

	err := applySchema(db, []string{`CREATE TABLE IF NOT EXISTS broken (id NOSUCHTYPE)`})
	if err == nil {
		t.Fatal("applySchema() accepted a statement with an unknown column type; a real schema bug must stop startup")
	}

	// An index whose table was never created is the ordering mistake most
	// likely to be introduced by editing the slice; IF NOT EXISTS guards the
	// index name, not its table, so this has to stay an error.
	if err := applySchema(db, []string{
		`CREATE INDEX IF NOT EXISTS idx_orphan ON nosuchtable (col)`,
	}); err == nil {
		t.Error("applySchema() accepted an index on a missing table")
	}
}
