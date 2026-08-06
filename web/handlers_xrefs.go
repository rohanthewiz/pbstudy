package web

import (
	"net/http"
	"strings"

	"github.com/rohanthewiz/rweb"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/study"
)

// handleXrefCreate records a correlation between two passages.
//
// Both ends arrive as written references rather than as numbers, so the same
// parser that powers the search box powers this form: "Genesis 3:15",
// "gen 3.15" and "Gn 3v15" all land in the same place.
func (s *Server) handleXrefCreate(ctx rweb.Context) error {
	req := ctx.Request()
	back := safeReturn(req.FormValue("return"), "/")

	from, err := bible.ParseRef(req.FormValue("from"))
	if err != nil {
		return s.renderError(ctx, http.StatusBadRequest, "Cannot link that verse",
			"The source reference could not be read.", err)
	}
	// The source is always a single verse — the form fills it from the hub's
	// own location, so a chapter here means a hand-edited request.
	if from.IsChapter() {
		return s.renderError(ctx, http.StatusBadRequest, "Cannot link that verse",
			"A cross-reference has to start at a specific verse.", nil)
	}

	to, err := bible.ParseRef(req.FormValue("to"))
	if err != nil {
		// A typo in the target is the common case, and the user is standing on
		// the verse hub. Send them back there with the complaint rather than
		// to an error page that loses their place.
		return s.verseHubWithProblem(ctx, from, back,
			"Could not read \""+req.FormValue("to")+"\" as a reference. "+
				"Try the form \"Genesis 3:15\".")
	}

	if _, err := study.CreateXref(s.store.Study, from, to, req.FormValue("comment")); err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot save cross-reference", "The study database could not be written.", err)
	}

	return ctx.Redirect(http.StatusSeeOther, back)
}

// handleXrefDelete tombstones a cross-reference and returns where it came from.
func (s *Server) handleXrefDelete(ctx rweb.Context) error {
	id := ctx.Request().Param("id")

	if err := study.DeleteXref(s.store.Study, id); err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot delete cross-reference", "The study database could not be written.", err)
	}
	return ctx.Redirect(http.StatusSeeOther,
		safeReturn(ctx.Request().FormValue("return"), "/"))
}

// verseHubWithProblem re-renders the verse hub with a complaint above the
// forms, for the case where a redirect would lose the message.
//
// Rendering rather than redirecting keeps the app free of flash-message state:
// there is no session, no cookie, and nothing to expire.
func (s *Server) verseHubWithProblem(ctx rweb.Context, at bible.Ref, back, problem string) error {
	bk, err := bible.ByNum(at.BookNum)
	if err != nil {
		return s.notFound(ctx, "There is no such book.")
	}

	ctx.SetStatus(http.StatusBadRequest)
	return s.renderVerseHub(ctx, bk, at.Chapter, at.VerseStart,
		translationFromURL(back, s.defaultTranslation()), problem)
}

// translationFromURL recovers the ?t= value from a return URL so the
// re-rendered hub stays in the translation the user was reading.
//
// A deliberately narrow parse rather than net/url: the only URLs reaching this
// are the ones our own views built, and the result is validated against the
// known translations before it is trusted with anything.
func translationFromURL(raw, fallback string) string {
	if i := strings.Index(raw, "?t="); i >= 0 {
		if t := raw[i+3:]; bible.IsKnownTranslation(t) {
			return t
		}
	}
	return fallback
}
