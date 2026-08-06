package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/study"
	"github.com/rohanthewiz/pbstudy/web/ui"
)

// handleSermons renders the sermons index.
func (s *Server) handleSermons(ctx rweb.Context) error {
	sermons, err := study.ListSermons(s.store.Study, 0)
	if err != nil {
		return s.studyUnavailable(ctx, err)
	}

	return s.render(ctx, ui.Page{
		Title:       "Sermons",
		Active:      ui.NavSermons,
		Translation: s.defaultTranslation(),
		Body:        ui.SermonsList{Sermons: sermons},
	})
}

// handleSermonCreate starts a sermon and opens its builder.
func (s *Server) handleSermonCreate(ctx rweb.Context) error {
	id, err := study.CreateSermon(s.store.Study, ctx.Request().FormValue("title"))
	if err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot create sermon", "The study database could not be written.", err)
	}
	return ctx.Redirect(http.StatusSeeOther, ui.SermonURL(id))
}

// handleSermonShow renders the builder.
//
// ?draft=1 turns on the live drafting panel. It is a query parameter rather
// than a separate page because the panel is one card on an otherwise identical
// page — and because the drafting POST has to land somewhere bookmarkable, so
// a refresh mid-draft reloads the builder rather than re-posting.
func (s *Server) handleSermonShow(ctx rweb.Context) error {
	sermon, ok := s.loadSermon(ctx)
	if !ok {
		return nil
	}

	streaming := ctx.Request().QueryParam("draft") == "1" && s.cfg.AIEnabled()
	return s.renderSermon(ctx, sermon, streaming, "")
}

// handleSermonRename saves a new title.
func (s *Server) handleSermonRename(ctx rweb.Context) error {
	sermon, ok := s.loadSermon(ctx)
	if !ok {
		return nil
	}

	if err := study.UpdateSermonTitle(s.store.Study, sermon.ID,
		ctx.Request().FormValue("title")); err != nil {
		return s.renderSermon(ctx, sermon, false, "A sermon needs a title.")
	}
	return ctx.Redirect(http.StatusSeeOther, ui.SermonURL(sermon.ID))
}

// handleSermonDelete tombstones a sermon and returns to the index.
func (s *Server) handleSermonDelete(ctx rweb.Context) error {
	id := ctx.Request().Param("id")

	if err := study.DeleteSermon(s.store.Study, id); err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot delete sermon", "The study database could not be written.", err)
	}
	return ctx.Redirect(http.StatusSeeOther, "/sermons")
}

// --- outline editing -------------------------------------------------------

// handleSectionAdd appends a section built from the add forms.
//
// One handler for all four kinds: they differ only in which field carries the
// content, and splitting them would mean four near-identical validate-append-
// redirect paths drifting apart.
func (s *Server) handleSectionAdd(ctx rweb.Context) error {
	sermon, ok := s.loadSermon(ctx)
	if !ok {
		return nil
	}

	sec, problem := readSectionForm(ctx)
	if problem != "" {
		return s.renderSermon(ctx, sermon, false, problem)
	}

	if _, err := study.AppendSection(s.store.Study, sermon.ID, sec); err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot add to the outline", "The study database could not be written.", err)
	}
	return ctx.Redirect(http.StatusSeeOther, ui.SermonURL(sermon.ID))
}

// handleSectionMove shifts a section one place in either direction.
func (s *Server) handleSectionMove(ctx rweb.Context) error {
	sermon, ok := s.loadSermon(ctx)
	if !ok {
		return nil
	}

	delta := 1
	if ctx.Request().FormValue("dir") == "up" {
		delta = -1
	}

	if err := study.MoveSection(s.store.Study, sermon.ID,
		ctx.Request().Param("sectionId"), delta); err != nil {
		// The only failure a user can cause here is acting on a stale page, so
		// the outline is re-read and shown as it now stands rather than
		// replaced with an error page.
		logger.LogErr(err, "cannot move outline section", "sermon", sermon.ID)
		return s.reloadSermonWithProblem(ctx, sermon.ID,
			"That section is no longer where the page thought it was; here is the outline as it stands.")
	}
	return ctx.Redirect(http.StatusSeeOther, ui.SermonURL(sermon.ID))
}

// handleSectionDelete removes a section from the outline.
func (s *Server) handleSectionDelete(ctx rweb.Context) error {
	sermon, ok := s.loadSermon(ctx)
	if !ok {
		return nil
	}

	if err := study.RemoveSection(s.store.Study, sermon.ID,
		ctx.Request().Param("sectionId")); err != nil {
		logger.LogErr(err, "cannot remove outline section", "sermon", sermon.ID)
		return s.reloadSermonWithProblem(ctx, sermon.ID,
			"That section had already been removed.")
	}
	return ctx.Redirect(http.StatusSeeOther, ui.SermonURL(sermon.ID))
}

// --- exports ---------------------------------------------------------------

// handleSermonExportMD serves the assembled outline as a Markdown download.
func (s *Server) handleSermonExportMD(ctx rweb.Context) error {
	sermon, ok := s.loadSermon(ctx)
	if !ok {
		return nil
	}

	doc, err := s.assemble(sermon)
	if err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot assemble the outline",
			"The outline could not be read against the scripture cache.", err)
	}

	res := ctx.Response()
	res.SetHeader("Content-Type", "text/markdown; charset=utf-8")
	res.SetHeader("Content-Disposition",
		`attachment; filename="`+study.FileSlug(sermon.Title)+`.md"`)
	return ctx.WriteString(doc)
}

// handleSermonExportHTML serves a standalone HTML rendering.
//
// Disposition is inline rather than attachment: an HTML sermon is something to
// read and print, and a browser tab is the reader. Saving it is still one
// keystroke away, and the document is self-contained (see study.ExportHTML), so
// the saved copy looks the same as the tab.
func (s *Server) handleSermonExportHTML(ctx rweb.Context) error {
	sermon, ok := s.loadSermon(ctx)
	if !ok {
		return nil
	}

	doc, err := s.assemble(sermon)
	if err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot assemble the outline",
			"The outline could not be read against the scripture cache.", err)
	}

	page, err := study.ExportHTML(sermon.Title, doc)
	if err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot render the export", "The outline could not be turned into HTML.", err)
	}

	ctx.Response().SetHeader("Content-Disposition",
		`inline; filename="`+study.FileSlug(sermon.Title)+`.html"`)
	return ctx.WriteHTML(page)
}

// --- AI drafting -----------------------------------------------------------

// handleSermonDraft is the button that starts a draft.
//
// It validates and redirects; the generation itself begins when the browser
// opens the event stream on the page it lands on. Splitting it that way is
// what removes the race between "the job started producing" and "the reader
// connected" — there is exactly one request, one goroutine and one channel, so
// no event can be produced before there is somebody to receive it.
func (s *Server) handleSermonDraft(ctx rweb.Context) error {
	sermon, ok := s.loadSermon(ctx)
	if !ok {
		return nil
	}

	if !s.cfg.AIEnabled() {
		return s.notFound(ctx, "Drafting is off; no API key is configured.")
	}
	if len(sermon.Outline) == 0 {
		return s.renderSermon(ctx, sermon, false,
			"There is nothing in this outline to draft from.")
	}

	return ctx.Redirect(http.StatusSeeOther, ui.SermonURL(sermon.ID)+"?draft=1")
}

// handleSermonDraftStream generates the draft and streams it.
//
// Every rejection is delivered as a one-event stream rather than an HTTP
// status. An EventSource cannot read a status line or a response body — a 404
// here reaches the browser as a bare connection error and shows the user
// nothing — so the reason travels as a "fail" event on a 200 stream, which the
// page can display.
func (s *Server) handleSermonDraftStream(ctx rweb.Context) error {
	id := ctx.Request().Param("id")
	ch := make(chan any, draftBuffer)

	// refuse sends one explanation and closes. Safe to write without a reader:
	// the channel is buffered, so neither the send nor the close can block.
	refuse := func(reason string) error {
		ch <- rweb.SSEvent{Type: sseFail, Data: `{"text":` + jsonString(reason) + `}`}
		close(ch)
		return s.rweb.SetupSSE(ctx, ch, sseDelta)
	}

	if !s.cfg.AIEnabled() {
		return refuse("Drafting is off; no API key is configured.")
	}

	sermon, err := study.GetSermon(s.store.Study, id)
	if err != nil {
		return refuse("That sermon does not exist, or has been deleted.")
	}
	if len(sermon.Outline) == 0 {
		return refuse("There is nothing in this outline to draft from.")
	}

	// Claim the sermon before doing any work, so a reconnecting EventSource or
	// a second tab cannot start a parallel generation.
	if !s.drafts.begin(sermon.ID) {
		return refuse("A draft is already being written for this sermon.")
	}

	doc, err := s.assemble(sermon)
	if err != nil {
		s.drafts.end(sermon.ID)
		logger.LogErr(err, "cannot assemble outline for drafting", "sermon", sermon.ID)
		return refuse("The outline could not be assembled, so there was nothing to send.")
	}

	if err := study.SetStatus(s.store.Study, sermon.ID, study.StatusDrafting); err != nil {
		// Status is bookkeeping, not a gate — nothing refuses to run because of
		// it, so a failure to record it must not stop the draft.
		logger.LogErr(err, "cannot mark sermon as drafting", "sermon", sermon.ID)
	}

	go s.runDraft(sermon.ID, sermon.Title, doc, ch)

	// The handler returns here; rweb drains ch onto the socket afterwards.
	return s.rweb.SetupSSE(ctx, ch, sseDelta)
}

// --- shared plumbing -------------------------------------------------------

// loadSermon fetches the sermon named in the path, writing a 404 and returning
// false when there is none. The two-value shape keeps every handler's first
// three lines identical.
func (s *Server) loadSermon(ctx rweb.Context) (study.Sermon, bool) {
	sermon, err := study.GetSermon(s.store.Study, ctx.Request().Param("id"))
	if err != nil {
		_ = s.notFound(ctx, "That sermon does not exist, or has been deleted.")
		return study.Sermon{}, false
	}
	return sermon, true
}

// assemble builds the Markdown document for a sermon: exports and the AI
// drafter both come through here, so they cannot diverge.
func (s *Server) assemble(sermon study.Sermon) (string, error) {
	return study.AssembleOutline(s.store.Study, s.store.Bible, sermon, s.defaultTranslation())
}

// reloadSermonWithProblem re-reads a sermon and shows it with a message. Used
// where the user acted on a page that no longer matches the stored outline.
func (s *Server) reloadSermonWithProblem(ctx rweb.Context, id, problem string) error {
	sermon, err := study.GetSermon(s.store.Study, id)
	if err != nil {
		return s.notFound(ctx, "That sermon does not exist, or has been deleted.")
	}
	return s.renderSermon(ctx, sermon, false, problem)
}

// renderSermon assembles the builder page.
//
// Every read below degrades on its own rather than failing the page: the
// outline is the reason to be here and it is already in hand. A missing note
// title becomes a "no longer available" row, an unrenderable draft becomes no
// draft, and an empty note picker becomes a hint — none of which is worth
// replacing the outline with an error page.
func (s *Server) renderSermon(ctx rweb.Context, sermon study.Sermon,
	streaming bool, problem string) error {

	translation := s.defaultTranslation()

	noteIDs := make([]string, 0, len(sermon.Outline))
	for _, sec := range sermon.Outline {
		if sec.Kind == study.KindNote && sec.NoteID != "" {
			noteIDs = append(noteIDs, sec.NoteID)
		}
	}
	referenced, err := study.NotesByIDs(s.store.Study, noteIDs)
	if err != nil {
		logger.LogErr(err, "cannot resolve outline notes", "sermon", sermon.ID)
		referenced = map[string]study.Note{}
	}

	rows := make([]ui.OutlineRow, 0, len(sermon.Outline))
	for _, sec := range sermon.Outline {
		row := ui.OutlineRow{
			Section: sec,
			Label:   study.DescribeSection(sec),
			Href:    ui.SectionHref(sec, translation),
		}
		if sec.Kind == study.KindNote {
			note, ok := referenced[sec.NoteID]
			if !ok {
				row.Label = "This note has been deleted."
				row.Href = ""
				row.Missing = true
			} else {
				row.Label = note.Title
			}
		}
		rows = append(rows, row)
	}

	draftHTML := ""
	if sermon.HasDraft() {
		draftHTML, err = study.RenderMarkdown(sermon.DraftMD)
		if err != nil {
			logger.LogErr(err, "cannot render sermon draft", "sermon", sermon.ID)
			draftHTML = ""
		}
	}

	// The picker offers every live note. A study database large enough for that
	// to be unwieldy is also one where the notes list and search are the way in,
	// and this select is not the place to reinvent either.
	knownNotes, err := study.ListNotes(s.store.Study, 0)
	if err != nil {
		knownNotes = nil
	}

	return s.render(ctx, ui.Page{
		Title:       sermon.Title,
		Active:      ui.NavSermons,
		Translation: translation,
		Body: ui.SermonBuilder{
			Sermon:      sermon,
			Rows:        rows,
			Translation: translation,
			KnownNotes:  knownNotes,
			DraftHTML:   draftHTML,
			AIEnabled:   s.cfg.AIEnabled(),
			Streaming:   streaming,
			Problem:     problem,
		},
	})
}

// readSectionForm turns the add forms into a section, or returns the message to
// show the user.
func readSectionForm(ctx rweb.Context) (study.Section, string) {
	req := ctx.Request()
	kind := strings.TrimSpace(req.FormValue("kind"))
	text := strings.TrimSpace(req.FormValue("text"))

	sec := study.Section{Kind: kind}

	switch kind {
	case study.KindHeading, study.KindPoint:
		sec.Text = text

	case study.KindPassage:
		ref, err := bible.ParseRef(text)
		if err != nil {
			return sec, "Could not read " + strconv.Quote(text) +
				" as a passage. Try the form \"John 3:16-18\"."
		}
		sec.Ref = ref

	case study.KindNote:
		sec.NoteID = strings.TrimSpace(req.FormValue("noteId"))

	default:
		return sec, "That is not a kind of section this outline understands."
	}

	if err := study.ValidateSection(sec); err != nil {
		return sec, capitalize(err.Error()) + "."
	}
	return sec, ""
}

// jsonString quotes a string as a JSON value. Used for the handful of refusal
// payloads that are built without an event struct.
func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}

// capitalize upper-cases the first letter, so an error phrased for a log reads
// as a sentence on a page.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
