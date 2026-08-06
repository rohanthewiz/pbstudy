package study

import (
	"database/sql"
	"strconv"
	"strings"

	"github.com/rohanthewiz/serr"

	"github.com/rohanthewiz/pbstudy/bible"
)

// AssembleOutline turns a sermon's outline into a Markdown document with the
// scripture and note text inlined.
//
// # Why this is the only assembler
//
// The same Markdown serves three destinations: the .md download, the standalone
// HTML export, and the payload sent to the AI drafter. One code path means the
// draft is written against exactly the document the user can read, and an
// export is exactly what the model saw. Three formatters would drift, and the
// drift would be invisible — the user would be reading one thing while the
// model read another.
//
// # Missing material is marked, not fatal
//
// A note section can point at a note that has since been deleted, and a passage
// can name a translation that was never downloaded. Neither fails the assembly:
// the gap is written into the document as an italic marker. A sermon outline is
// a working document, and refusing to produce it because one of twenty sections
// lost its source would be the wrong trade — the marker tells the preacher
// exactly what to fix, and the AI drafter is told plainly not to invent the
// missing text.
func AssembleOutline(studyDB, bibleDB *sql.DB, sermon Sermon, translation string) (string, error) {
	res, err := resolveOutline(studyDB, bibleDB, sermon.Outline, translation)
	if err != nil {
		return "", err
	}
	return formatOutline(sermon, res), nil
}

// maxInlinedVerses caps how much scripture a single passage section may inline.
//
// A whole-chapter section is legitimate — a preacher working through Romans 8
// wants the chapter — but Psalm 119 is 176 verses, and pasting it into a
// two-page outline (and into an AI prompt) helps nobody. The cap is generous
// enough to cover every chapter a sermon normally quotes whole, and the
// truncation is stated in the document rather than left to be noticed.
const maxInlinedVerses = 60

// outlineSources holds everything the formatter needs, already fetched. Keeping
// the formatter pure of database access is what makes its output testable
// without a live cache.
type outlineSources struct {
	translation string
	notes       map[string]Note
	// verses is keyed by bible.Ref, which is comparable — two sections citing
	// the same passage share one lookup.
	verses map[bible.Ref][]bible.Verse
	// truncated records the passages whose verse list hit maxInlinedVerses.
	truncated map[bible.Ref]bool
}

// resolveOutline fetches the notes and scripture an outline refers to.
func resolveOutline(studyDB, bibleDB *sql.DB, sections []Section,
	translation string) (outlineSources, error) {

	res := outlineSources{
		translation: translation,
		notes:       map[string]Note{},
		verses:      map[bible.Ref][]bible.Verse{},
		truncated:   map[bible.Ref]bool{},
	}

	var noteIDs []string
	seenNote := map[string]bool{}
	for _, sec := range sections {
		if sec.Kind == KindNote && sec.NoteID != "" && !seenNote[sec.NoteID] {
			seenNote[sec.NoteID] = true
			noteIDs = append(noteIDs, sec.NoteID)
		}
	}

	// One query for every note in the outline, rather than one per section.
	notes, err := NotesByIDs(studyDB, noteIDs)
	if err != nil {
		return res, err
	}
	res.notes = notes

	for _, sec := range sections {
		if sec.Kind != KindPassage {
			continue
		}
		if _, done := res.verses[sec.Ref]; done {
			continue
		}

		verses, err := fetchPassage(bibleDB, translation, sec.Ref)
		if err != nil {
			// A scripture read that fails is recorded as "no text" and marked in
			// the document, for the same reason a deleted note is: the rest of
			// the outline is still worth assembling.
			res.verses[sec.Ref] = nil
			continue
		}
		if len(verses) > maxInlinedVerses {
			verses = verses[:maxInlinedVerses]
			res.truncated[sec.Ref] = true
		}
		res.verses[sec.Ref] = verses
	}

	return res, nil
}

// fetchPassage reads the verses a reference covers: the whole chapter when the
// reference names one, otherwise the range.
func fetchPassage(bibleDB *sql.DB, translation string, ref bible.Ref) ([]bible.Verse, error) {
	if ref.IsChapter() {
		return bible.Chapter(bibleDB, translation, ref.BookNum, ref.Chapter)
	}
	return bible.VerseRange(bibleDB, translation,
		ref.BookNum, ref.Chapter, ref.VerseStart, ref.VerseEnd)
}

// formatOutline writes the document. Pure: everything it needs is in sources.
func formatOutline(sermon Sermon, src outlineSources) string {
	var b strings.Builder

	b.WriteString("# ")
	b.WriteString(strings.TrimSpace(sermon.Title))
	b.WriteString("\n")

	if len(sermon.Outline) == 0 {
		b.WriteString("\n*This outline is empty.*\n")
		return b.String()
	}

	// Points are the one kind that groups: consecutive points become one
	// Markdown list, and a blank line between them would split it into several.
	// prevWasPoint is what tracks that boundary.
	prevWasPoint := false

	for _, sec := range sermon.Outline {
		isPoint := sec.Kind == KindPoint
		if !(isPoint && prevWasPoint) {
			b.WriteString("\n")
		}
		prevWasPoint = isPoint

		switch sec.Kind {
		case KindHeading:
			b.WriteString("## ")
			b.WriteString(orPlaceholder(sec.Text, "Untitled section"))
			b.WriteString("\n")

		case KindPoint:
			b.WriteString("- ")
			b.WriteString(orPlaceholder(sec.Text, "…"))
			b.WriteString("\n")

		case KindPassage:
			writePassage(&b, sec.Ref, src)

		case KindNote:
			writeNote(&b, sec.NoteID, src)

		default:
			// An unknown kind can only arrive from a newer version of pbstudy
			// through a sync file. Say so rather than dropping the section
			// silently — a section that vanishes from an export looks like data
			// loss, and this way the outline still accounts for it.
			b.WriteString("*Unrecognized outline section (")
			b.WriteString(sec.Kind)
			b.WriteString(").*\n")
		}
	}

	return b.String()
}

// writePassage renders scripture as a blockquote headed by its citation.
//
// A blockquote rather than plain text: in the assembled document the preacher
// needs to see at a glance which words are scripture and which are theirs, and
// that distinction survives into every downstream format — the HTML export
// indents it, and the AI drafter is told the quoted spans are the text to
// preach from rather than to paraphrase.
func writePassage(b *strings.Builder, ref bible.Ref, src outlineSources) {
	b.WriteString("> **")
	b.WriteString(ref.String())
	b.WriteString("** (")
	b.WriteString(strings.ToUpper(src.translation))
	b.WriteString(")\n>\n")

	verses := src.verses[ref]
	if len(verses) == 0 {
		b.WriteString("> *No ")
		b.WriteString(strings.ToUpper(src.translation))
		b.WriteString(" text is cached for this passage.*\n")
		return
	}

	b.WriteString("> ")
	for i, v := range verses {
		if i > 0 {
			b.WriteString(" ")
		}
		// Number every verse when there is more than one. A single-verse
		// citation already names its verse in the heading above, so repeating
		// it there is noise.
		if len(verses) > 1 {
			b.WriteString("**")
			b.WriteString(strconv.Itoa(v.Num))
			b.WriteString("** ")
		}
		b.WriteString(strings.TrimSpace(v.Body))
	}
	b.WriteString("\n")

	if src.truncated[ref] {
		b.WriteString(">\n> *(Only the first ")
		b.WriteString(strconv.Itoa(maxInlinedVerses))
		b.WriteString(" verses of this passage are included.)*\n")
	}
}

// writeNote inlines one of the user's notes: its title as a sub-heading, the
// passages it is anchored to, then its body verbatim.
//
// The body is written unchanged rather than stripped of markup. It is already
// Markdown, this document is Markdown, and the note's own formatting — its
// lists, its emphasis — is part of what the preacher wrote.
func writeNote(b *strings.Builder, noteID string, src outlineSources) {
	note, ok := src.notes[noteID]
	if !ok {
		b.WriteString("*A note referenced here is no longer available.*\n")
		return
	}

	b.WriteString("### ")
	b.WriteString(orPlaceholder(note.Title, "Untitled note"))
	b.WriteString("\n")

	if len(note.Refs) > 0 {
		refs := make([]bible.Ref, 0, len(note.Refs))
		for _, r := range note.Refs {
			refs = append(refs, r.Ref)
		}
		b.WriteString("\n*")
		b.WriteString(bible.FormatRefList(refs))
		b.WriteString("*\n")
	}

	body := strings.TrimSpace(note.BodyMD)
	if body == "" {
		b.WriteString("\n*(This note has no body.)*\n")
		return
	}
	b.WriteString("\n")
	b.WriteString(body)
	b.WriteString("\n")
}

func orPlaceholder(s, placeholder string) string {
	if s = strings.TrimSpace(s); s != "" {
		return s
	}
	return placeholder
}

// DescribeSection renders a one-line label for a section, for the builder's
// outline table and for anywhere a section needs naming outside the document.
func DescribeSection(sec Section) string {
	switch sec.Kind {
	case KindHeading:
		return orPlaceholder(sec.Text, "Untitled section")
	case KindPoint:
		return orPlaceholder(sec.Text, "(empty point)")
	case KindPassage:
		return sec.Ref.String()
	case KindNote:
		return "" // the view resolves the note's title; it has the note in hand
	default:
		return sec.Kind
	}
}

// ValidateSection checks a section built from form input and returns the
// message to show the user, or "" when it is usable.
func ValidateSection(sec Section) error {
	switch sec.Kind {
	case KindHeading, KindPoint:
		if strings.TrimSpace(sec.Text) == "" {
			return serr.New("that section needs some text")
		}
	case KindPassage:
		if sec.Ref.BookNum < 1 || sec.Ref.BookNum > len(bible.Books) || sec.Ref.Chapter < 1 {
			return serr.New("that is not a passage I can look up")
		}
	case KindNote:
		if sec.NoteID == "" {
			return serr.New("choose a note to add")
		}
	default:
		return serr.New("unknown section kind", "kind", sec.Kind)
	}
	return nil
}
