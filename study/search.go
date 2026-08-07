package study

import (
	"database/sql"
	"strings"

	"github.com/rohanthewiz/serr"
)

// Text search over the user's own writing.
//
// # Why ILIKE and nothing cleverer
//
// bytdb has no full-text index, and the study database of a single person is
// measured in hundreds of rows, not millions. A scan costs microseconds. An
// inverted index would be a second copy of the data to keep correct through
// every edit, delete and sync import, bought with latency nobody can perceive.
// The scripture side made the same call for the same reason — see
// bible.SearchText — and the two stay symmetric on purpose: one mental model
// for "how does search work here".
//
// # Why none of them join
//
// NotesForVerse and friends need DISTINCT over a join to notes, because a note
// reachable by several anchors would otherwise repeat. The searchable text all
// lives on the notes row itself, so these queries need neither the join nor the
// DISTINCT — one scan, one row per note.

// DefaultSearchLimit caps a single result group. Large enough that a real
// search is never silently cut short, small enough that a one-letter query
// cannot render the whole database into one page.
const DefaultSearchLimit = 100

// NoteHit is one note matched by a text search, with the fragment that
// matched.
//
// The snippet is computed here rather than in the view because it depends on
// the query, and a view that re-derives "which part of this note matched" is a
// view that will disagree with the query that found it.
type NoteHit struct {
	Note Note
	// Snippet is a plain-text window around the first match in the body, or a
	// leading excerpt when the match was in the title only.
	Snippet string
}

// SearchNotes finds live notes whose title or body contains q.
//
// Ordering is by recency, matching the notes list: when two notes match
// equally well — and with no ranking, they always do — the one edited most
// recently is the one being thought about.
func SearchNotes(db *sql.DB, q string, limit int) ([]NoteHit, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	pattern := likeContains(q)
	rows, err := db.Query(
		`SELECT id, title, body_md, created_at, updated_at FROM notes
		  WHERE deleted_at IS NULL AND (title ILIKE $1 OR body_md ILIKE $1)
		  ORDER BY updated_at DESC
		  LIMIT $2`, pattern, limit)
	if err != nil {
		return nil, serr.Wrap(err, "note search failed", "q", q)
	}
	notes, err := scanNotes(rows)
	if err != nil {
		return nil, err
	}
	// Anchors and tags come along: a search result that shows which passage a
	// note hangs on is a result the user can act on without opening it.
	if err := attachRefsAndTags(db, notes); err != nil {
		return nil, err
	}

	return NoteHits(notes, q), nil
}

// NoteHits wraps already-loaded notes as search hits.
//
// Exported because the reference fast-path in the web layer produces its
// results from NotesForVerse rather than from a text query, and those results
// have to render through exactly the same view. An empty q yields a leading
// excerpt, which is the honest snippet when nothing was matched on.
func NoteHits(notes []Note, q string) []NoteHit {
	hits := make([]NoteHit, 0, len(notes))
	for _, n := range notes {
		hits = append(hits, NoteHit{
			Note:    n,
			Snippet: Snippet(n.BodyMD, q, 200),
		})
	}
	return hits
}

// SearchTags finds live tags whose name or description contains q.
//
// Tags are the topical index of the study database, so a query that names a
// topic should offer the topic page itself and not only the notes that happen
// to spell the word. Counts come from the same helper ListTags uses, so a tag
// shows the same number wherever it appears.
func SearchTags(db *sql.DB, q string) ([]Tag, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}

	pattern := likeContains(q)
	rows, err := db.Query(
		`SELECT id, name, descrip, updated_at FROM tags
		  WHERE deleted_at IS NULL AND (name ILIKE $1 OR descrip ILIKE $1)
		  ORDER BY name`, pattern)
	if err != nil {
		return nil, serr.Wrap(err, "tag search failed", "q", q)
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

// SearchXrefs finds live cross-references whose comment contains q.
//
// Included because the comment on a correlation is often the only place a
// thought was written down — "this is the protoevangelium" typed once while
// linking Genesis 3:15 to Romans 16:20 and never repeated in a note. Without
// this the text would be unreachable by search, which is the same as lost.
//
// Rows come back stamped outbound, so Other() yields the To end and the view
// can render the pair as From → To. Neither end is "the far side" here: a
// search result is read from outside the link, not from one of its ends.
func SearchXrefs(db *sql.DB, q string, limit int) ([]CrossRef, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = DefaultSearchLimit
	}

	rows, err := db.Query(
		`SELECT id, from_book, from_chapter, from_verse,
		        to_book, to_chapter, to_verse_start, to_verse_end,
		        comment, created_at, updated_at
		   FROM cross_refs
		  WHERE deleted_at IS NULL AND comment ILIKE $1
		  ORDER BY from_book, from_chapter, from_verse
		  LIMIT $2`, likeContains(q), limit)
	if err != nil {
		return nil, serr.Wrap(err, "cross-reference search failed", "q", q)
	}
	return scanXrefs(rows, false)
}

// Snippet returns a plain-text window of source centred on the first
// occurrence of q.
//
// # How the offsets stay honest
//
// The window is cut from the *stripped* text (markup removed, whitespace
// collapsed), not from the raw Markdown, so a match never comes back wrapped
// in half a link or a stray heading marker. That means the match has to be
// located in the stripped text too — searching the raw source and slicing the
// stripped copy at that offset would land somewhere else entirely.
//
// Two cases fall back to a leading excerpt rather than guessing:
//
//   - The match is not in the stripped text. It was inside markup the stripper
//     removed — a link URL, say — so there is no honest window to show.
//   - Lowercasing changed the byte length of either string. Case-insensitive
//     matching here compares lowercased copies and then slices the original,
//     which is only valid while lowercasing preserves offsets. True for ASCII,
//     not for every Unicode input, and a wrong offset would cut a rune in half.
func Snippet(source, q string, width int) string {
	if width <= 0 {
		width = 200
	}

	plain := strings.Join(strings.Fields(stripMarkup(source)), " ")
	if len(plain) <= width {
		return plain
	}
	// No query to centre on — the caller wants a preview, not a window.
	q = strings.TrimSpace(q)
	if q == "" {
		return Excerpt(source, width)
	}

	lowerPlain, lowerQ := strings.ToLower(plain), strings.ToLower(q)
	if len(lowerPlain) != len(plain) || len(lowerQ) != len(q) {
		return Excerpt(source, width)
	}

	i := strings.Index(lowerPlain, lowerQ)
	if i < 0 {
		return Excerpt(source, width)
	}

	// Centre the window on the match, then clamp to the text. The lead is
	// deliberately short of half the width: reading resumes at the match, so
	// the words after it are worth more than the words before it.
	lead := width / 3
	start := max(i-lead, 0)
	end := start + width
	if end > len(plain) {
		// Ran off the end: slide the window back so it stays full width,
		// unless the whole text is shorter than the window.
		end = len(plain)
		start = max(end-width, 0)
	}

	// Snap both cuts to word boundaries so the window does not open or close
	// mid-word, but never past the match itself.
	if start > 0 {
		if j := strings.IndexByte(plain[start:i], ' '); j >= 0 {
			start += j + 1
		}
	}
	if end < len(plain) {
		matchEnd := i + len(q)
		if j := strings.LastIndexByte(plain[matchEnd:end], ' '); j >= 0 {
			end = matchEnd + j
		}
	}

	out := plain[start:end]
	if start > 0 {
		out = "…" + out
	}
	if end < len(plain) {
		out += "…"
	}
	return out
}

// --- internals -------------------------------------------------------------

// likeContains builds a "contains" pattern with the LIKE metacharacters
// escaped out of the user's text.
//
// Without this, a search for "100%" matches every row (the % becomes a
// wildcard) and a search for "_" matches everything with at least one
// character. The backslash is escaped first, or escaping the others would
// double-escape their new backslashes.
func likeContains(q string) string {
	return "%" + likeEscaper.Replace(q) + "%"
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
