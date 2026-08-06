package ui

import (
	"strings"

	"github.com/rohanthewiz/element"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/blb"
)

// Search renders scripture text-search results.
//
// Results are rendered with the matched substring wrapped in <mark>, which is
// the one place this app builds HTML from user input — see highlight() for
// how that is kept safe.
type Search struct {
	Query       string
	Translation string
	Results     []bible.SearchResult
}

func (s Search) Render(b *element.Builder) (x any) {
	b.H1Class("page-title").T("Search")

	if s.Query == "" {
		b.PClass("page-sub").R(
			b.T("Search the text of "),
			b.Strong().T(upper(s.Translation)),
			b.T(", or type a reference like "),
			b.Code().T("John 3:16"),
			b.T(" to jump straight to it."),
		)
		return
	}

	// The query is echoed back to the user, so it is escaped like any other
	// text that came in over the wire.
	b.PClass("page-sub").F("%d result(s) for %q in %s",
		len(s.Results), esc(s.Query), upper(s.Translation))

	if len(s.Results) == 0 {
		b.PClass("empty").T("No verses matched. Try a shorter phrase.")
		return
	}

	// Results carry our own reference links; ScriptTagger stays out.
	b.Div("class", "card "+blb.NoTagClass).R(
		element.ForEach(s.Results, func(r bible.SearchResult) {
			b.DivClass("translation-row").R(
				b.DivClass("translation-tag").R(
					b.A("href", VerseURL(r.BookNum, r.Chapter, r.Num)+"?t="+s.Translation).
						T(shortRef(r.Ref)),
				),
				b.DivClass("translation-body").R(
					highlight(b, r.Body, s.Query),
				),
			)
		}),
	)
	return
}

// shortRef renders "Jn 3:16"-ish labels for the results gutter, using the BLB
// abbreviation since it is the most compact form already in the Book table.
func shortRef(ref bible.Ref) string {
	bk := ref.Book()
	return upper(bk.BLBAbbrev[:1]) + bk.BLBAbbrev[1:] + " " +
		itoa(ref.Chapter) + ":" + itoa(ref.VerseStart)
}

// highlight emits body with every case-insensitive occurrence of q wrapped in
// <mark>.
//
// Two properties worth stating, because both are easy to get wrong:
//
//  1. Matching happens on the raw strings, but every fragment is escaped on
//     its way out (see esc). Escaping AFTER the match is what keeps the
//     offsets honest: escaping first would turn a searched-for "&" into
//     "&amp;" and the index arithmetic below would slice through the middle of
//     an entity. A query of "<script>" therefore highlights literally.
//
//  2. Matching is done on lowercased copies, then sliced out of the ORIGINAL
//     body so the match keeps its real casing. That only works while
//     lowercasing preserves byte offsets, which holds for ASCII but not for
//     every Unicode input. The guard below falls back to unhighlighted text
//     rather than slicing at a wrong offset and corrupting the output.
func highlight(b *element.Builder, body, q string) (x any) {
	lowerBody := strings.ToLower(body)
	lowerQ := strings.ToLower(q)

	if q == "" || len(lowerBody) != len(body) || len(lowerQ) != len(q) {
		b.T(esc(body))
		return
	}

	for {
		i := strings.Index(lowerBody, lowerQ)
		if i < 0 {
			b.T(esc(body))
			return
		}
		if i > 0 {
			b.T(esc(body[:i]))
		}
		b.Ele("mark").T(esc(body[i : i+len(q)]))

		body = body[i+len(q):]
		lowerBody = lowerBody[i+len(q):]
	}
}
