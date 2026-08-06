// Package study owns the user's own data: notes, the verse ranges they are
// anchored to, tags, and cross-references between passages.
//
// # Why this package is separate from bible
//
// Everything here lives in study.bytdb and is irreplaceable. Everything in
// bible/ lives in bible.bytdb and can be re-downloaded. Keeping them in
// different packages as well as different files means a query can never
// accidentally reach across — the two *sql.DB handles are passed in
// explicitly and there is no way to hand the wrong one to the wrong package
// without the compiler noticing the package name.
//
// # The shape every syncable row shares
//
// Notes, tags and cross-references are top-level entities. Each carries:
//
//	id          a UUID minted locally, so two machines offline at the same
//	            time can both create rows without colliding
//	updated_at  the last-writer-wins clock the Phase 5 sync engine compares
//	deleted_at  a tombstone; rows are never physically removed, because a
//	            delete that leaves no trace is indistinguishable from "the
//	            other machine has not seen this row yet" and would resurrect
//	            on the next import
//
// note_refs and note_tags are deliberately NOT in that list. They are children
// of a note with no independent identity: on sync import a note's refs and tag
// links are replaced wholesale, so they carry no tombstone and are physically
// deleted when they change.
//
// Every read in this package filters tombstones, so callers never see a
// deleted row and never have to remember to ask.
package study

import (
	"strconv"
	"time"

	"github.com/google/uuid"
)

// newID mints a primary key for a syncable row.
//
// A UUID rather than a sequence: sequence values are only unique within the
// database that issued them, and this data is meant to merge across machines
// that were both offline when the rows were created.
func newID() string { return uuid.NewString() }

// nowUTC is the single source of "now" for this package.
//
// UTC everywhere, without exception. These timestamps are compared across
// machines during sync, and a machine that moved between time zones (or
// crossed a DST boundary) must not appear to have edited a row in the past.
func nowUTC() time.Time { return time.Now().UTC() }

// placeholders builds "$1, $2, ... $n" for an IN list, and returns the ids as
// a []any ready to spread into Query.
//
// Building the list rather than looping one query per id keeps a note-list
// render at a fixed number of round trips no matter how many notes are shown.
// The values still travel as bound parameters — nothing user-supplied is ever
// concatenated into SQL.
func placeholders(ids []string) (string, []any) {
	list := make([]byte, 0, len(ids)*5)
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			list = append(list, ',', ' ')
		}
		list = append(list, '$')
		list = strconv.AppendInt(list, int64(i+1), 10)
		args[i] = id
	}
	return string(list), args
}
