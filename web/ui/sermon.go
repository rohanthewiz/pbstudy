package ui

import (
	"github.com/rohanthewiz/element"

	"github.com/rohanthewiz/pbstudy/study"
)

// SermonURL is the permalink for a sermon.
func SermonURL(id string) string { return "/sermons/" + pathSeg(id) }

// SermonsList is the /sermons index.
type SermonsList struct {
	Sermons []study.Sermon
}

func (l SermonsList) Render(b *element.Builder) (x any) {
	b.H1Class("page-title").T("Sermons")
	b.PClass("page-sub").T(
		"A sermon is an ordered outline of headings, passages, notes and points. " +
			"Assemble it here, then export it or hand it to the drafter.")

	// The create form is on the index rather than behind a "New sermon" page:
	// a sermon starts as nothing but a title, and a whole page round trip to
	// type one is ceremony.
	b.DivClass("card").R(
		b.FormClass("inline-create", "method", "post", "action", "/sermons").R(
			b.Input("type", "text", "name", "title", "placeholder", "Title of the sermon",
				"autocomplete", "off", "required", "required"),
			b.Button("type", "submit", "class", "btn btn-primary").T("Start a sermon"),
		),
	)

	if len(l.Sermons) == 0 {
		b.DivClass("notice").R(
			b.Strong().T("No sermons yet. "),
			b.T("Name one above, then build its outline from the passages, notes and "),
			b.T("headings you have already collected."),
		)
		return
	}

	b.DivClass("card").R(
		element.ForEach(l.Sermons, func(s study.Sermon) {
			b.DivClass("note-row").R(
				b.AClass("note-title", "href", SermonURL(s.ID)).T(esc(s.Title)),
				b.DivClass("chip-row").R(
					b.SpanClass("chip").T(Plural(s.SectionCount(), "section")),
					b.Wrap(func() {
						if s.HasDraft() {
							b.SpanClass("chip chip-tag").T("drafted")
						}
					}),
				),
			)
		}),
	)
	return
}

// OutlineRow is one section of an outline, resolved for display.
//
// The handler resolves note titles before rendering rather than letting the
// view query: a view that reads the database is a view that can fail halfway
// through writing a response, with the status line already sent.
type OutlineRow struct {
	Section study.Section
	// Label is what the row reads as.
	Label string
	// Href links the row at its source — the note, or the passage in scripture.
	// Empty for headings and points, which have no elsewhere to be.
	Href string
	// Missing marks a section whose note has been deleted.
	Missing bool
}

// SermonBuilder is the single-sermon page: the outline, the controls that
// change it, the exports, and the draft.
type SermonBuilder struct {
	Sermon      study.Sermon
	Rows        []OutlineRow
	Translation string
	// KnownNotes populates the "add a note" picker.
	KnownNotes []study.Note
	// DraftHTML is the stored draft rendered from Markdown, or empty.
	DraftHTML string
	// AIEnabled mirrors cfg.Config.AIEnabled. When false the drafting controls
	// are absent entirely rather than present-and-failing: a button that
	// explains it cannot work is worse than no button.
	AIEnabled bool
	// Streaming turns on the live draft panel, set by ?draft=1 after the
	// drafting POST redirects here.
	Streaming bool
	Problem   string
	// EditingID names the one section whose row is rendered as an edit form
	// instead of a label; empty means none. Set from ?edit=<section id>, so
	// "this row is being edited" is a URL like every other state of this page
	// and needs no JavaScript.
	EditingID string
	// EditingText is what the edit form's input holds: the section's stored
	// content when the form is first opened, or what the user typed when a
	// rejected edit is shown again. The handler decides which, so nothing the
	// user typed is replaced by the stored value on a validation failure.
	EditingText string
}

func (v SermonBuilder) Render(b *element.Builder) (x any) {
	b.DivClass("page-head").R(
		b.H1Class("page-title").T(esc(v.Sermon.Title)),
		b.DivClass("link-row").R(
			b.A("class", "btn", "href", SermonURL(v.Sermon.ID)+"/export.md").T("Markdown"),
			b.A("class", "btn", "href", SermonURL(v.Sermon.ID)+"/export.html").T("HTML"),
		),
	)

	b.PClass("page-sub").R(
		b.T(Plural(v.Sermon.SectionCount(), "section")),
		b.T(" · scripture in "),
		b.T(upper(v.Translation)),
		b.T(" · updated "),
		b.Time("datetime", v.Sermon.UpdatedAt.Format("2006-01-02T15:04:05Z")).
			T(v.Sermon.UpdatedAt.Local().Format("2 Jan 2006, 15:04")),
	)

	if v.Problem != "" {
		b.DivClass("notice").R(
			b.Strong().T("Not changed. "),
			b.T(esc(v.Problem)),
		)
	}

	v.renderRenameForm(b)
	v.renderOutline(b)
	v.renderAddForms(b)
	v.renderDraft(b)
	v.renderDangerZone(b)
	return
}

// renderOutline lists the sections with their reordering controls.
//
// Each control is its own one-button form rather than a checkbox column and a
// single "apply" button. Three tiny POSTs per row is more markup, but it means
// every action is complete on click, works with JavaScript disabled, and — with
// each button carrying its section's id rather than its position — a stale page
// cannot move the wrong row.
func (v SermonBuilder) renderOutline(b *element.Builder) (x any) {
	if len(v.Rows) == 0 {
		b.DivClass("notice").R(
			b.Strong().T("This outline is empty. "),
			b.T("Add a heading to open a movement, a passage to preach from, "),
			b.T("a note you have already written, or a point you want to make."),
		)
		return
	}

	b.DivClass("card").R(
		element.ForEach(v.Rows, func(row OutlineRow) {
			// The row carries an id so a URL fragment can land on it: the edit
			// form's Cancel and the redirect after a save both point at
			// #sec-<id>, which keeps the browser on the row that was touched
			// instead of back at the top of a long outline. (The link that
			// opens the form deliberately does not; see sectionEditLink.)
			b.DivClass("outline-row", "id", SectionAnchor(row.Section.ID)).R(
				b.SpanClass("chip chip-kind").T(kindLabel(row.Section.Kind)),

				b.DivClass("outline-label").R(
					b.Wrap(func() {
						switch {
						case v.editing(row):
							v.renderSectionEdit(b, row)
						case row.Missing:
							b.EmClass("empty").T(esc(row.Label))
						case row.Href != "":
							b.A("href", row.Href).T(esc(row.Label))
						default:
							b.T(esc(row.Label))
						}
					}),
				),

				b.DivClass("outline-actions").R(
					v.sectionEditLink(b, row),
					v.sectionButton(b, row.Section.ID, "move", "up", "↑", "Move up"),
					v.sectionButton(b, row.Section.ID, "move", "down", "↓", "Move down"),
					v.sectionButton(b, row.Section.ID, "delete", "", "×", "Remove from the outline"),
				),
			)
		}),
	)
	return
}

// SectionAnchor is the element id, and so the URL fragment, of a section's
// row. Path-escaped like every other place an id reaches markup: section ids
// are minted here as UUIDs, but an outline can also arrive in a sync file, and
// the id of a section in one is whatever that file says.
func SectionAnchor(sectionID string) string { return "sec-" + pathSeg(sectionID) }

// editing reports whether this row is the one shown as an edit form.
//
// The kind is checked as well as the id: ?edit= is a query parameter anyone can
// type, and naming a note section there must leave it as the link it is rather
// than render a form the server would refuse.
func (v SermonBuilder) editing(row OutlineRow) bool {
	return v.EditingID != "" && row.Section.ID == v.EditingID &&
		study.SectionEditable(row.Section.Kind)
}

// sectionEditLink is the pencil that opens a row for editing.
//
// A link rather than a button, because opening the form changes nothing: it is
// a GET of this same page with ?edit=<id>, and the server renders that one row
// as a form. A <details> disclosure per row was the other candidate (it is what
// the rename and delete controls use), but it adds a line of "Edit" under every
// row of the outline for a control used on one row at a time.
//
// It comes first in the actions so the three buttons every row has stay in the
// same place whether or not the row can be edited; note rows have no pencil.
//
// # Why this link carries no #sec-<id> fragment
//
// It would seem to want one, to scroll to the row being opened. But browsers
// skip autofocus on a page whose URL has a fragment (the fragment is taken as
// the author's statement of where the page should start), and the form's
// input relies on autofocus: seen in Chrome, the row opened with the cursor
// nowhere and the first keystroke went to the page. Without the fragment the
// input is focused, and focusing an element scrolls it into view, so the row
// is on screen either way.
func (v SermonBuilder) sectionEditLink(b *element.Builder, row OutlineRow) (x any) {
	if !study.SectionEditable(row.Section.Kind) || v.editing(row) {
		return
	}
	href := SermonURL(v.Sermon.ID) + "?edit=" + urlQueryEscape(row.Section.ID)
	b.A("class", "btn btn-icon", "href", href, "title", "Edit this section").T("✎")
	return
}

// renderSectionEdit is the form a row becomes while it is being edited.
//
// One text input for all three editable kinds, the same shape as the add form
// below the outline: a heading or a point is its text, and a passage is typed
// as a reference and parsed on the server (so "John 3:16-18" and "Jn 3.16-18"
// both work, and a reference that does not parse comes back here with the
// reason, still holding what was typed).
func (v SermonBuilder) renderSectionEdit(b *element.Builder, row OutlineRow) (x any) {
	action := SermonURL(v.Sermon.ID) + "/sections/" + pathSeg(row.Section.ID) + "/edit"

	placeholder := "What this section says"
	if row.Section.Kind == study.KindPassage {
		placeholder = "A reference like John 3:16-18"
	}

	b.FormClass("outline-edit", "method", "post", "action", action).R(
		b.Input("type", "text", "name", "text", "value", esc(v.EditingText),
			"placeholder", placeholder, "aria-label", "Section text",
			"autocomplete", "off", "required", "required", "autofocus", "autofocus"),
		b.Button("type", "submit", "class", "btn btn-primary").T("Save"),
		b.A("class", "btn", "href",
			SermonURL(v.Sermon.ID)+"#"+SectionAnchor(row.Section.ID)).T("Cancel"),
	)
	return
}

// sectionButton emits one single-button form targeting a section by id.
func (v SermonBuilder) sectionButton(b *element.Builder, sectionID, action, dir, glyph, title string) (x any) {
	url := SermonURL(v.Sermon.ID) + "/sections/" + pathSeg(sectionID) + "/" + action

	b.Form("method", "post", "action", url).R(
		b.Wrap(func() {
			if dir != "" {
				b.Input("type", "hidden", "name", "dir", "value", dir)
			}
		}),
		b.Button("type", "submit", "class", "btn btn-icon", "title", title).T(glyph),
	)
	return
}

// renderAddForms is how sections get into the outline.
//
// Two forms, split by what they need from the user rather than by section kind:
// headings, points and passages are all "pick a kind, type a line", while a note
// is "choose one you already wrote". Folding the note picker into the first form
// would mean a select that is meaningless for three of the four kinds.
func (v SermonBuilder) renderAddForms(b *element.Builder) (x any) {
	action := SermonURL(v.Sermon.ID) + "/sections"

	b.DivClass("card").R(
		b.H2().T("Add to the outline"),

		b.FormClass("section-add", "method", "post", "action", action).R(
			b.SelectClass("section-kind", "name", "kind").R(
				b.Option("value", study.KindHeading).T("Heading"),
				b.Option("value", study.KindPassage).T("Passage"),
				b.Option("value", study.KindPoint).T("Point"),
			),
			b.Input("type", "text", "name", "text", "autocomplete", "off",
				"placeholder", "A heading, a point, or a reference like John 3:16-18"),
			b.Button("type", "submit", "class", "btn btn-primary").T("Add"),
		),
		b.SpanClass("field-hint").T(
			"A passage is looked up when the outline is assembled, so its text is "+
				"always the current one in the cache."),

		b.Wrap(func() {
			if len(v.KnownNotes) == 0 {
				b.PClass("empty").T("You have no notes to add yet.")
				return
			}
			b.FormClass("section-add", "method", "post", "action", action).R(
				b.Input("type", "hidden", "name", "kind", "value", study.KindNote),
				b.SelectClass("section-note", "name", "noteId").R(
					element.ForEach(v.KnownNotes, func(n study.Note) {
						b.Option("value", esc(n.ID)).T(esc(n.Title))
					}),
				),
				b.Button("type", "submit", "class", "btn").T("Add note"),
			)
		}),
	)
	return
}

// renderDraft holds the AI half of the page: the button, the live stream, and
// whatever draft is already stored.
func (v SermonBuilder) renderDraft(b *element.Builder) (x any) {
	b.DivClass("card").R(
		b.H2().T("Draft"),

		b.Wrap(func() {
			switch {
			case !v.AIEnabled:
				b.P().R(
					b.T("Drafting is off. Set "),
					b.Code().T("ANTHROPIC_API_KEY"),
					b.T(" and restart to have a draft written from this outline. "),
					b.T("The exports above work either way — the outline is the document."),
				)
			case len(v.Rows) == 0:
				b.PClass("empty").T("Add something to the outline first; there is nothing to draft from.")
			default:
				b.P().R(
					b.T("The drafter is given the assembled outline — the same document the "),
					b.T("exports produce — and asked to write from it without adding scripture "),
					b.T("of its own."),
				)
				b.Form("method", "post", "action", SermonURL(v.Sermon.ID)+"/draft").R(
					b.Button("type", "submit", "class", "btn btn-primary").
						T(draftButtonLabel(v.Sermon.HasDraft())),
				)
			}
		}),

		b.Wrap(func() {
			if v.Streaming {
				v.renderStreamPanel(b)
			}
		}),

		b.Wrap(func() {
			// The stored draft stays on the page while a new one streams above
			// it: until the replacement is saved, the old draft is still the
			// only finished one there is.
			if v.DraftHTML == "" {
				return
			}
			b.DivClass("draft-body").R(
				renderMarkdownBody(b, v.DraftHTML),
			)
		}),
	)
	return
}

// renderStreamPanel is the live drafting view.
//
// The data-draft-url attribute is the whole contract with app.js: no inline
// script, no template-injected JavaScript, just a URL the script opens an
// EventSource against. Nothing user-supplied goes into it — the sermon id is a
// UUID this app minted.
func (v SermonBuilder) renderStreamPanel(b *element.Builder) (x any) {
	b.DivClass("draft-stream", "data-draft-url", SermonURL(v.Sermon.ID)+"/draft/stream").R(
		b.DivClass("draft-status", "data-draft-status", "1").T("Connecting…"),
		b.PreClass("draft-text", "data-draft-text", "1").T(""),
		b.DivClass("link-row draft-done", "data-draft-done", "1", "hidden", "hidden").R(
			b.A("class", "btn btn-primary", "href", SermonURL(v.Sermon.ID)).
				T("Open the saved draft"),
		),
	)
	return
}

func (v SermonBuilder) renderRenameForm(b *element.Builder) (x any) {
	b.DetailsClass("inline-edit").R(
		b.Summary().T("Rename this sermon"),
		b.Form("method", "post", "action", SermonURL(v.Sermon.ID)).R(
			b.Input("type", "text", "name", "title", "value", esc(v.Sermon.Title),
				"autocomplete", "off", "required", "required"),
			b.Button("type", "submit", "class", "btn").T("Save"),
		),
	)
	return
}

func (v SermonBuilder) renderDangerZone(b *element.Builder) (x any) {
	b.DetailsClass("danger-zone").R(
		b.Summary().T("Delete this sermon"),
		b.PClass("page-sub").T(
			"The outline and any draft go with it. The notes it referenced are untouched."),
		b.Form("method", "post", "action", SermonURL(v.Sermon.ID)+"/delete").R(
			b.Button("type", "submit", "class", "btn btn-danger").T("Delete sermon"),
		),
	)
	return
}

// --- small helpers ---------------------------------------------------------

func kindLabel(kind string) string {
	switch kind {
	case study.KindHeading:
		return "Heading"
	case study.KindPassage:
		return "Passage"
	case study.KindNote:
		return "Note"
	case study.KindPoint:
		return "Point"
	default:
		// An unknown kind can only have come from a sync file (the builder's
		// forms post the four constants), so it is echoed escaped rather
		// than trusted.
		return esc(kind)
	}
}

func draftButtonLabel(hasDraft bool) string {
	if hasDraft {
		return "Draft again"
	}
	return "Draft with AI"
}

// SectionHref points a row at its source: a note at its page, a passage at the
// reader or the verse hub. Headings and points have no destination.
func SectionHref(sec study.Section, translation string) string {
	switch sec.Kind {
	case study.KindNote:
		return NoteURL(sec.NoteID)
	case study.KindPassage:
		return refHref(sec.Ref, translation)
	default:
		return ""
	}
}
