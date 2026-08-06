package web

import (
	"net/http"

	"github.com/rohanthewiz/rweb"

	"github.com/rohanthewiz/pbstudy/study"
	"github.com/rohanthewiz/pbstudy/web/ui"
)

// handleTags renders the tag index.
func (s *Server) handleTags(ctx rweb.Context) error {
	tags, err := study.ListTags(s.store.Study)
	if err != nil {
		return s.studyUnavailable(ctx, err)
	}

	return s.render(ctx, ui.Page{
		Title:       "Tags",
		Active:      ui.NavTags,
		Translation: s.defaultTranslation(),
		Body:        ui.TagsList{Tags: tags},
	})
}

// handleTagShow renders one tag's topical study: every note carrying it.
//
// This is the payoff for tagging at all — notes written weeks apart against
// unrelated passages collected under the topic they share.
func (s *Server) handleTagShow(ctx rweb.Context) error {
	id := ctx.Request().Param("id")

	tag, err := study.GetTag(s.store.Study, id)
	if err != nil {
		return s.notFound(ctx, "That tag does not exist, or has been deleted.")
	}

	notes, err := study.NotesForTag(s.store.Study, id)
	if err != nil {
		return s.studyUnavailable(ctx, err)
	}

	translation := s.defaultTranslation()

	return s.render(ctx, ui.Page{
		Title:       tag.Name,
		Active:      ui.NavTags,
		Translation: translation,
		Body: ui.TagPage{
			Tag:   tag,
			Notes: notes,
			// Derived from the notes already in hand — no second query, and no
			// chance of the passage list disagreeing with the notes below it.
			Passages:    study.RefsAcross(notes),
			Translation: translation,
		},
	})
}

// handleTagDescrip saves a tag's description.
func (s *Server) handleTagDescrip(ctx rweb.Context) error {
	id := ctx.Request().Param("id")

	if err := study.UpdateTagDescrip(s.store.Study, id, ctx.Request().FormValue("descrip")); err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot save tag", "The study database could not be written.", err)
	}
	return ctx.Redirect(http.StatusSeeOther, ui.TagURL(id))
}

// handleTagDelete tombstones a tag and unlinks it from its notes.
func (s *Server) handleTagDelete(ctx rweb.Context) error {
	id := ctx.Request().Param("id")

	if err := study.DeleteTag(s.store.Study, id); err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot delete tag", "The study database could not be written.", err)
	}
	return ctx.Redirect(http.StatusSeeOther, "/tags")
}
