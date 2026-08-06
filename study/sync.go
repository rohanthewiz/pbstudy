package study

import (
	"database/sql"
	"strings"
	"time"

	"github.com/rohanthewiz/serr"

	"github.com/rohanthewiz/pbstudy/bible"
)

// This file is the study database's half of the sync engine: the on-disk shape
// of every syncable entity, a whole-table export, and an idempotent apply.
//
// # Why the record types are not the domain types
//
// Note, Tag, CrossRef and Sermon are shaped for the pages that display them —
// tombstones filtered out, tag links resolved, unexported fields recording
// which end a cross-reference was loaded from. A sync record is the opposite:
// it must carry tombstones (a delete that leaves no trace resurrects on the
// next import), it must be stable across versions of this program, and every
// field it has is one somebody may have to read in a text editor.
//
// Keeping them separate means the display types stay free to change shape
// without silently rewriting the format of files already sitting in someone's
// iCloud folder.
//
// # The one clock everything is compared on
//
// updated_at, UTC, last writer wins. bytdb stores TIMESTAMP at microsecond
// precision (verified: a value written with nanoseconds reads back truncated),
// so every record here is built from a database read rather than from an
// in-memory value, and RFC3339Nano round-trips it exactly. The syncer compares
// at that same precision — see syncer.newer.

// SyncVersion is the format version stamped into every exported file.
//
// A file carrying a HIGHER version is skipped on import rather than guessed at.
// Reading a format this binary does not know would at best drop fields; worse,
// the export half of the same run would then write the truncated record back
// and destroy what the newer machine wrote.
const SyncVersion = 1

// Record kinds. These are written into each file and checked on import, so a
// file that lands in the wrong folder is reported rather than half-applied.
const (
	SyncKindNote   = "note"
	SyncKindTag    = "tag"
	SyncKindXref   = "xref"
	SyncKindSermon = "sermon"
)

// Syncable is what the syncer needs of any record: who it is, when it was last
// written, what it claims to be, and whether it is a tombstone.
//
// The methods are named SyncX rather than ID/UpdatedAt because the records
// carry fields by those names — and the fields are what the JSON is built
// from, so they stay plain data.
type Syncable interface {
	SyncID() string
	SyncStamp() time.Time
	SyncKind() string
	SyncVersion() int
	SyncDeleted() time.Time
}

// deletedAt is the zero-time form of a tombstone pointer.
//
// The records carry *time.Time so an absent tombstone is an absent JSON field
// rather than a zero time somebody has to remember means "alive"; compaction
// wants the same fact as a plain value it can compare against a cutoff without
// a nil check at every call site. IsZero() is the "still alive" test.
func deletedAt(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

// NoteRecord is a note as it travels between machines.
//
// Tags are carried as NAMES, not ids. Tag identity across machines is the name
// — two machines that independently create "Grace" have two different ids for
// one idea — so a note that referred to a tag by id would arrive pointing at
// nothing. Names are resolved (and created) on import.
//
// Refs are embedded rather than filed separately: a note's anchors have no
// identity of their own and are replaced wholesale on import, so there is
// nothing to merge and nothing to tombstone.
type NoteRecord struct {
	Kind      string      `json:"kind"`
	V         int         `json:"v"`
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	BodyMD    string      `json:"bodyMd"`
	Refs      []bible.Ref `json:"refs,omitempty"`
	Tags      []string    `json:"tags,omitempty"`
	CreatedAt time.Time   `json:"createdAt"`
	UpdatedAt time.Time   `json:"updatedAt"`
	DeletedAt *time.Time  `json:"deletedAt,omitempty"`
}

func (r NoteRecord) SyncID() string         { return r.ID }
func (r NoteRecord) SyncStamp() time.Time   { return r.UpdatedAt }
func (r NoteRecord) SyncKind() string       { return r.Kind }
func (r NoteRecord) SyncVersion() int       { return r.V }
func (r NoteRecord) SyncDeleted() time.Time { return deletedAt(r.DeletedAt) }

// TagRecord is a tag as it travels. Name is the cross-machine identity; see
// ApplyTag for what happens when two machines minted different ids for it.
type TagRecord struct {
	Kind      string     `json:"kind"`
	V         int        `json:"v"`
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Descrip   string     `json:"descrip,omitempty"`
	UpdatedAt time.Time  `json:"updatedAt"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
}

func (r TagRecord) SyncID() string         { return r.ID }
func (r TagRecord) SyncStamp() time.Time   { return r.UpdatedAt }
func (r TagRecord) SyncKind() string       { return r.Kind }
func (r TagRecord) SyncVersion() int       { return r.V }
func (r TagRecord) SyncDeleted() time.Time { return deletedAt(r.DeletedAt) }

// XrefRecord is a cross-reference as it travels. From and To are whole Refs
// rather than the eight loose columns the table stores, because the file is
// something a person may have to read.
type XrefRecord struct {
	Kind      string     `json:"kind"`
	V         int        `json:"v"`
	ID        string     `json:"id"`
	From      bible.Ref  `json:"from"`
	To        bible.Ref  `json:"to"`
	Comment   string     `json:"comment,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
}

func (r XrefRecord) SyncID() string         { return r.ID }
func (r XrefRecord) SyncStamp() time.Time   { return r.UpdatedAt }
func (r XrefRecord) SyncKind() string       { return r.Kind }
func (r XrefRecord) SyncVersion() int       { return r.V }
func (r XrefRecord) SyncDeleted() time.Time { return deletedAt(r.DeletedAt) }

// SermonRecord is a sermon as it travels, outline and draft included. The
// outline is the same []Section the database column holds, so the file and the
// column agree by construction.
type SermonRecord struct {
	Kind      string     `json:"kind"`
	V         int        `json:"v"`
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Outline   []Section  `json:"outline,omitempty"`
	DraftMD   string     `json:"draftMd,omitempty"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	DeletedAt *time.Time `json:"deletedAt,omitempty"`
}

func (r SermonRecord) SyncID() string         { return r.ID }
func (r SermonRecord) SyncStamp() time.Time   { return r.UpdatedAt }
func (r SermonRecord) SyncKind() string       { return r.Kind }
func (r SermonRecord) SyncVersion() int       { return r.V }
func (r SermonRecord) SyncDeleted() time.Time { return deletedAt(r.DeletedAt) }

// --- export ----------------------------------------------------------------

// ExportNotes reads every note — tombstones included — with its anchors and
// tag names.
//
// Whole-table scans with no IN lists: sync walks everything anyway, so three
// full reads beat one query per note, and a personal study database is a few
// hundred rows.
func ExportNotes(db *sql.DB) ([]NoteRecord, error) {
	refs, err := allNoteRefs(db)
	if err != nil {
		return nil, err
	}
	tags, err := allNoteTagNames(db)
	if err != nil {
		return nil, err
	}

	rows, err := db.Query(
		`SELECT id, title, body_md, created_at, updated_at, deleted_at
		   FROM notes ORDER BY id`)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read notes for export")
	}
	defer rows.Close()

	var out []NoteRecord
	for rows.Next() {
		rec := NoteRecord{Kind: SyncKindNote, V: SyncVersion}
		var deleted sql.NullTime
		if err := rows.Scan(&rec.ID, &rec.Title, &rec.BodyMD,
			&rec.CreatedAt, &rec.UpdatedAt, &deleted); err != nil {
			return nil, serr.Wrap(err, "cannot scan note row for export")
		}
		rec.DeletedAt = nullTime(deleted)
		rec.Refs = refs[rec.ID]
		rec.Tags = tags[rec.ID]
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate notes for export")
	}
	return out, nil
}

// ExportTags reads every tag, tombstones included.
func ExportTags(db *sql.DB) ([]TagRecord, error) {
	rows, err := db.Query(
		`SELECT id, name, descrip, updated_at, deleted_at FROM tags ORDER BY id`)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read tags for export")
	}
	defer rows.Close()

	var out []TagRecord
	for rows.Next() {
		rec := TagRecord{Kind: SyncKindTag, V: SyncVersion}
		var deleted sql.NullTime
		if err := rows.Scan(&rec.ID, &rec.Name, &rec.Descrip,
			&rec.UpdatedAt, &deleted); err != nil {
			return nil, serr.Wrap(err, "cannot scan tag row for export")
		}
		rec.DeletedAt = nullTime(deleted)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate tags for export")
	}
	return out, nil
}

// ExportXrefs reads every cross-reference, tombstones included.
func ExportXrefs(db *sql.DB) ([]XrefRecord, error) {
	rows, err := db.Query(
		`SELECT id, from_book, from_chapter, from_verse,
		        to_book, to_chapter, to_verse_start, to_verse_end,
		        comment, created_at, updated_at, deleted_at
		   FROM cross_refs ORDER BY id`)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read cross-references for export")
	}
	defer rows.Close()

	var out []XrefRecord
	for rows.Next() {
		rec := XrefRecord{Kind: SyncKindXref, V: SyncVersion}
		var deleted sql.NullTime
		if err := rows.Scan(&rec.ID, &rec.From.BookNum, &rec.From.Chapter, &rec.From.VerseStart,
			&rec.To.BookNum, &rec.To.Chapter, &rec.To.VerseStart, &rec.To.VerseEnd,
			&rec.Comment, &rec.CreatedAt, &rec.UpdatedAt, &deleted); err != nil {
			return nil, serr.Wrap(err, "cannot scan cross-reference row for export")
		}
		// The From end is always one verse; filling VerseEnd keeps the file
		// reading the same as Ref.String would render it.
		rec.From.VerseEnd = rec.From.VerseStart
		rec.DeletedAt = nullTime(deleted)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate cross-references for export")
	}
	return out, nil
}

// ExportSermons reads every sermon, tombstones included.
//
// An outline that will not decode is exported as an empty outline rather than
// failing the run: the title, the draft and the timestamps are still worth
// carrying to the other machine, and the unreadable column is already reported
// wherever that sermon is opened.
func ExportSermons(db *sql.DB) ([]SermonRecord, error) {
	rows, err := db.Query(
		`SELECT id, title, outline, draft_md, status, created_at, updated_at, deleted_at
		   FROM sermons ORDER BY id`)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read sermons for export")
	}
	defer rows.Close()

	var out []SermonRecord
	for rows.Next() {
		rec := SermonRecord{Kind: SyncKindSermon, V: SyncVersion}
		var outline, draft sql.NullString
		var deleted sql.NullTime
		if err := rows.Scan(&rec.ID, &rec.Title, &outline, &draft, &rec.Status,
			&rec.CreatedAt, &rec.UpdatedAt, &deleted); err != nil {
			return nil, serr.Wrap(err, "cannot scan sermon row for export")
		}
		rec.DraftMD = draft.String
		rec.Outline, _ = decodeOutline(outline)
		rec.DeletedAt = nullTime(deleted)
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate sermons for export")
	}
	return out, nil
}

// --- import ----------------------------------------------------------------

// ApplyNote writes an incoming note over whatever is here, children included.
//
// The caller has already decided this record wins on the clock; this function's
// job is to make the local row match it exactly. Anchors and tag links are
// replaced wholesale — they are carried inside the record, so "replace" is the
// whole merge — and missing tags are created by name.
//
// Tombstoned records go through the same path rather than a delete branch: the
// record still carries the children it had when it was deleted, so a note that
// is later undeleted on the other machine comes back whole.
func ApplyNote(db *sql.DB, rec NoteRecord) error {
	if rec.ID == "" {
		return serr.New("note record has no id")
	}

	tx, err := db.Begin()
	if err != nil {
		return serr.Wrap(err, "cannot begin transaction")
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`UPDATE notes SET title = $1, body_md = $2, created_at = $3,
		        updated_at = $4, deleted_at = $5
		  WHERE id = $6`,
		rec.Title, rec.BodyMD, rec.CreatedAt, rec.UpdatedAt, rec.DeletedAt, rec.ID)
	if err != nil {
		return serr.Wrap(err, "cannot update note from sync", "note", rec.ID)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		if _, err := tx.Exec(
			`INSERT INTO notes (id, title, body_md, created_at, updated_at, deleted_at)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			rec.ID, rec.Title, rec.BodyMD, rec.CreatedAt, rec.UpdatedAt, rec.DeletedAt); err != nil {
			return serr.Wrap(err, "cannot insert note from sync", "note", rec.ID)
		}
	}

	if _, err := tx.Exec(`DELETE FROM note_refs WHERE note_id = $1`, rec.ID); err != nil {
		return serr.Wrap(err, "cannot clear note refs", "note", rec.ID)
	}
	if _, err := tx.Exec(`DELETE FROM note_tags WHERE note_id = $1`, rec.ID); err != nil {
		return serr.Wrap(err, "cannot clear note tags", "note", rec.ID)
	}
	if err := writeRefs(tx, rec.ID, rec.Refs, rec.UpdatedAt); err != nil {
		return err
	}
	if err := writeTagLinks(tx, rec.ID, rec.Tags, rec.UpdatedAt); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return serr.Wrap(err, "cannot commit note from sync", "note", rec.ID)
	}
	return nil
}

// Outcomes of ApplyTag. A tag is the one entity whose incoming record may match
// a local row the caller could not have known about — see ApplyTag — so it is
// the one that has to report what it decided.
const (
	// TagUnchanged means the local tag of this name was already at least this
	// new. Reported so a repeated sync stops claiming to import something.
	TagUnchanged = ""
	// TagWritten means the row with this id was inserted or updated.
	TagWritten = "written"
	// TagMerged means the record was folded into a local tag of the same name
	// carrying a different id.
	TagMerged = "merged"
)

// ApplyTag writes an incoming tag, matching on name when the id is unknown, and
// reports which of the outcomes above happened.
//
// # Why name matching is mandatory, not a nicety
//
// Two machines offline at the same time both create "Grace" and mint different
// UUIDs for it. The local unique index on tags.name would REJECT the second one
// outright, so an id-only import cannot even insert it. Matching on the folded
// name instead means the two converge on one row.
//
// The local id survives that merge, so the two machines keep different ids for
// the same tag forever — and that is fine, because nothing else refers to a tag
// by id across machines: notes carry tag NAMES. The visible cost is one
// redundant file per machine in the sync folder.
//
// # Why the merge branch re-checks the clock
//
// The syncer decides what to import by comparing a file against the local row
// with the SAME id, and in this branch there is no such row — so its comparison
// was never made. Applying unconditionally would rewrite the local tag's
// timestamp on every single run, which the run after that would then export,
// forever. Comparing against the row actually matched is what makes a second
// sync of an unchanged folder do nothing.
func ApplyTag(db *sql.DB, rec TagRecord) (outcome string, err error) {
	if rec.ID == "" {
		return TagUnchanged, serr.New("tag record has no id")
	}
	if strings.TrimSpace(rec.Name) == "" {
		return TagUnchanged, serr.New("tag record has no name", "tag", rec.ID)
	}

	tx, err := db.Begin()
	if err != nil {
		return TagUnchanged, serr.Wrap(err, "cannot begin transaction")
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(
		`UPDATE tags SET name = $1, descrip = $2, updated_at = $3, deleted_at = $4
		  WHERE id = $5`,
		rec.Name, rec.Descrip, rec.UpdatedAt, rec.DeletedAt, rec.ID)
	if err != nil {
		return TagUnchanged, serr.Wrap(err, "cannot update tag from sync", "tag", rec.ID)
	}

	outcome = TagWritten
	localID := rec.ID

	if n, _ := res.RowsAffected(); n == 0 {
		match, err := matchTagByName(tx, rec.Name)
		if err != nil {
			return TagUnchanged, err
		}

		switch {
		case match.id == "":
			if _, err := tx.Exec(
				`INSERT INTO tags (id, name, descrip, updated_at, deleted_at)
				 VALUES ($1, $2, $3, $4, $5)`,
				rec.ID, rec.Name, rec.Descrip, rec.UpdatedAt, rec.DeletedAt); err != nil {
				return TagUnchanged, serr.Wrap(err, "cannot insert tag from sync", "tag", rec.ID)
			}

		case !rec.UpdatedAt.After(match.updatedAt):
			return TagUnchanged, nil // the tag of this name here is already at least this new

		default:
			// Same idea, different id. Keep the local spelling and id; take the
			// description and the tombstone state, which is all that can
			// meaningfully differ.
			if _, err := tx.Exec(
				`UPDATE tags SET descrip = $1, updated_at = $2, deleted_at = $3 WHERE id = $4`,
				rec.Descrip, rec.UpdatedAt, rec.DeletedAt, match.id); err != nil {
				return TagUnchanged, serr.Wrap(err, "cannot merge tag from sync", "tag", rec.Name)
			}
			localID = match.id
			outcome = TagMerged
		}
	}

	// A retired tag must stop appearing on notes here as immediately as it does
	// on the machine it was retired from — the same asymmetry DeleteTag makes:
	// the tag keeps its tombstone, the links go for real.
	if rec.DeletedAt != nil {
		if _, err := tx.Exec(`DELETE FROM note_tags WHERE tag_id = $1`, localID); err != nil {
			return TagUnchanged, serr.Wrap(err, "cannot unlink deleted tag", "tag", localID)
		}
	}

	if err := tx.Commit(); err != nil {
		return TagUnchanged, serr.Wrap(err, "cannot commit tag from sync", "tag", rec.ID)
	}
	return outcome, nil
}

// tagMatch is a local tag found by name: its id and the clock to compare
// against. A zero id means there is none.
type tagMatch struct {
	id        string
	updatedAt time.Time
}

// matchTagByName finds a local tag by case-folded name, tombstones included.
//
// Folding in Go rather than SQL for the same reason loadTagsForMatching does
// it: the unique index on tags.name is case-sensitive, so the match has to be
// made where the comparison can be case-insensitive without turning a
// user-typed name into a LIKE pattern.
func matchTagByName(tx *sql.Tx, name string) (tagMatch, error) {
	rows, err := tx.Query(`SELECT id, name, updated_at FROM tags`)
	if err != nil {
		return tagMatch{}, serr.Wrap(err, "cannot read tags")
	}
	defer rows.Close()

	want := strings.ToLower(strings.TrimSpace(name))
	var found tagMatch

	for rows.Next() {
		var id, have string
		var updated time.Time
		if err := rows.Scan(&id, &have, &updated); err != nil {
			return tagMatch{}, serr.Wrap(err, "cannot scan tag row")
		}
		if strings.ToLower(strings.TrimSpace(have)) == want {
			found = tagMatch{id: id, updatedAt: updated}
		}
	}
	if err := rows.Err(); err != nil {
		return tagMatch{}, serr.Wrap(err, "cannot iterate tag rows")
	}
	return found, nil
}

// ApplyXref writes an incoming cross-reference over whatever is here.
func ApplyXref(db *sql.DB, rec XrefRecord) error {
	if rec.ID == "" {
		return serr.New("cross-reference record has no id")
	}

	// A single-verse target arrives with VerseEnd unset (omitempty drops it),
	// so the end is clamped up to the start rather than stored as zero — which
	// the containment predicate in XrefsTo would read as an empty range.
	toEnd := max(rec.To.VerseEnd, rec.To.VerseStart)

	res, err := db.Exec(
		`UPDATE cross_refs SET from_book = $1, from_chapter = $2, from_verse = $3,
		        to_book = $4, to_chapter = $5, to_verse_start = $6, to_verse_end = $7,
		        comment = $8, created_at = $9, updated_at = $10, deleted_at = $11
		  WHERE id = $12`,
		rec.From.BookNum, rec.From.Chapter, rec.From.VerseStart,
		rec.To.BookNum, rec.To.Chapter, rec.To.VerseStart, toEnd,
		rec.Comment, rec.CreatedAt, rec.UpdatedAt, rec.DeletedAt, rec.ID)
	if err != nil {
		return serr.Wrap(err, "cannot update cross-reference from sync", "xref", rec.ID)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return nil
	}

	if _, err := db.Exec(
		`INSERT INTO cross_refs (id, from_book, from_chapter, from_verse,
		     to_book, to_chapter, to_verse_start, to_verse_end,
		     comment, created_at, updated_at, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		rec.ID, rec.From.BookNum, rec.From.Chapter, rec.From.VerseStart,
		rec.To.BookNum, rec.To.Chapter, rec.To.VerseStart, toEnd,
		rec.Comment, rec.CreatedAt, rec.UpdatedAt, rec.DeletedAt); err != nil {
		return serr.Wrap(err, "cannot insert cross-reference from sync", "xref", rec.ID)
	}
	return nil
}

// ApplySermon writes an incoming sermon over whatever is here.
//
// The status is normalized on the way in. "drafting" means a generator is
// running in THIS process against THIS registry; it can survive a crash on the
// other machine, and importing it would leave a sermon that looks perpetually
// busy. The draft text is the honest evidence of what happened.
func ApplySermon(db *sql.DB, rec SermonRecord) error {
	if rec.ID == "" {
		return serr.New("sermon record has no id")
	}

	outline, err := encodeOutline(rec.Outline)
	if err != nil {
		return err
	}

	status := rec.Status
	if status == StatusDrafting || status == "" {
		status = StatusOutline
		if strings.TrimSpace(rec.DraftMD) != "" {
			status = StatusDrafted
		}
	}

	res, err := db.Exec(
		`UPDATE sermons SET title = $1, outline = $2, draft_md = $3, status = $4,
		        created_at = $5, updated_at = $6, deleted_at = $7
		  WHERE id = $8`,
		rec.Title, outline, rec.DraftMD, status,
		rec.CreatedAt, rec.UpdatedAt, rec.DeletedAt, rec.ID)
	if err != nil {
		return serr.Wrap(err, "cannot update sermon from sync", "sermon", rec.ID)
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return nil
	}

	if _, err := db.Exec(
		`INSERT INTO sermons (id, title, outline, draft_md, status, created_at, updated_at, deleted_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		rec.ID, rec.Title, outline, rec.DraftMD, status,
		rec.CreatedAt, rec.UpdatedAt, rec.DeletedAt); err != nil {
		return serr.Wrap(err, "cannot insert sermon from sync", "sermon", rec.ID)
	}
	return nil
}

// --- internals -------------------------------------------------------------

// allNoteRefs reads every note anchor in one scan, keyed by note.
func allNoteRefs(db *sql.DB) (map[string][]bible.Ref, error) {
	rows, err := db.Query(
		`SELECT note_id, book_num, chapter, verse_start, verse_end
		   FROM note_refs ORDER BY book_num, chapter, verse_start`)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read note refs for export")
	}
	defer rows.Close()

	out := map[string][]bible.Ref{}
	for rows.Next() {
		var noteID string
		var ref bible.Ref
		if err := rows.Scan(&noteID, &ref.BookNum, &ref.Chapter,
			&ref.VerseStart, &ref.VerseEnd); err != nil {
			return nil, serr.Wrap(err, "cannot scan note ref for export")
		}
		out[noteID] = append(out[noteID], ref)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate note refs for export")
	}
	return out, nil
}

// allNoteTagNames reads every live tag link in one scan, as names keyed by
// note. Tombstoned tags are excluded: DeleteTag removes the links, so a link to
// one is a row that should not have survived.
func allNoteTagNames(db *sql.DB) (map[string][]string, error) {
	rows, err := db.Query(
		`SELECT nt.note_id, t.name
		   FROM note_tags nt INNER JOIN tags t ON t.id = nt.tag_id
		  WHERE t.deleted_at IS NULL
		  ORDER BY t.name`)
	if err != nil {
		return nil, serr.Wrap(err, "cannot read note tags for export")
	}
	defer rows.Close()

	out := map[string][]string{}
	for rows.Next() {
		var noteID, name string
		if err := rows.Scan(&noteID, &name); err != nil {
			return nil, serr.Wrap(err, "cannot scan note tag for export")
		}
		out[noteID] = append(out[noteID], name)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate note tags for export")
	}
	return out, nil
}

// nullTime turns a scanned NULL timestamp into the pointer the record types
// use, so an absent tombstone is an absent JSON field rather than a zero time
// somebody has to remember means "alive".
func nullTime(t sql.NullTime) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time.UTC()
	return &v
}
