package ui

import (
	"github.com/rohanthewiz/element"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/blb"
	"github.com/rohanthewiz/pbstudy/study"
)

// ChapterLink is a prev/next target, precomputed by the handler so the view
// does no database work.
type ChapterLink struct {
	URL   string
	Label string
}

// Reader renders one chapter of scripture.
type Reader struct {
	Translation  string
	Translations []bible.Translation
	Book         *bible.Book
	Chapter      int
	Verses       []bible.Verse
	Prev         ChapterLink
	Next         ChapterLink
	// FocusVerse, when non-zero, is scrolled to via the URL fragment.
	FocusVerse int
	// Marks is what the user has attached to each verse of this chapter,
	// keyed by verse number, with study.ChapterVerseKey holding anything
	// attached to the chapter as a whole. Precomputed by the handler in one
	// query so rendering a verse costs a map lookup, not a round trip.
	Marks map[int]study.Marks
	// ChapterNotes are notes anchored to the whole chapter rather than to a
	// verse in it. They have nowhere to hang in the verse flow, so they get
	// their own card above the text.
	ChapterNotes []study.Note
}

func (r Reader) Render(b *element.Builder) (x any) {
	b.DivClass("reader-bar").R(
		r.renderPicker(b),
		r.renderChapterNav(b),
	)

	if len(r.Verses) == 0 {
		b.DivClass("notice").R(
			b.Strong().T("Nothing here yet. "),
			b.T("The "),
			b.Strong().T(r.Translation),
			b.T(" translation has no text cached for this chapter. Run "),
			b.Code().F("pbstudy download %s", r.Translation),
			b.T(" to fetch it."),
		)
		return
	}

	r.renderChapterNotes(b)

	// The scripture container is excluded from ScriptTagger. We already
	// render every verse with its own number, anchor, and BLB link; letting
	// the tagger re-scan this text would double-link the whole chapter.
	// Note bodies elsewhere on the page stay taggable, which is the point.
	b.Div("class", "scripture "+blb.NoTagClass).R(
		element.ForEach(r.Verses, func(v bible.Verse) {
			// The ?t= carries the translation being read into the hub, so
			// its "back to chapter" link and BLB deep links return here
			// rather than to whatever translation happens to sort first.
			b.SpanClass("verse", "id", "v"+itoa(v.Num)).R(
				b.AClass("verse-num",
					"href", VerseURL(v.BookNum, v.Chapter, v.Num)+"?t="+r.Translation,
					"title", "Open verse hub").T(itoa(v.Num)),
				b.T(esc(v.Body)),
				r.renderMarks(b, v.Num),
			)
		}),
	)

	b.DivClass("link-row").R(
		b.A("class", "btn", "href",
			blb.ChapterURL(r.Translation, r.Book.Num, r.Chapter),
			"target", "_blank", "rel", "noopener").
			F("This chapter on Blue Letter Bible"),
	)
	return
}

// renderMarks draws the indicator that a verse has notes or cross-references
// attached.
//
// It is a link to the verse hub rather than a decoration, because "I see there
// is something here" and "show me what is here" are the same impulse one
// motion apart. The glyphs are text (a pencil and a link) rather than images
// or CSS shapes so they inherit the reading colour and scale with the text.
func (r Reader) renderMarks(b *element.Builder, verse int) (x any) {
	m := r.Marks[verse]
	if !m.Any() {
		return
	}

	label := ""
	glyph := ""
	if m.Notes > 0 {
		glyph += "✎"
		label = Plural(m.Notes, "note")
	}
	if m.Xrefs > 0 {
		glyph += "⁂"
		if label != "" {
			label += ", "
		}
		label += Plural(m.Xrefs, "cross-reference")
	}

	b.AClass("verse-mark",
		"href", VerseURL(r.Book.Num, r.Chapter, verse)+"?t="+r.Translation,
		"title", esc(label)).T(glyph)
	return
}

// renderChapterNotes shows notes anchored to the chapter itself.
//
// Rendered before the scripture rather than after: a chapter-level note is
// usually context or an outline the user wrote to read the chapter *with*, so
// burying it below 40 verses would defeat the purpose.
func (r Reader) renderChapterNotes(b *element.Builder) (x any) {
	if len(r.ChapterNotes) == 0 {
		return
	}
	b.DivClass("card").R(
		b.H2().F("Notes on %s %d", esc(r.Book.Name), r.Chapter),
		element.ForEach(r.ChapterNotes, func(n study.Note) {
			renderNoteSummary(b, n)
		}),
	)
	return
}

// renderPicker is a plain GET form: book, chapter, translation, submit.
// Round-tripping through the server keeps every reader state a real URL, and
// means the picker works with JavaScript off (app.js only repopulates the
// chapter list live).
func (r Reader) renderPicker(b *element.Builder) (x any) {
	b.Form("method", "get", "action", "/read/go").R(
		b.Select("name", "book", "data-book-select", "1", "aria-label", "Book").R(
			element.ForEach(bible.Books, func(bk bible.Book) {
				attrs := []string{"value", itoa(bk.Num), "data-chapters", itoa(bk.ChapterCount)}
				if bk.Num == r.Book.Num {
					attrs = append(attrs, "selected", "selected")
				}
				b.Option(attrs...).T(bk.Name)
			}),
		),

		b.Select("name", "chapter", "data-chapter-select", "1", "aria-label", "Chapter").R(
			b.Wrap(func() {
				for c := 1; c <= r.Book.ChapterCount; c++ {
					attrs := []string{"value", itoa(c)}
					if c == r.Chapter {
						attrs = append(attrs, "selected", "selected")
					}
					b.Option(attrs...).T(itoa(c))
				}
			}),
		),

		b.Wrap(func() {
			// Only offer a translation switcher once more than one is
			// downloaded; a select with a single option is just noise.
			if len(r.Translations) < 2 {
				b.Input("type", "hidden", "name", "translation", "value", r.Translation)
				return
			}
			b.Select("name", "translation", "aria-label", "Translation").R(
				element.ForEach(r.Translations, func(t bible.Translation) {
					attrs := []string{"value", t.Abbrev, "title", t.Name}
					if t.Abbrev == r.Translation {
						attrs = append(attrs, "selected", "selected")
					}
					b.Option(attrs...).T(upper(t.Abbrev))
				}),
			)
		}),

		b.Button("type", "submit").T("Go"),
	)
	return
}

func (r Reader) renderChapterNav(b *element.Builder) (x any) {
	b.DivClass("chapter-nav").R(
		b.Wrap(func() {
			// data-nav-prev / data-nav-next are what app.js binds the arrow
			// keys to; absent links simply disable the shortcut.
			if r.Prev.URL != "" {
				b.A("class", "btn", "href", r.Prev.URL, "data-nav-prev", "1",
					"title", r.Prev.Label).T("← Prev")
			}
			if r.Next.URL != "" {
				b.A("class", "btn", "href", r.Next.URL, "data-nav-next", "1",
					"title", r.Next.Label).T("Next →")
			}
		}),
	)
	return
}

// upper uppercases an ASCII translation abbrev without pulling in strings.
func upper(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 32
		}
	}
	return string(out)
}
