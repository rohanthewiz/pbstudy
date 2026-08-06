package ui

import (
	"github.com/rohanthewiz/element"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/blb"
	"github.com/rohanthewiz/pbstudy/study"
)

// VerseHub is the per-verse landing page: the verse in every downloaded
// translation, the notes and cross-references attached to it, forms to add
// more, and links out to Blue Letter Bible.
//
// It is intentionally translation-independent — the whole point of landing on
// a verse is to see it from several angles at once.
type VerseHub struct {
	Book    *bible.Book
	Chapter int
	Verse   int
	// Renderings is the same verse across translations, in preference order.
	Renderings []bible.Verse
	// ReadTranslation is the translation the user arrived from, used for
	// the "back to chapter" link and the BLB deep links.
	ReadTranslation string

	// Notes are the live notes whose anchors cover this verse.
	Notes []study.Note
	// XrefsOut are correlations drawn from this verse; XrefsIn are ones drawn
	// to it from elsewhere. Both are shown, because a correlation the user
	// recorded while reading Romans is exactly what they want surfaced when
	// they later land in Genesis.
	XrefsOut []study.CrossRef
	XrefsIn  []study.CrossRef

	// KnownTags feeds the quick-add form's tag autocomplete.
	KnownTags []study.Tag
	// Problem reports a rejected quick-add, echoed back above the form.
	Problem string
}

func (v VerseHub) Render(b *element.Builder) (x any) {
	ref := bible.Ref{
		BookNum:    v.Book.Num,
		Chapter:    v.Chapter,
		VerseStart: v.Verse,
		VerseEnd:   v.Verse,
	}

	b.H1Class("page-title").T(esc(ref.String()))
	b.PClass("page-sub").R(
		b.A("href", ReadURL(v.ReadTranslation, v.Book.Num, v.Chapter)+"#v"+itoa(v.Verse)).
			F("Read %s %d in context", esc(v.Book.Name), v.Chapter),
	)

	if v.Problem != "" {
		b.DivClass("notice").R(
			b.Strong().T("Not saved. "),
			b.T(esc(v.Problem)),
		)
	}

	v.renderTranslations(b)
	v.renderNotes(b, ref)
	v.renderXrefs(b, ref)
	v.renderExternalLinks(b)
	return
}

func (v VerseHub) renderTranslations(b *element.Builder) (x any) {
	if len(v.Renderings) == 0 {
		b.DivClass("notice").R(
			b.Strong().T("No text cached for this verse. "),
			b.T("Run "),
			b.Code().T("pbstudy download kjv"),
			b.T(" to populate the scripture cache."),
		)
		return
	}

	// Scripture we render ourselves is off-limits to ScriptTagger, same
	// reasoning as the reader: it would re-link text that already carries
	// our own links.
	b.Div("class", "card "+blb.NoTagClass).R(
		element.ForEach(v.Renderings, func(vs bible.Verse) {
			b.DivClass("translation-row").R(
				b.DivClass("translation-tag").T(upper(vs.Translation)),
				b.DivClass("translation-body").T(esc(vs.Body)),
			)
		}),
	)
	return
}

// renderNotes lists the notes on this verse and offers the quick-add form.
//
// The quick-add posts to the same /notes endpoint the full editor uses, with
// the reference pre-filled and a return URL — one create path, two entrances.
// A second endpoint that did "the same thing but from the hub" is how the two
// drift apart.
func (v VerseHub) renderNotes(b *element.Builder, ref bible.Ref) (x any) {
	b.DivClass("card").R(
		b.DivClass("page-head").R(
			b.H2().T("Notes"),
			b.A("class", "btn", "href",
				"/notes/new?refs="+urlQueryEscape(ref.String())).T("Full editor"),
		),

		b.Wrap(func() {
			if len(v.Notes) == 0 {
				b.PClass("empty").T("Nothing written on this verse yet.")
				return
			}
			element.ForEach(v.Notes, func(n study.Note) {
				renderNoteSummary(b, n)
			})
		}),

		v.renderQuickNote(b, ref),
	)
	return
}

func (v VerseHub) renderQuickNote(b *element.Builder, ref bible.Ref) (x any) {
	returnTo := VerseURL(v.Book.Num, v.Chapter, v.Verse) + "?t=" + v.ReadTranslation

	b.DetailsClass("quick-add").R(
		b.Summary().F("Add a note on %s", esc(ref.String())),
		b.Form("method", "post", "action", "/notes").R(
			// The anchor and the destination travel as hidden fields so this
			// form needs no JavaScript and no special-case handler.
			b.Input("type", "hidden", "name", "refs", "value", esc(ref.String())),
			b.Input("type", "hidden", "name", "return", "value", esc(returnTo)),

			b.TextArea("name", "body", "rows", "6", "spellcheck", "true",
				"placeholder", "Markdown. [[G26]] links the lexicon.").R(),

			b.DivClass("field-row").R(
				b.Input("type", "text", "name", "tags", "placeholder", "Tags, comma separated",
					"autocomplete", "off", "list", "known-tags"),
				b.Button("type", "submit", "class", "btn btn-primary").T("Save note"),
			),

			b.Wrap(func() {
				if len(v.KnownTags) == 0 {
					return
				}
				b.DataList("id", "known-tags").R(
					element.ForEach(v.KnownTags, func(t study.Tag) {
						b.Option("value", esc(t.Name)).R()
					}),
				)
			}),
		),
	)
	return
}

// renderXrefs shows both directions of correlation and the form to add one.
func (v VerseHub) renderXrefs(b *element.Builder, ref bible.Ref) (x any) {
	b.DivClass("card").R(
		b.H2().T("Cross-references"),

		b.Wrap(func() {
			if len(v.XrefsOut) == 0 && len(v.XrefsIn) == 0 {
				b.PClass("empty").T("No correlations recorded for this verse.")
				return
			}
			v.renderXrefGroup(b, "From here", v.XrefsOut)
			v.renderXrefGroup(b, "To here", v.XrefsIn)
		}),

		v.renderQuickXref(b, ref),
	)
	return
}

// renderXrefGroup renders one direction. The arrow glyph carries the direction
// visually so the two groups are distinguishable at a glance even when the
// headings scroll out of view.
func (v VerseHub) renderXrefGroup(b *element.Builder, heading string, xrefs []study.CrossRef) (x any) {
	if len(xrefs) == 0 {
		return
	}

	arrow := "→"
	if xrefs[0].Inbound() {
		arrow = "←"
	}

	b.DivClass("xref-group").R(
		b.H3Class("xref-heading").T(heading),
		element.ForEach(xrefs, func(xr study.CrossRef) {
			other := xr.Other()
			b.DivClass("xref-row").R(
				b.SpanClass("xref-arrow").T(arrow),
				b.AClass("xref-ref", "href", refHref(other, v.ReadTranslation)).
					T(esc(other.String())),
				b.Wrap(func() {
					if xr.Comment != "" {
						b.SpanClass("xref-comment").T(esc(xr.Comment))
					}
				}),
				// Deleting is a GET-free, one-button form so it cannot be
				// triggered by a prefetch or a crawler following links.
				b.FormClass("xref-delete", "method", "post",
					"action", "/xrefs/"+xr.ID+"/delete").R(
					b.Input("type", "hidden", "name", "return",
						"value", esc(VerseURL(v.Book.Num, v.Chapter, v.Verse)+
							"?t="+v.ReadTranslation)),
					b.Button("type", "submit", "class", "btn-link",
						"title", "Remove this cross-reference").T("×"),
				),
			)
		}),
	)
	return
}

func (v VerseHub) renderQuickXref(b *element.Builder, ref bible.Ref) (x any) {
	returnTo := VerseURL(v.Book.Num, v.Chapter, v.Verse) + "?t=" + v.ReadTranslation

	b.DetailsClass("quick-add").R(
		b.Summary().F("Link %s to another passage", esc(ref.String())),
		b.Form("method", "post", "action", "/xrefs").R(
			b.Input("type", "hidden", "name", "from", "value", esc(ref.String())),
			b.Input("type", "hidden", "name", "return", "value", esc(returnTo)),

			b.DivClass("field-row").R(
				b.Input("type", "text", "name", "to", "placeholder", "Genesis 3:15",
					"autocomplete", "off", "required", "required"),
				b.Input("type", "text", "name", "comment",
					"placeholder", "Why they connect (optional)", "autocomplete", "off"),
				b.Button("type", "submit", "class", "btn btn-primary").T("Link"),
			),
		),
	)
	return
}

func (v VerseHub) renderExternalLinks(b *element.Builder) (x any) {
	b.DivClass("card").R(
		b.H2().T("Study tools"),
		b.PClass("page-sub").T("Original languages and commentary at Blue Letter Bible."),
		b.DivClass("link-row").R(
			b.A("class", "btn", "target", "_blank", "rel", "noopener",
				"href", blb.VerseURL(v.ReadTranslation, v.Book.Num, v.Chapter, v.Verse)).
				T("Verse page"),
			b.A("class", "btn", "target", "_blank", "rel", "noopener",
				"href", blb.InterlinearURL(v.ReadTranslation, v.Book.Num, v.Chapter, v.Verse)).
				T("Interlinear"),
			b.A("class", "btn", "target", "_blank", "rel", "noopener",
				"href", blb.LexiconURL(defaultLexiconEntry(v.Book))).
				T("Lexicon"),
		),
	)
	return
}

// defaultLexiconEntry picks a sensible lexicon landing point for the book's
// language: G26 (agape) for New Testament books, H430 (elohim) for the Old.
// The Lexicon button is a jumping-off point, not a lookup — a real lookup
// comes from a [[G26]] shortcode inside a note.
func defaultLexiconEntry(bk *bible.Book) string {
	if bk.Testament == bible.OldTestament {
		return "H430"
	}
	return "G26"
}
