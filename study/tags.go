package study

import (
	"database/sql"
	"strings"
	"time"

	"github.com/rohanthewiz/serr"
)

// Tag is a topic label. Tags are what turn a pile of notes into a topical
// study: /tags/:id collects every note carrying the tag, whatever passage each
// one is anchored to.
type Tag struct {
	ID        string
	Name      string
	Descrip   string
	UpdatedAt time.Time
	// NoteCount is populated by ListTags only. It is a display figure, not
	// part of the row, so nothing writes it back.
	NoteCount int
}

// ListTags returns live tags in name order, each with a count of the live
// notes carrying it.
//
// The count comes from a second query rather than a LEFT JOIN with GROUP BY:
// tags with no notes still have to appear (a tag you just created and have not
// used yet), and an outer join is a bigger ask of bytdb than two small scans.
func ListTags(db *sql.DB) ([]Tag, error) {
	rows, err := db.Query(
		`SELECT id, name, descrip, updated_at FROM tags
		  WHERE deleted_at IS NULL
		  ORDER BY name`)
	if err != nil {
		return nil, serr.Wrap(err, "cannot list tags")
	}
	tags, err := scanTags(rows)
	if err != nil {
		return nil, err
	}
	if len(tags) == 0 {
		return tags, nil
	}

	counts, err := tagNoteCounts(db)
	if err != nil {
		return nil, err
	}
	for i := range tags {
		tags[i].NoteCount = counts[tags[i].ID]
	}
	return tags, nil
}

// GetTag loads one live tag.
func GetTag(db *sql.DB, id string) (Tag, error) {
	var t Tag
	err := db.QueryRow(
		`SELECT id, name, descrip, updated_at FROM tags
		  WHERE id = $1 AND deleted_at IS NULL`, id).
		Scan(&t.ID, &t.Name, &t.Descrip, &t.UpdatedAt)
	if err != nil {
		return t, serr.Wrap(err, "tag not found", "tag", id)
	}
	return t, nil
}

// UpdateTagDescrip sets a tag's description — the one field of a tag the user
// can edit after creation. The name is not editable here on purpose: tag names
// are the cross-machine identity used by the sync importer, so renaming one is
// a merge operation, not a field edit, and belongs with the sync engine.
func UpdateTagDescrip(db *sql.DB, id, descrip string) error {
	res, err := db.Exec(
		`UPDATE tags SET descrip = $1, updated_at = $2 WHERE id = $3 AND deleted_at IS NULL`,
		strings.TrimSpace(descrip), nowUTC(), id)
	if err != nil {
		return serr.Wrap(err, "cannot update tag", "tag", id)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return serr.New("no such tag", "tag", id)
	}
	return nil
}

// DeleteTag tombstones a tag and unlinks it from every note.
//
// The links go for real while the tag itself only gets a tombstone, and the
// asymmetry is deliberate: note_tags rows have no identity to preserve, but
// the tag's tombstone is what tells the other machine the tag was retired
// rather than never seen.
func DeleteTag(db *sql.DB, id string) error {
	tx, err := db.Begin()
	if err != nil {
		return serr.Wrap(err, "cannot begin transaction")
	}
	defer func() { _ = tx.Rollback() }()

	now := nowUTC()
	res, err := tx.Exec(
		`UPDATE tags SET deleted_at = $1, updated_at = $1 WHERE id = $2 AND deleted_at IS NULL`,
		now, id)
	if err != nil {
		return serr.Wrap(err, "cannot delete tag", "tag", id)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return serr.New("no such tag", "tag", id)
	}

	if _, err := tx.Exec(`DELETE FROM note_tags WHERE tag_id = $1`, id); err != nil {
		return serr.Wrap(err, "cannot unlink tag", "tag", id)
	}

	if err := tx.Commit(); err != nil {
		return serr.Wrap(err, "cannot commit tag delete")
	}
	return nil
}

// NotesForTag returns the live notes carrying a tag, most recently edited
// first, with their own refs and tags attached.
func NotesForTag(db *sql.DB, tagID string) ([]Note, error) {
	rows, err := db.Query(
		`SELECT DISTINCT n.id, n.title, n.body_md, n.created_at, n.updated_at
		   FROM note_tags nt INNER JOIN notes n ON n.id = nt.note_id
		  WHERE nt.tag_id = $1 AND n.deleted_at IS NULL
		  ORDER BY n.updated_at DESC`, tagID)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read notes for tag", "tag", tagID)
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

// --- internals -------------------------------------------------------------

// writeTagLinks attaches a note to the named tags, creating any that do not
// exist yet.
//
// Tags are created on demand rather than picked from a managed list. Typing
// "Grace, Covenant" into the note editor is the entire tag-management UI, and
// that is intentional: a study tool that makes you visit a settings page
// before you can label a thought will not get used.
func writeTagLinks(tx *sql.Tx, noteID string, names []string, now time.Time) error {
	names = normalizeTagNames(names)
	if len(names) == 0 {
		return nil
	}

	existing, err := loadTagsForMatching(tx)
	if err != nil {
		return err
	}

	for _, name := range names {
		tagID, err := ensureTag(tx, existing, name, now)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO note_tags (id, note_id, tag_id, updated_at) VALUES ($1, $2, $3, $4)`,
			newID(), noteID, tagID, now); err != nil {
			return serr.Wrap(err, "cannot link tag to note", "tag", name)
		}
	}
	return nil
}

// ensureTag returns the id of the tag with this name, creating or reviving the
// row as needed. existing is the case-folded index built by
// loadTagsForMatching and is updated in place, so two new tags in one save
// cannot both try to insert the same name.
func ensureTag(tx *sql.Tx, existing map[string]string, name string, now time.Time) (string, error) {
	key := strings.ToLower(name)

	if id, ok := existing[key]; ok {
		// The row may be a tombstone from an earlier delete. Reviving it
		// keeps the id stable across the delete, which matters when the other
		// machine still has notes linked to it.
		//
		// The cleared tombstone travels as a bound nil rather than a literal
		// NULL in the statement text — one less piece of SQL dialect to rely
		// on, and it reads the same as the nil in the INSERT below.
		if _, err := tx.Exec(
			`UPDATE tags SET deleted_at = $1, updated_at = $2
			  WHERE id = $3 AND deleted_at IS NOT NULL`, nil, now, id); err != nil {
			return "", serr.Wrap(err, "cannot revive tag", "tag", name)
		}
		return id, nil
	}

	id := newID()
	if _, err := tx.Exec(
		`INSERT INTO tags (id, name, descrip, updated_at, deleted_at) VALUES ($1, $2, $3, $4, $5)`,
		id, name, "", now, nil); err != nil {
		return "", serr.Wrap(err, "cannot create tag", "tag", name)
	}
	existing[key] = id
	return id, nil
}

// loadTagsForMatching reads every tag — tombstoned ones included — into a
// map keyed by lowercased name.
//
// Case-folding in Go rather than in SQL is a deliberate trade. The unique
// index on tags.name is case-SENSITIVE, so "Grace" and "grace" could both be
// inserted; folding here means the second one finds the first instead. The
// tags table is tens of rows in a personal study database, so reading it whole
// once per note save costs nothing measurable, and it avoids an ILIKE whose
// wildcards would have to be escaped out of user-typed tag names.
func loadTagsForMatching(tx *sql.Tx) (map[string]string, error) {
	rows, err := tx.Query(`SELECT id, name FROM tags`)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read tags")
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, serr.Wrap(err, "cannot scan tag row")
		}
		out[strings.ToLower(name)] = id
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate tag rows")
	}
	return out, nil
}

// normalizeTagNames trims, collapses internal whitespace, drops empties, and
// de-duplicates case-insensitively while keeping the first spelling the user
// typed. "grace, Grace , grace" becomes one tag named "grace".
func normalizeTagNames(names []string) []string {
	var out []string
	seen := map[string]bool{}

	for _, raw := range names {
		name := strings.Join(strings.Fields(raw), " ")
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
}

// SplitTagNames parses the comma-separated tag field of the note editor.
// Exported because the web layer is what receives the raw form value.
func SplitTagNames(field string) []string {
	return normalizeTagNames(strings.Split(field, ","))
}

// tagNoteCounts returns live-note counts per tag id.
func tagNoteCounts(db *sql.DB) (map[string]int, error) {
	rows, err := db.Query(
		`SELECT nt.tag_id, COUNT(*) FROM note_tags nt
		   INNER JOIN notes n ON n.id = nt.note_id
		  WHERE n.deleted_at IS NULL
		  GROUP BY nt.tag_id`)
	if err != nil {
		return nil, serr.Wrap(err, "cannot count tagged notes")
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, serr.Wrap(err, "cannot scan tag count row")
		}
		out[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate tag count rows")
	}
	return out, nil
}

// attachTags fills in the Tags field of an already-loaded batch of notes, in
// one query keyed on the note ids in hand.
func attachTags(db *sql.DB, ids []string, byID map[string]*Note) error {
	list, args := placeholders(ids)

	rows, err := db.Query(
		`SELECT nt.note_id, t.id, t.name, t.descrip, t.updated_at
		   FROM note_tags nt INNER JOIN tags t ON t.id = nt.tag_id
		  WHERE nt.note_id IN (`+list+`) AND t.deleted_at IS NULL
		  ORDER BY t.name`, args...)
	if err != nil {
		return serr.Wrap(err, "cannot read note tags")
	}
	defer rows.Close()

	for rows.Next() {
		var noteID string
		var t Tag
		if err := rows.Scan(&noteID, &t.ID, &t.Name, &t.Descrip, &t.UpdatedAt); err != nil {
			return serr.Wrap(err, "cannot scan note tag row")
		}
		if n, ok := byID[noteID]; ok {
			n.Tags = append(n.Tags, t)
		}
	}
	if err := rows.Err(); err != nil {
		return serr.Wrap(err, "cannot iterate note tag rows")
	}
	return nil
}

// scanTags drains a tag result set, closing rows on every path.
func scanTags(rows *sql.Rows) ([]Tag, error) {
	defer rows.Close()

	var out []Tag
	for rows.Next() {
		var t Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Descrip, &t.UpdatedAt); err != nil {
			return nil, serr.Wrap(err, "cannot scan tag row")
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate tag rows")
	}
	return out, nil
}
