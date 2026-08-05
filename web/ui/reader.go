package ui

import (
	"github.com/rohanthewiz/element"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/blb"
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
				b.T(v.Body),
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
