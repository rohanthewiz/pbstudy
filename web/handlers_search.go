package web

import (
	"net/http"
	"strings"

	"github.com/rohanthewiz/rweb"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/study"
	"github.com/rohanthewiz/pbstudy/web/ui"
)

// scriptureSearchLimit caps a scripture result group.
//
// Higher than the study-side cap because a common word genuinely occurs
// hundreds of times in scripture and skimming that list is a legitimate way to
// study a word, whereas a personal notes database with 100 hits for one query
// is telling you to ask a narrower question.
const scriptureSearchLimit = 200

// handleSearch serves the combined search page.
//
// Three behaviours in one route, which is what makes the header box feel right:
//
//	"John 3:16"  parses as a reference -> jump to the verse hub
//	"love"       does not parse        -> ILIKE scan of scripture and study data
//	scope=notes  narrows the second    -> and changes what the first one means
//
// # The one rule worth stating about the fast-path
//
// A reference query jumps — except when the scope is "my notes". Someone who
// has explicitly narrowed to their own writing and then types "John 3:16" is
// asking "what have I written about this verse", not "take me there". So under
// that scope the reference is resolved into an anchor lookup instead
// (NotesForVerse / NotesForChapter) and the page offers the jump as a link.
//
// The reference test itself is deliberately strict (see bible.ParseRef): a
// query that is *almost* a reference should fall through to a text search
// rather than teleport the user somewhere surprising.
func (s *Server) handleSearch(ctx rweb.Context) error {
	req := ctx.Request()
	q := strings.TrimSpace(req.QueryParam("q"))
	scope := ui.NormalizeScope(req.QueryParam("scope"))

	// Validated, not merely defaulted: this value is reflected into the page
	// and into every result link, and it arrives from a query string.
	translation := req.QueryParam("translation")
	if !bible.IsKnownTranslation(translation) {
		translation = s.defaultTranslation()
	}

	// The version picker is only meaningful over what is actually cached.
	downloaded, err := bible.Downloaded(s.store.Bible)
	if err != nil {
		downloaded = nil
	}

	page := ui.Search{
		Query:        q,
		Scope:        scope,
		Translation:  translation,
		Translations: downloaded,
		VerseLimit:   scriptureSearchLimit,
		NoteLimit:    study.DefaultSearchLimit,
	}

	if q == "" {
		return s.renderSearch(ctx, page)
	}

	if ref, ok := bible.LooksLikeRef(q); ok {
		if scope != ui.ScopeNotes {
			// A whole-chapter reference goes to the reader; a verse reference
			// goes to the verse hub.
			if ref.IsChapter() {
				return ctx.Redirect(http.StatusFound,
					ui.ReadURL(translation, ref.BookNum, ref.Chapter))
			}
			return ctx.Redirect(http.StatusFound,
				ui.VerseURL(ref.BookNum, ref.Chapter, ref.VerseStart)+"?t="+translation)
		}

		notes, err := s.notesAnchoredTo(ref)
		if err != nil {
			return s.studyUnavailable(ctx, err)
		}
		page.AnchorRef = &ref
		page.Notes = study.NoteHits(notes, "")
		// These came from an anchor lookup, not a text query, so nothing was
		// capped and a "showing the first N" notice would be a lie.
		page.NoteLimit = 0
		return s.renderSearch(ctx, page)
	}

	if page.Wants(ui.ScopeScripture) {
		results, err := bible.SearchText(s.store.Bible, translation, q, scriptureSearchLimit)
		if err != nil {
			return s.renderError(ctx, http.StatusInternalServerError,
				"Search failed", "The scripture database could not be queried.", err)
		}
		page.Verses = results
	}

	if page.Wants(ui.ScopeNotes) {
		if err := s.searchStudy(&page, q); err != nil {
			return s.studyUnavailable(ctx, err)
		}
	}

	return s.renderSearch(ctx, page)
}

// searchStudy fills in the study-database half of the results.
//
// A failure here is fatal to the page rather than degraded, unlike the reader's
// indicator dots. The difference is what the page is for: a reader that loses
// its dots still shows scripture, but a search that quietly drops the notes
// group answers "where does this come up?" with a confident, wrong "nowhere".
func (s *Server) searchStudy(page *ui.Search, q string) error {
	notes, err := study.SearchNotes(s.store.Study, q, study.DefaultSearchLimit)
	if err != nil {
		return err
	}
	tags, err := study.SearchTags(s.store.Study, q)
	if err != nil {
		return err
	}
	xrefs, err := study.SearchXrefs(s.store.Study, q, study.DefaultSearchLimit)
	if err != nil {
		return err
	}

	page.Notes, page.Tags, page.Xrefs = notes, tags, xrefs
	return nil
}

// notesAnchoredTo resolves a reference into the notes attached to it.
//
// A chapter reference ("John 3") collects the notes on the chapter as a whole;
// a verse reference collects the notes whose range covers that verse. The two
// are separate queries because a chapter-level anchor is stored as verse 0 and
// deliberately does not match any specific verse — see study.NotesForVerse.
func (s *Server) notesAnchoredTo(ref bible.Ref) ([]study.Note, error) {
	if ref.IsChapter() {
		return study.NotesForChapter(s.store.Study, ref.BookNum, ref.Chapter)
	}
	return study.NotesForVerse(s.store.Study, ref.BookNum, ref.Chapter, ref.VerseStart)
}

// renderSearch is the single exit for every search render, so the page title
// and nav state cannot drift between the empty, jumped and matched paths.
func (s *Server) renderSearch(ctx rweb.Context, page ui.Search) error {
	title := "Search"
	if page.Query != "" {
		title = "Search: " + page.Query
	}

	return s.render(ctx, ui.Page{
		Title:       title,
		Active:      ui.NavSearch,
		Translation: page.Translation,
		Body:        page,
	})
}
