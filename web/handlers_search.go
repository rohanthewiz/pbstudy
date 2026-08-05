package web

import (
	"net/http"
	"strings"

	"github.com/rohanthewiz/rweb"

	"pbstudy/bible"
	"pbstudy/web/ui"
)

// handleSearch serves the search page.
//
// Two behaviours in one route, which is what makes the header box feel right:
//
//	"John 3:16"  parses as a reference -> redirect straight to the verse hub
//	"love"       does not parse        -> ILIKE scan of the scripture text
//
// The reference fast-path is checked first and deliberately strict (see
// bible.ParseRef): a query that is *almost* a reference should fall through
// to a text search rather than teleport the user somewhere surprising.
//
// Scope is scripture-only for now. Notes and the combined view join once the
// study database has content to search.
func (s *Server) handleSearch(ctx rweb.Context) error {
	req := ctx.Request()
	q := strings.TrimSpace(req.QueryParam("q"))

	translation := req.QueryParam("translation")
	if translation == "" {
		translation = s.defaultTranslation()
	}

	if q == "" {
		return s.render(ctx, ui.Page{
			Title:       "Search",
			Active:      ui.NavSearch,
			Translation: translation,
			Body:        ui.Search{Translation: translation},
		})
	}

	// Reference fast-path. A whole-chapter reference goes to the reader; a
	// verse reference goes to the verse hub.
	if ref, ok := bible.LooksLikeRef(q); ok {
		if ref.IsChapter() {
			return ctx.Redirect(http.StatusFound,
				ui.ReadURL(translation, ref.BookNum, ref.Chapter))
		}
		return ctx.Redirect(http.StatusFound,
			ui.VerseURL(ref.BookNum, ref.Chapter, ref.VerseStart)+"?t="+translation)
	}

	results, err := bible.SearchText(s.store.Bible, translation, q, 0)
	if err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Search failed", "The scripture database could not be queried.", err)
	}

	return s.render(ctx, ui.Page{
		Title:       "Search: " + q,
		Active:      ui.NavSearch,
		Translation: translation,
		Body: ui.Search{
			Query:       q,
			Translation: translation,
			Results:     results,
		},
	})
}
