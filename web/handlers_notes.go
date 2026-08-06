package web

import (
	"net/http"
	"strings"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/study"
	"github.com/rohanthewiz/pbstudy/web/ui"
)

// handleNotes renders the notes index.
func (s *Server) handleNotes(ctx rweb.Context) error {
	notes, err := study.ListNotes(s.store.Study, 0)
	if err != nil {
		return s.studyUnavailable(ctx, err)
	}

	return s.render(ctx, ui.Page{
		Title:       "Notes",
		Active:      ui.NavNotes,
		Translation: s.defaultTranslation(),
		Body: ui.NotesList{
			Notes:       notes,
			Translation: s.defaultTranslation(),
		},
	})
}

// handleNoteNew renders an empty editor.
//
// ?refs= pre-fills the reference field, which is how the verse hub's "Full
// editor" link carries the passage across without a session or a draft record.
func (s *Server) handleNoteNew(ctx rweb.Context) error {
	return s.renderNoteEditor(ctx, ui.NoteEditor{
		Action:    "/notes",
		Heading:   "New note",
		RefsField: ctx.Request().QueryParam("refs"),
		CancelURL: "/notes",
	})
}

// handleNoteCreate saves a new note.
//
// Serves both entrances — the full editor and the verse hub's quick-add — so
// there is exactly one place that turns a form into a note. The two differ
// only in which fields they send and where they ask to be sent afterwards.
func (s *Server) handleNoteCreate(ctx rweb.Context) error {
	draft, problem := s.readNoteForm(ctx)
	if problem != "" {
		// Re-render the editor with what was typed rather than discarding it.
		// A verse-hub quick-add lands here too: the full editor is a better
		// place to fix a bad reference than a collapsed disclosure.
		return s.renderNoteEditor(ctx, ui.NoteEditor{
			Action:    "/notes",
			Heading:   "New note",
			Title:     ctx.Request().FormValue("title"),
			RefsField: ctx.Request().FormValue("refs"),
			TagsField: ctx.Request().FormValue("tags"),
			BodyMD:    ctx.Request().FormValue("body"),
			CancelURL: "/notes",
			Problem:   problem,
		})
	}

	id, err := study.CreateNote(s.store.Study, draft)
	if err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot save note", "The study database could not be written.", err)
	}

	return ctx.Redirect(http.StatusSeeOther,
		safeReturn(ctx.Request().FormValue("return"), ui.NoteURL(id)))
}

// handleNoteShow renders one note beside the scripture it is anchored to.
func (s *Server) handleNoteShow(ctx rweb.Context) error {
	id := ctx.Request().Param("id")

	note, err := study.GetNote(s.store.Study, id)
	if err != nil {
		return s.notFound(ctx, "That note does not exist, or has been deleted.")
	}

	bodyHTML, err := study.RenderMarkdown(note.BodyMD)
	if err != nil {
		// A Markdown failure must not hide the note. The record itself is
		// intact and its anchors still render; losing the formatted body is a
		// far better outcome than an error page over the whole thing.
		logger.LogErr(err, "cannot render note markdown", "note", id)
		bodyHTML = ""
	}

	translation := s.defaultTranslation()

	return s.render(ctx, ui.Page{
		Title:       note.Title,
		Active:      ui.NavNotes,
		Translation: translation,
		Body: ui.NoteDetail{
			Note:        note,
			BodyHTML:    bodyHTML,
			Anchors:     s.resolveAnchors(note.Refs, translation),
			Translation: translation,
		},
	})
}

// handleNoteEdit renders the editor populated from the stored note.
func (s *Server) handleNoteEdit(ctx rweb.Context) error {
	id := ctx.Request().Param("id")

	note, err := study.GetNote(s.store.Study, id)
	if err != nil {
		return s.notFound(ctx, "That note does not exist, or has been deleted.")
	}

	return s.renderNoteEditor(ctx, ui.NoteEditor{
		Action:     ui.NoteURL(id),
		Heading:    "Edit note",
		Title:      note.Title,
		RefsField:  bible.FormatRefList(refsOf(note)),
		TagsField:  tagNamesOf(note),
		BodyMD:     note.BodyMD,
		CancelURL:  ui.NoteURL(id),
		IsExisting: true,
	})
}

// handleNoteUpdate saves an edit.
func (s *Server) handleNoteUpdate(ctx rweb.Context) error {
	id := ctx.Request().Param("id")

	draft, problem := s.readNoteForm(ctx)
	if problem != "" {
		return s.renderNoteEditor(ctx, ui.NoteEditor{
			Action:     ui.NoteURL(id),
			Heading:    "Edit note",
			Title:      ctx.Request().FormValue("title"),
			RefsField:  ctx.Request().FormValue("refs"),
			TagsField:  ctx.Request().FormValue("tags"),
			BodyMD:     ctx.Request().FormValue("body"),
			CancelURL:  ui.NoteURL(id),
			Problem:    problem,
			IsExisting: true,
		})
	}

	if err := study.UpdateNote(s.store.Study, id, draft); err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot save note", "The study database could not be written.", err)
	}

	return ctx.Redirect(http.StatusSeeOther, ui.NoteURL(id))
}

// handleNoteDelete tombstones a note and returns to the index.
func (s *Server) handleNoteDelete(ctx rweb.Context) error {
	id := ctx.Request().Param("id")

	if err := study.DeleteNote(s.store.Study, id); err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot delete note", "The study database could not be written.", err)
	}
	return ctx.Redirect(http.StatusSeeOther,
		safeReturn(ctx.Request().FormValue("return"), "/notes"))
}

// --- shared plumbing -------------------------------------------------------

// readNoteForm turns a submitted form into a draft, or returns the message to
// show the user.
//
// A bad reference is the only rejection. Everything else has a sane default:
// no title is derived from the body, no tags is no tags, and an empty body is
// a legitimate placeholder note the user intends to fill in later.
func (s *Server) readNoteForm(ctx rweb.Context) (study.NoteDraft, string) {
	req := ctx.Request()

	refs, rejected := bible.ParseRefList(req.FormValue("refs"))
	if len(rejected) > 0 {
		return study.NoteDraft{}, "Could not read " +
			ui.Plural(len(rejected), "reference") + ": " + strings.Join(rejected, ", ") +
			". Try the form \"John 3:16-18\"."
	}

	return study.NoteDraft{
		Title:    req.FormValue("title"),
		BodyMD:   req.FormValue("body"),
		Refs:     refs,
		TagNames: study.SplitTagNames(req.FormValue("tags")),
	}, ""
}

// renderNoteEditor renders the editor with the known tags attached, so every
// entrance to the form offers the same autocomplete.
func (s *Server) renderNoteEditor(ctx rweb.Context, ed ui.NoteEditor) error {
	// A failure here costs autocomplete, not the page.
	ed.KnownTags, _ = study.ListTags(s.store.Study)

	return s.render(ctx, ui.Page{
		Title:       ed.Heading,
		Active:      ui.NavNotes,
		Translation: s.defaultTranslation(),
		Body:        ed,
	})
}

// resolveAnchors pairs each of a note's references with the scripture it
// points at, so the note page shows the passage rather than just naming it.
//
// A missing translation yields an empty verse list rather than an error: the
// note is still worth reading with its anchors listed, and the view says so.
func (s *Server) resolveAnchors(refs []study.NoteRef, translation string) []ui.Anchor {
	out := make([]ui.Anchor, 0, len(refs))

	for _, r := range refs {
		a := ui.Anchor{Ref: r.Ref}

		if r.Ref.IsChapter() {
			// A whole-chapter anchor deliberately does NOT inline the chapter.
			// Forty verses inside a note page is the note's content, not its
			// context; the reference chip links to the reader instead.
			out = append(out, a)
			continue
		}

		verses, err := bible.VerseRange(s.store.Bible, translation,
			r.Ref.BookNum, r.Ref.Chapter, r.Ref.VerseStart, r.Ref.VerseEnd)
		if err == nil {
			a.Verses = verses
		}
		out = append(out, a)
	}
	return out
}

// refsOf extracts the plain references from a note's anchors, for the editor's
// reference field.
func refsOf(n study.Note) []bible.Ref {
	out := make([]bible.Ref, 0, len(n.Refs))
	for _, r := range n.Refs {
		out = append(out, r.Ref)
	}
	return out
}

// tagNamesOf renders a note's tags back into the comma-separated form the
// editor's tag field accepts.
func tagNamesOf(n study.Note) string {
	names := make([]string, 0, len(n.Tags))
	for _, t := range n.Tags {
		names = append(names, t.Name)
	}
	return strings.Join(names, ", ")
}
