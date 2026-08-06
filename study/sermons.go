package study

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/rohanthewiz/serr"

	"github.com/rohanthewiz/pbstudy/bible"
)

// A sermon is an ordered outline plus an optional draft.
//
// # Why the outline is one JSONB column and not a child table
//
// An outline is only ever read and written whole: every page that shows it
// shows all of it, and every edit (add, remove, reorder) rewrites the order of
// the rest. A child table would buy joins, a position column to keep
// consistent, and a second thing for the sync engine to merge — in exchange for
// query shapes nothing asks for. Nobody searches for "sermons whose third
// section is a heading".
//
// The cost of that choice is that concurrent edits clobber each other, which is
// why every mutation below is a read-modify-write inside a transaction, and why
// sections carry their own ids (see Section.ID).

// Section kinds. These are JSON values persisted in the database, so they are
// stable strings rather than an int enum — a renumbering would silently
// reinterpret every stored outline.
const (
	KindHeading = "heading" // a movement of the sermon: "The problem"
	KindPassage = "passage" // scripture, inlined verbatim at assembly time
	KindNote    = "note"    // one of the user's notes, inlined at assembly time
	KindPoint   = "point"   // a bullet the preacher wants to make
)

// Sermon statuses.
const (
	StatusOutline  = "outline"  // no draft yet
	StatusDrafting = "drafting" // a draft is being generated right now
	StatusDrafted  = "drafted"  // draft_md holds something
)

// Section is one entry in an outline.
//
// One struct with a Kind tag rather than four types with an interface: the
// whole value is round-tripped through JSON, and a discriminated union in Go
// costs a custom UnmarshalJSON for no gain at four variants.
//
// Ref is a value, not a pointer, and is meaningful only when Kind is
// KindPassage; NoteID likewise only for KindNote. omitempty keeps the stored
// JSON to what each kind actually uses.
type Section struct {
	// ID is minted when the section is appended and never changes.
	//
	// Load-bearing for editing: the builder page renders a Move-up button per
	// row, and by the time it is clicked the outline may have been reordered in
	// another tab. An index would move that button's meaning underneath it — the
	// click would reorder whatever now sits at position 3. An id either finds
	// its section wherever it drifted to, or reports that it is gone.
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Text is the heading title or the point's text. Unused by passage and
	// note sections.
	Text string `json:"text,omitempty"`
	// Ref locates a passage section. Tagged omitzero rather than omitempty:
	// omitempty has no effect on a struct value, so a heading's zero Ref would
	// otherwise be written into every stored outline.
	Ref bible.Ref `json:"ref,omitzero"`
	// NoteID points a note section at one of the user's notes.
	NoteID string `json:"noteId,omitempty"`
}

// Sermon is one sermon or teaching under construction.
type Sermon struct {
	ID        string
	Title     string
	Outline   []Section
	DraftMD   string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SectionCount is a display helper so views need no len() in a render tree.
func (s Sermon) SectionCount() int { return len(s.Outline) }

// HasDraft reports whether a draft has been generated and kept.
func (s Sermon) HasDraft() bool { return strings.TrimSpace(s.DraftMD) != "" }

// DefaultSermonLimit caps the sermons list, matching the notes list.
const DefaultSermonLimit = 500

// CreateSermon starts a new sermon with an empty outline and returns its id.
func CreateSermon(db *sql.DB, title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled sermon"
	}

	id := newID()
	now := nowUTC()

	if _, err := db.Exec(
		`INSERT INTO sermons (id, title, outline, draft_md, status, created_at, updated_at, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id, title, emptyOutlineJSON, "", StatusOutline, now, now, nil); err != nil {
		return "", serr.Wrap(err, "cannot create sermon")
	}
	return id, nil
}

// emptyOutlineJSON is what an outline column holds before anything is added.
// A literal "[]" rather than NULL, so every read path decodes the same shape
// and none of them needs a null check.
const emptyOutlineJSON = "[]"

// GetSermon loads one live sermon.
func GetSermon(db *sql.DB, id string) (Sermon, error) {
	var s Sermon
	var outline sql.NullString
	var draft sql.NullString

	err := db.QueryRow(
		`SELECT id, title, outline, draft_md, status, created_at, updated_at
		   FROM sermons WHERE id = $1 AND deleted_at IS NULL`, id).
		Scan(&s.ID, &s.Title, &outline, &draft, &s.Status, &s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return s, serr.Wrap(err, "sermon not found", "sermon", id)
	}

	s.DraftMD = draft.String
	s.Outline, err = decodeOutline(outline)
	if err != nil {
		return s, serr.Wrap(err, "sermon has an unreadable outline", "sermon", id)
	}
	return s, nil
}

// ListSermons returns live sermons, most recently edited first — the same
// recency order the notes list uses, and for the same reason: a study session
// returns to what it was just working on.
func ListSermons(db *sql.DB, limit int) ([]Sermon, error) {
	if limit <= 0 {
		limit = DefaultSermonLimit
	}

	rows, err := db.Query(
		`SELECT id, title, outline, draft_md, status, created_at, updated_at
		   FROM sermons
		  WHERE deleted_at IS NULL
		  ORDER BY updated_at DESC
		  LIMIT $1`, limit)
	if err != nil {
		return nil, serr.Wrap(err, "cannot list sermons")
	}
	defer rows.Close()

	var out []Sermon
	for rows.Next() {
		var s Sermon
		var outline, draft sql.NullString
		if err := rows.Scan(&s.ID, &s.Title, &outline, &draft, &s.Status,
			&s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, serr.Wrap(err, "cannot scan sermon row")
		}
		s.DraftMD = draft.String
		// A single unreadable outline must not blank the index. The row still
		// has a title worth listing and a link worth following; the detail page
		// is where the decode failure is reported.
		s.Outline, _ = decodeOutline(outline)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate sermon rows")
	}
	return out, nil
}

// UpdateSermonTitle renames a sermon.
func UpdateSermonTitle(db *sql.DB, id, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return serr.New("a sermon needs a title")
	}

	res, err := db.Exec(
		`UPDATE sermons SET title = $1, updated_at = $2
		  WHERE id = $3 AND deleted_at IS NULL`, title, nowUTC(), id)
	if err != nil {
		return serr.Wrap(err, "cannot rename sermon", "sermon", id)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return serr.New("no such sermon", "sermon", id)
	}
	return nil
}

// DeleteSermon tombstones a sermon, on the same terms as a note: the row
// survives with deleted_at set so the deletion can propagate during sync.
func DeleteSermon(db *sql.DB, id string) error {
	now := nowUTC()
	res, err := db.Exec(
		`UPDATE sermons SET deleted_at = $1, updated_at = $1
		  WHERE id = $2 AND deleted_at IS NULL`, now, id)
	if err != nil {
		return serr.Wrap(err, "cannot delete sermon", "sermon", id)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return serr.New("no such sermon", "sermon", id)
	}
	return nil
}

// CountSermons returns how many live sermons exist, for the dashboard.
func CountSermons(db *sql.DB) (int, error) {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sermons WHERE deleted_at IS NULL`).
		Scan(&n); err != nil {
		return 0, serr.Wrap(err, "cannot count sermons")
	}
	return n, nil
}

// --- outline editing -------------------------------------------------------

// AppendSection adds a section to the end of an outline and returns its id.
func AppendSection(db *sql.DB, sermonID string, sec Section) (string, error) {
	if sec.ID == "" {
		sec.ID = newID()
	}
	sec.Text = strings.TrimSpace(sec.Text)

	err := mutateOutline(db, sermonID, func(sections []Section) ([]Section, error) {
		return append(sections, sec), nil
	})
	if err != nil {
		return "", err
	}
	return sec.ID, nil
}

// RemoveSection drops a section by id.
//
// A section that is already gone is reported rather than ignored: the click
// came from a page that no longer matches the outline, and saying so is more
// useful than a silent no-op that looks like the button is broken.
func RemoveSection(db *sql.DB, sermonID, sectionID string) error {
	return mutateOutline(db, sermonID, func(sections []Section) ([]Section, error) {
		i := indexOfSection(sections, sectionID)
		if i < 0 {
			return nil, serr.New("that section is no longer in the outline",
				"sermon", sermonID, "section", sectionID)
		}
		return append(sections[:i:i], sections[i+1:]...), nil
	})
}

// MoveSection shifts a section one place up (delta -1) or down (delta +1).
//
// Moving off either end is a no-op rather than an error — the button is
// rendered on every row, and a user who clicks "up" on the first row has made
// no mistake worth an error page.
func MoveSection(db *sql.DB, sermonID, sectionID string, delta int) error {
	return mutateOutline(db, sermonID, func(sections []Section) ([]Section, error) {
		i := indexOfSection(sections, sectionID)
		if i < 0 {
			return nil, serr.New("that section is no longer in the outline",
				"sermon", sermonID, "section", sectionID)
		}
		j := i + delta
		if j < 0 || j >= len(sections) {
			return sections, nil
		}
		sections[i], sections[j] = sections[j], sections[i]
		return sections, nil
	})
}

// SetDraft stores generated draft text and moves the sermon to the matching
// status.
func SetDraft(db *sql.DB, id, draftMD, status string) error {
	res, err := db.Exec(
		`UPDATE sermons SET draft_md = $1, status = $2, updated_at = $3
		  WHERE id = $4 AND deleted_at IS NULL`, draftMD, status, nowUTC(), id)
	if err != nil {
		return serr.Wrap(err, "cannot save sermon draft", "sermon", id)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return serr.New("no such sermon", "sermon", id)
	}
	return nil
}

// SetStatus moves a sermon between statuses without touching its draft.
func SetStatus(db *sql.DB, id, status string) error {
	res, err := db.Exec(
		`UPDATE sermons SET status = $1, updated_at = $2
		  WHERE id = $3 AND deleted_at IS NULL`, status, nowUTC(), id)
	if err != nil {
		return serr.Wrap(err, "cannot set sermon status", "sermon", id)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return serr.New("no such sermon", "sermon", id)
	}
	return nil
}

// --- internals -------------------------------------------------------------

// mutateOutline applies edit to a sermon's outline inside one transaction.
//
// Read-modify-write is unavoidable for a whole-column outline, so it is done
// under a transaction rather than as two statements: two browser tabs open on
// the same sermon must not be able to interleave a read and a write and lose an
// edit. The section ids handled by the callers close the other half of that
// problem — a stale page targets a section, not a position.
func mutateOutline(db *sql.DB, sermonID string, edit func([]Section) ([]Section, error)) error {
	tx, err := db.Begin()
	if err != nil {
		return serr.Wrap(err, "cannot begin transaction")
	}
	defer func() { _ = tx.Rollback() }()

	var raw sql.NullString
	if err := tx.QueryRow(
		`SELECT outline FROM sermons WHERE id = $1 AND deleted_at IS NULL`, sermonID).
		Scan(&raw); err != nil {
		return serr.Wrap(err, "sermon not found", "sermon", sermonID)
	}

	sections, err := decodeOutline(raw)
	if err != nil {
		return serr.Wrap(err, "sermon has an unreadable outline", "sermon", sermonID)
	}

	sections, err = edit(sections)
	if err != nil {
		return err
	}

	encoded, err := encodeOutline(sections)
	if err != nil {
		return err
	}

	if _, err := tx.Exec(
		`UPDATE sermons SET outline = $1, updated_at = $2 WHERE id = $3`,
		encoded, nowUTC(), sermonID); err != nil {
		return serr.Wrap(err, "cannot save outline", "sermon", sermonID)
	}

	if err := tx.Commit(); err != nil {
		return serr.Wrap(err, "cannot commit outline change")
	}
	return nil
}

func indexOfSection(sections []Section, id string) int {
	for i, s := range sections {
		if s.ID == id {
			return i
		}
	}
	return -1
}

// decodeOutline turns the stored column into sections.
//
// A NULL or empty column is an empty outline, not an error: rows written before
// this feature existed, or by a future sync import that omitted the field, are
// legitimately outline-less and should open in the builder rather than 500.
func decodeOutline(raw sql.NullString) ([]Section, error) {
	text := strings.TrimSpace(raw.String)
	if !raw.Valid || text == "" || text == "null" {
		return nil, nil
	}

	var sections []Section
	if err := json.Unmarshal([]byte(text), &sections); err != nil {
		return nil, serr.Wrap(err, "cannot decode outline")
	}

	// Repair rather than reject: an outline that arrived from an older version
	// (or a hand-edited sync file) may carry sections with no id. Minting one
	// here keeps the edit buttons working instead of leaving a row that cannot
	// be moved or removed.
	for i := range sections {
		if sections[i].ID == "" {
			sections[i].ID = newID()
		}
	}
	return sections, nil
}

func encodeOutline(sections []Section) (string, error) {
	if len(sections) == 0 {
		return emptyOutlineJSON, nil
	}
	b, err := json.Marshal(sections)
	if err != nil {
		return "", serr.Wrap(err, "cannot encode outline")
	}
	return string(b), nil
}
