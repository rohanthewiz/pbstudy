package syncer

import (
	"strconv"
	"strings"
	"time"
)

// Report is what one reconciliation pass did, per entity.
//
// This is a first-class result rather than a log line. Sync is the one feature
// in this app whose success is invisible — nothing on screen changes when it
// works — so "6 notes in, 2 out, 1 file I could not read" is the whole user
// interface for it. A silent sync that quietly failed to import the note
// written on the other machine is indistinguishable from one that had nothing
// to do, and that is exactly the failure this type exists to prevent.
type Report struct {
	Dir       string
	StartedAt time.Time
	Duration  time.Duration
	Entities  []EntityReport
}

// EntityReport is one entity's share of a pass.
type EntityReport struct {
	// Kind is a study.SyncKind* value.
	Kind string

	Imported int // files that overwrote a local row
	Exported int // rows that overwrote a file
	Skipped  int // both sides already agreed

	// Problems are the files this pass could not use, and the rows it could
	// not write, in the words the user needs. They are collected rather than
	// returned as an error because one broken file must not stop the rest of
	// the pass — but it must not vanish either.
	Problems []string

	// Notes are things that happened and are worth saying, though nothing went
	// wrong: a tag merged into one of the same name, for instance.
	Notes []string
}

// problemSink is anything that collects the files a pass could not use.
//
// The file readers are shared between reconciliation and compaction, and the
// two report what they did in different words — "3 in, 2 out" against "3 rows
// removed" — but an unreadable file is the same fact to both. This is the one
// thing they have in common, so it is the only thing the readers ask for.
type problemSink interface{ fail(msg string) }

// fail records a problem. Unexported: only the pass that hit it may add one.
func (e *EntityReport) fail(msg string) { e.Problems = append(e.Problems, msg) }

// Note records something worth saying that was not a failure.
func (e *EntityReport) Note(msg string) { e.Notes = append(e.Notes, msg) }

// Changed reports whether this entity moved anything in either direction.
func (e EntityReport) Changed() bool { return e.Imported > 0 || e.Exported > 0 }

// Label is the entity's plural name for display.
func (e EntityReport) Label() string { return kindLabel(e.Kind) }

// kindLabel renders a study.SyncKind* value as the heading a person reads.
// Shared by both report types so the two never disagree about what to call a
// cross-reference.
func kindLabel(kind string) string {
	switch kind {
	case "note":
		return "Notes"
	case "tag":
		return "Tags"
	case "xref":
		return "Cross-references"
	case "sermon":
		return "Sermons"
	default:
		return kind
	}
}

// Summary is the one-line description of what happened to this entity.
func (e EntityReport) Summary() string {
	if !e.Changed() && len(e.Problems) == 0 {
		return "nothing to do (" + strconv.Itoa(e.Skipped) + " already in step)"
	}

	var parts []string
	if e.Imported > 0 {
		parts = append(parts, strconv.Itoa(e.Imported)+" in")
	}
	if e.Exported > 0 {
		parts = append(parts, strconv.Itoa(e.Exported)+" out")
	}
	if e.Skipped > 0 {
		parts = append(parts, strconv.Itoa(e.Skipped)+" unchanged")
	}
	if len(e.Problems) > 0 {
		parts = append(parts, strconv.Itoa(len(e.Problems))+" could not be used")
	}
	return strings.Join(parts, " · ")
}

// Imported totals the incoming direction across every entity.
func (r Report) Imported() int { return r.total(func(e EntityReport) int { return e.Imported }) }

// Exported totals the outgoing direction across every entity.
func (r Report) Exported() int { return r.total(func(e EntityReport) int { return e.Exported }) }

// Problems totals the files and rows this pass could not use.
func (r Report) Problems() int { return r.total(func(e EntityReport) int { return len(e.Problems) }) }

func (r Report) total(of func(EntityReport) int) int {
	n := 0
	for _, e := range r.Entities {
		n += of(e)
	}
	return n
}

// Headline is the sentence the settings page leads with.
func (r Report) Headline() string {
	switch {
	case r.Imported() == 0 && r.Exported() == 0 && r.Problems() == 0:
		return "Everything was already in step."
	case r.Problems() > 0:
		return plural(r.Imported(), "change") + " in, " +
			plural(r.Exported(), "file") + " out, " +
			plural(r.Problems(), "problem") + "."
	default:
		return plural(r.Imported(), "change") + " in, " +
			plural(r.Exported(), "file") + " out."
	}
}

// plural renders "1 note" / "2 notes" for the small set of words used here.
func plural(n int, word string) string {
	s := strconv.Itoa(n) + " " + word
	if n != 1 {
		s += "s"
	}
	return s
}
