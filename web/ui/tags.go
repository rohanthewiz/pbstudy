package ui

import (
	"github.com/rohanthewiz/element"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/study"
)

// TagURL is the permalink for a tag's topical study page.
func TagURL(id string) string { return "/tags/" + id }

// TagsList is the /tags index.
//
// Tags have no create form of their own. They come into existence by being
// typed into a note's tag field, which is the only moment a tag is actually
// wanted — a tag with no notes is not a topic, it is an intention.
type TagsList struct {
	Tags []study.Tag
}

func (l TagsList) Render(b *element.Builder) (x any) {
	b.H1Class("page-title").T("Tags")

	if len(l.Tags) == 0 {
		b.DivClass("notice").R(
			b.Strong().T("No tags yet. "),
			b.T("Tags are created by typing them into a note's "),
			b.Strong().T("Tags"),
			b.T(" field. Each one then collects every note carrying it — "),
			b.T("a topical study assembled out of what you have already written."),
		)
		return
	}

	b.PClass("page-sub").T(Plural(len(l.Tags), "tag") + ".")

	b.DivClass("card").R(
		b.DivClass("chip-row chip-row-large").R(
			element.ForEach(l.Tags, func(t study.Tag) {
				b.AClass("chip chip-tag", "href", TagURL(t.ID),
					"title", esc(t.Descrip)).R(
					b.T(esc(t.Name)),
					b.SpanClass("chip-count").T(itoa(t.NoteCount)),
				)
			}),
		),
	)
	return
}

// TagPage is one tag's topical study: every note carrying it, whatever passage
// each is anchored to.
type TagPage struct {
	Tag   study.Tag
	Notes []study.Note
	// Passages are the distinct anchors across those notes, in canonical
	// order — the topic's scripture reading list, assembled from writing that
	// was never organised around it.
	Passages    []bible.Ref
	Translation string
}

func (p TagPage) Render(b *element.Builder) (x any) {
	b.H1Class("page-title").T(esc(p.Tag.Name))

	if p.Tag.Descrip != "" {
		b.PClass("page-sub").T(esc(p.Tag.Descrip))
	} else {
		b.PClass("page-sub").T(Plural(len(p.Notes), "note") + " carry this tag.")
	}

	p.renderDescripForm(b)
	p.renderPassages(b)

	if len(p.Notes) == 0 {
		b.PClass("empty").T(
			"No notes carry this tag any more. It will disappear from the list once deleted.")
	} else {
		b.DivClass("card").R(
			element.ForEach(p.Notes, func(n study.Note) {
				renderNoteSummary(b, n)
			}),
		)
	}

	p.renderDangerZone(b)
	return
}

// renderPassages lists the scripture this topic touches.
//
// Above the notes, not below them: the passages are the shortest answer to
// "what does my study of this topic actually cover", and they are what a
// sermon or a teaching gets built from. The notes underneath are the working
// out.
func (p TagPage) renderPassages(b *element.Builder) (x any) {
	if len(p.Passages) == 0 {
		return
	}

	b.DivClass("result-group").R(
		b.H2Class("result-heading").R(
			b.T("Passages"),
			b.SpanClass("result-count").T(itoa(len(p.Passages))),
		),
		b.DivClass("card").R(
			b.DivClass("chip-row chip-row-large").R(
				element.ForEach(p.Passages, func(ref bible.Ref) {
					b.AClass("chip chip-ref", "href", refHref(ref, p.Translation)).
						T(esc(ref.String()))
				}),
			),
		),
	)
	return
}

// renderDescripForm lets the user say what the tag is for.
//
// Behind a disclosure because it is a rare action, and inline rather than on a
// separate edit page because it is a single field — a whole page round trip
// for one text input is the kind of ceremony that stops people from writing
// the description at all.
func (p TagPage) renderDescripForm(b *element.Builder) (x any) {
	b.DetailsClass("inline-edit").R(
		b.Summary().T("Describe this tag"),
		b.Form("method", "post", "action", TagURL(p.Tag.ID)+"/descrip").R(
			b.Input("type", "text", "name", "descrip", "value", esc(p.Tag.Descrip),
				"placeholder", "What this topic covers", "autocomplete", "off"),
			b.Button("type", "submit", "class", "btn").T("Save"),
		),
	)
	return
}

func (p TagPage) renderDangerZone(b *element.Builder) (x any) {
	b.DetailsClass("danger-zone").R(
		b.Summary().T("Delete this tag"),
		b.PClass("page-sub").T(
			"The notes are kept — only the tag and its links to them are removed."),
		b.Form("method", "post", "action", TagURL(p.Tag.ID)+"/delete").R(
			b.Button("type", "submit", "class", "btn btn-danger").T("Delete tag"),
		),
	)
	return
}
