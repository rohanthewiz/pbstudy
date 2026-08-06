package study

import (
	"database/sql"
	"time"

	"github.com/rohanthewiz/serr"
)

// This file is the only place in the program that physically removes a row.
//
// # Why tombstones exist, and why they cannot live forever
//
// Deleting a row outright is unsyncable: the other machine still holds it, and
// the next import — which creates any record it has no local row for — puts it
// straight back. So every delete in this app sets deleted_at and leaves the row
// where it is, and the row's file in the sync folder becomes the evidence the
// other machines need to make the same delete.
//
// That evidence has a job with an end. Once every machine has seen the delete,
// the row and its file are carrying nothing but weight. Nothing here can know
// when that moment arrives — this package never talks to another machine — so
// the caller supplies a cutoff and compaction trusts it. syncer.Compact is what
// picks one, and syncer.DefaultRetention explains the number.
//
// # The two-step shape
//
// Finding and removing are separate calls on purpose. The syncer has to delete
// a row's file as well as the row, so it needs the id list in hand; and a dry
// run is then simply the find without the purge. Both halves are cheap — a
// personal study database is a few hundred rows.

// tombstonedTables are the tables compaction may touch. Their names are
// interpolated into SQL below, so this list — not a caller's string — is what
// keeps that interpolation safe.
const (
	tableNotes   = "notes"
	tableTags    = "tags"
	tableXrefs   = "cross_refs"
	tableSermons = "sermons"
)

// ExpiredNotes lists notes tombstoned strictly before cutoff.
func ExpiredNotes(db *sql.DB, cutoff time.Time) ([]string, error) {
	return expiredIn(db, tableNotes, cutoff)
}

// ExpiredTags lists tags tombstoned strictly before cutoff.
func ExpiredTags(db *sql.DB, cutoff time.Time) ([]string, error) {
	return expiredIn(db, tableTags, cutoff)
}

// ExpiredXrefs lists cross-references tombstoned strictly before cutoff.
func ExpiredXrefs(db *sql.DB, cutoff time.Time) ([]string, error) {
	return expiredIn(db, tableXrefs, cutoff)
}

// ExpiredSermons lists sermons tombstoned strictly before cutoff.
func ExpiredSermons(db *sql.DB, cutoff time.Time) ([]string, error) {
	return expiredIn(db, tableSermons, cutoff)
}

// PurgeNotes removes the given notes for real, anchors and tag links included,
// and returns the ids it actually removed.
//
// DeleteNote deliberately leaves note_refs and note_tags in place — a tombstoned
// note that comes back must come back whole — so this is where those rows
// finally go. They have no tombstone of their own and nothing reads them across
// a dead note, so removing them is invisible to every other query.
//
// A note may still be named by a live sermon's outline (Section.NoteID). That is
// already the case while the note is tombstoned: resolveOutline filters
// tombstones, so the section renders as a missing-note marker either way. What
// compaction takes away is the chance to undelete it, which is precisely the
// chance the retention window was there to preserve.
func PurgeNotes(db *sql.DB, ids []string) ([]string, error) {
	return purgeRows(db, tableNotes, ids, func(tx *sql.Tx, in string, args []any) error {
		if _, err := tx.Exec(`DELETE FROM note_refs WHERE note_id IN (`+in+`)`, args...); err != nil {
			return serr.Wrap(err, "cannot remove anchors of purged notes")
		}
		if _, err := tx.Exec(`DELETE FROM note_tags WHERE note_id IN (`+in+`)`, args...); err != nil {
			return serr.Wrap(err, "cannot remove tag links of purged notes")
		}
		return nil
	})
}

// PurgeTags removes the given tags for real and returns the ids removed.
//
// DeleteTag and ApplyTag both drop a retired tag's links immediately, so there
// is normally nothing left to clear — but a tag revived by ensureTag and
// re-tombstoned, or a link written by an import that landed between the two,
// can leave one behind. Clearing again costs one statement and closes the case.
func PurgeTags(db *sql.DB, ids []string) ([]string, error) {
	return purgeRows(db, tableTags, ids, func(tx *sql.Tx, in string, args []any) error {
		if _, err := tx.Exec(`DELETE FROM note_tags WHERE tag_id IN (`+in+`)`, args...); err != nil {
			return serr.Wrap(err, "cannot remove links of purged tags")
		}
		return nil
	})
}

// PurgeXrefs removes the given cross-references for real. Nothing hangs off a
// cross-reference — both ends are plain columns — so there are no children.
func PurgeXrefs(db *sql.DB, ids []string) ([]string, error) {
	return purgeRows(db, tableXrefs, ids, nil)
}

// PurgeSermons removes the given sermons for real. The outline lives in the
// row's own JSONB column, so it goes with it.
func PurgeSermons(db *sql.DB, ids []string) ([]string, error) {
	return purgeRows(db, tableSermons, ids, nil)
}

// --- internals -------------------------------------------------------------

// expiredIn lists the ids of rows in one table tombstoned strictly before
// cutoff.
//
// Strictly before, not at-or-before: an equal clock is the same tie the sync
// merge refuses to break (see syncer.newer), and there is no reason for
// compaction to be the one operation in this program that resolves it.
func expiredIn(db *sql.DB, table string, cutoff time.Time) ([]string, error) {
	rows, err := db.Query(
		`SELECT id FROM `+table+`
		  WHERE deleted_at IS NOT NULL AND deleted_at < $1
		  ORDER BY id`, cutoff.UTC())
	if err != nil {
		return nil, serr.Wrap(err, "cannot read expired tombstones", "table", table)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, serr.Wrap(err, "cannot scan tombstone id", "table", table)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate tombstones", "table", table)
	}
	return out, nil
}

// purgeRows deletes tombstoned rows from one table, giving children a chance to
// go first, and returns the ids actually removed.
//
// The id list arrives from the syncer, which assembled it from a sync folder as
// well as from this database — so it is narrowed against deleted_at here before
// anything is deleted. That guard is the whole safety of this file: a file
// claiming a row is dead is not the same thing as the row being dead here, and
// a live row must survive a caller that got confused. Narrowing first is also
// what keeps the children safe, since the child deletes run on the narrowed
// list rather than on what was asked for.
//
// One transaction for the whole batch: a half-purged note — row gone, anchors
// still pointing at it — is a state no query in this package expects.
func purgeRows(db *sql.DB, table string, ids []string,
	children func(tx *sql.Tx, in string, args []any) error) ([]string, error) {

	if len(ids) == 0 {
		return nil, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, serr.Wrap(err, "cannot begin transaction")
	}
	defer func() { _ = tx.Rollback() }()

	dead, err := tombstonedIDs(tx, table, ids)
	if err != nil {
		return nil, err
	}
	if len(dead) == 0 {
		return nil, nil
	}

	in, args := placeholders(dead)

	if children != nil {
		if err := children(tx, in, args); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM `+table+` WHERE id IN (`+in+`)`, args...); err != nil {
		return nil, serr.Wrap(err, "cannot remove tombstoned rows", "table", table)
	}

	if err := tx.Commit(); err != nil {
		return nil, serr.Wrap(err, "cannot commit compaction", "table", table)
	}
	return dead, nil
}

// tombstonedIDs narrows a caller's id list to the rows in this table that
// really are tombstones.
func tombstonedIDs(tx *sql.Tx, table string, ids []string) ([]string, error) {
	in, args := placeholders(ids)

	rows, err := tx.Query(
		`SELECT id FROM `+table+`
		  WHERE id IN (`+in+`) AND deleted_at IS NOT NULL`, args...)
	if err != nil {
		return nil, serr.Wrap(err, "cannot confirm tombstones", "table", table)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, serr.Wrap(err, "cannot scan tombstone id", "table", table)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, serr.Wrap(err, "cannot iterate tombstones", "table", table)
	}
	return out, nil
}
