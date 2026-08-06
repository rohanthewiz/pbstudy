package ui

import (
	"github.com/rohanthewiz/element"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/blb"
	"github.com/rohanthewiz/pbstudy/study"
)

// NoteURL is the permalink for a note.
func NoteURL(id string) string { return "/notes/" + id }

// NotesList is the /notes index.
type NotesList struct {
	Notes []study.Note
	// Translation rides along so every reference chip links back into the
	// version the user is reading.
	Translation string
}

func (l NotesList) Render(b *element.Builder) (x any) {
	b.DivClass("page-head").R(
		b.H1Class("page-title").T("Notes"),
		b.A("class", "btn btn-primary", "href", "/notes/new").T("New note"),
	)

	if len(l.Notes) == 0 {
		b.DivClass("notice").R(
			b.Strong().T("No notes yet. "),
			b.T("Start one from a verse — open a chapter, click a verse number, "),
			b.T("and add a note there — or create a free-standing one with "),
			b.Strong().T("New note"),
			b.T("."),
		)
		return
	}

	b.PClass("page-sub").T(Plural(len(l.Notes), "note") + ", most recently edited first.")

	// Note bodies are exactly where ScriptTagger SHOULD work — a reference
	// the user typed into a note gets a hover popup. So no no-tag class here,
	// unlike the scripture containers.
	b.DivClass("card").R(
		element.ForEach(l.Notes, func(n study.Note) {
			renderNoteSummary(b, n)
		}),
	)
	return
}

// NoteDetail is the single-note page.
//
// It shows the note beside the scripture it is anchored to, which is the
// reason this app exists rather than a folder of Markdown files: the passage
// is right there, offline, without leaving the note.
type NoteDetail struct {
	Note study.Note
	// BodyHTML is the output of study.RenderMarkdown — already-escaped HTML,
	// written raw. See renderMarkdownBody.
	BodyHTML string
	// Anchors pairs each of the note's references with the scripture text it
	// points at, resolved by the handler.
	Anchors     []Anchor
	Translation string
}

// Anchor is one of a note's references together with the verses it covers.
type Anchor struct {
	Ref    bible.Ref
	Verses []bible.Verse
}

func (d NoteDetail) Render(b *element.Builder) (x any) {
	b.DivClass("page-head").R(
		b.H1Class("page-title").T(esc(d.Note.Title)),
		b.DivClass("link-row").R(
			b.A("class", "btn", "href", NoteURL(d.Note.ID)+"/edit").T("Edit"),
		),
	)

	b.PClass("page-sub").R(
		b.T("Updated "),
		b.Time("datetime", d.Note.UpdatedAt.Format("2006-01-02T15:04:05Z")).
			T(d.Note.UpdatedAt.Local().Format("2 Jan 2006, 15:04")),
	)

	renderRefChips(b, d.Note.Refs, d.Translation)
	renderTagChips(b, d.Note.Tags)

	b.DivClass("card note-body").R(
		renderMarkdownBody(b, d.BodyHTML),
	)

	d.renderAnchors(b)
	d.renderDangerZone(b)
	return
}

// renderAnchors shows the scripture each reference points at.
//
// This container IS marked no-tag: the verses here are rendered by us with our
// own links, so letting ScriptTagger re-scan them would double-link them. The
// note body above stays taggable, which is the split the whole integration is
// built around.
func (d NoteDetail) renderAnchors(b *element.Builder) (x any) {
	if len(d.Anchors) == 0 {
		return
	}

	b.Div("class", "card "+blb.NoTagClass).R(
		b.H2().T("Scripture"),
		element.ForEach(d.Anchors, func(a Anchor) {
			b.DivClass("anchor").R(
				b.AClass("anchor-ref", "href", refHref(a.Ref, d.Translation)).
					T(esc(a.Ref.String())),
				b.Wrap(func() {
					if len(a.Verses) == 0 {
						b.PClass("empty").F("No %s text cached for this passage.",
							upper(d.Translation))
						return
					}
					b.DivClass("anchor-text").R(
						element.ForEach(a.Verses, func(v bible.Verse) {
							b.SpanClass("verse-inline").R(
								b.SpanClass("verse-num").T(itoa(v.Num)),
								b.T(esc(v.Body)),
							)
						}),
					)
				}),
			)
		}),
	)
	return
}

// renderDangerZone hides the delete button behind a disclosure.
//
// A <details> element rather than a JavaScript confirm(): it needs no script,
// it is keyboard accessible for free, and it makes deleting a two-deliberate-
// actions affair without a modal that can be dismissed by reflex.
func (d NoteDetail) renderDangerZone(b *element.Builder) (x any) {
	b.DetailsClass("danger-zone").R(
		b.Summary().T("Delete this note"),
		b.PClass("page-sub").T(
			"The note is retired rather than erased, so the deletion carries across "+
				"to your other machines when sync is enabled."),
		b.Form("method", "post", "action", NoteURL(d.Note.ID)+"/delete").R(
			b.Button("type", "submit", "class", "btn btn-danger").T("Delete note"),
		),
	)
	return
}

// NoteEditor is the create/edit form.
//
// One component for both, distinguished only by Action and by whether Note.ID
// is set. The alternative — two nearly identical forms — is how the create and
// edit paths drift apart.
type NoteEditor struct {
	// Action is where the form posts.
	Action string
	// Heading titles the page ("New note" / "Edit note").
	Heading string
	// The form's current values. On a validation failure these are what the
	// user typed, not what is stored, so nothing is lost on a re-render.
	Title      string
	RefsField  string
	TagsField  string
	BodyMD     string
	CancelURL  string
	KnownTags  []study.Tag
	Problem    string
	IsExisting bool
}

func (e NoteEditor) Render(b *element.Builder) (x any) {
	b.H1Class("page-title").T(esc(e.Heading))

	if e.Problem != "" {
		b.DivClass("notice").R(
			b.Strong().T("Not saved. "),
			b.T(esc(e.Problem)),
		)
	}

	b.FormClass("note-editor", "method", "post", "action", e.Action).R(
		b.LabelClass("field").R(
			b.SpanClass("field-label").T("Title"),
			b.Input("type", "text", "name", "title", "value", esc(e.Title),
				"placeholder", "Left blank, the first line of the note is used",
				"autocomplete", "off"),
		),

		b.LabelClass("field").R(
			b.SpanClass("field-label").T("References"),
			b.Input("type", "text", "name", "refs", "value", esc(e.RefsField),
				"placeholder", "John 3:16-18; Rom 5:8; Ps 23",
				"autocomplete", "off"),
			b.SpanClass("field-hint").T(
				"Separate with semicolons. A chapter alone (\"John 3\") anchors the "+
					"note to the whole chapter."),
		),

		b.LabelClass("field").R(
			b.SpanClass("field-label").T("Tags"),
			b.Input("type", "text", "name", "tags", "value", esc(e.TagsField),
				"placeholder", "Grace, Covenant", "autocomplete", "off",
				"list", "known-tags"),
			b.Wrap(func() {
				// A datalist rather than a multi-select: tags are created by
				// typing, and this offers the ones that exist without getting
				// in the way of inventing a new one.
				if len(e.KnownTags) == 0 {
					return
				}
				b.DataList("id", "known-tags").R(
					element.ForEach(e.KnownTags, func(t study.Tag) {
						b.Option("value", esc(t.Name)).R()
					}),
				)
			}),
		),

		b.LabelClass("field").R(
			b.SpanClass("field-label").T("Note"),
			b.TextArea("name", "body", "rows", "18", "spellcheck", "true",
				"placeholder", "Markdown. Cite a Strong's number as [[G26]] to link the lexicon.").
				T(esc(e.BodyMD)),
			b.SpanClass("field-hint").T(
				"Markdown is rendered on save. [[G26]] and [[H430]] become Blue Letter "+
					"Bible lexicon links."),
		),

		b.DivClass("link-row").R(
			b.Button("type", "submit", "class", "btn btn-primary").T("Save note"),
			b.A("class", "btn", "href", e.CancelURL).T("Cancel"),
		),
	)
	return
}

// --- shared note fragments -------------------------------------------------

// renderNoteSummary is one row in a list of notes: title, anchors, tags, and a
// one-line excerpt. Used by the notes list, the tag page, the verse hub and
// the reader, so all four stay identical without any of them knowing about the
// others.
func renderNoteSummary(b *element.Builder, n study.Note) (x any) {
	b.DivClass("note-row").R(
		b.AClass("note-title", "href", NoteURL(n.ID)).T(esc(n.Title)),
		b.Wrap(func() {
			if len(n.Refs) == 0 && len(n.Tags) == 0 {
				return
			}
			b.DivClass("chip-row").R(
				element.ForEach(n.Refs, func(r study.NoteRef) {
					b.SpanClass("chip chip-ref").T(esc(r.Ref.String()))
				}),
				element.ForEach(n.Tags, func(t study.Tag) {
					b.AClass("chip chip-tag", "href", TagURL(t.ID)).T(esc(t.Name))
				}),
			)
		}),
		b.PClass("note-excerpt").T(esc(study.Excerpt(n.BodyMD, 200))),
	)
	return
}

// renderRefChips renders a note's anchors as links into scripture.
func renderRefChips(b *element.Builder, refs []study.NoteRef, translation string) (x any) {
	if len(refs) == 0 {
		return
	}
	b.DivClass("chip-row").R(
		element.ForEach(refs, func(r study.NoteRef) {
			b.AClass("chip chip-ref", "href", refHref(r.Ref, translation)).
				T(esc(r.Ref.String()))
		}),
	)
	return
}

// renderTagChips renders a note's tags as links to their topical pages.
func renderTagChips(b *element.Builder, tags []study.Tag) (x any) {
	if len(tags) == 0 {
		return
	}
	b.DivClass("chip-row").R(
		element.ForEach(tags, func(t study.Tag) {
			b.AClass("chip chip-tag", "href", TagURL(t.ID)).T(esc(t.Name))
		}),
	)
	return
}

// renderMarkdownBody writes pre-rendered note HTML straight into the output.
//
// This is the ONE place in the ui package that emits HTML it did not build
// element by element, and it is safe for a specific reason: the string comes
// from study.RenderMarkdown, which runs goldmark with raw-HTML passthrough
// disabled, so anything markup-shaped in the source was already escaped.
// Nothing else may take this path — see escape.go.
func renderMarkdownBody(b *element.Builder, bodyHTML string) (x any) {
	if bodyHTML == "" {
		b.PClass("empty").T("This note has no body yet.")
		return
	}
	_ = b.WriteString(bodyHTML)
	return
}

// refHref points a reference at the right page: a whole-chapter reference
// belongs in the reader, a verse reference on the verse hub.
func refHref(ref bible.Ref, translation string) string {
	if ref.IsChapter() {
		return ReadURL(translation, ref.BookNum, ref.Chapter)
	}
	return VerseURL(ref.BookNum, ref.Chapter, ref.VerseStart) + "?t=" + translation
}

// Plural renders "1 note" / "3 notes" — the 's' rule is enough for every noun
// this app counts.
func Plural(n int, noun string) string {
	if n == 1 {
		return itoa(n) + " " + noun
	}
	return itoa(n) + " " + noun + "s"
}
