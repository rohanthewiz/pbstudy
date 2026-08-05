package ui

import (
	"github.com/rohanthewiz/element"

	"pbstudy/bible"
	"pbstudy/blb"
)

// VerseHub is the per-verse landing page: the verse in every downloaded
// translation, links out to Blue Letter Bible, and (from Phase 2) the notes
// and cross-references attached to it.
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
}

func (v VerseHub) Render(b *element.Builder) (x any) {
	ref := bible.Ref{
		BookNum:    v.Book.Num,
		Chapter:    v.Chapter,
		VerseStart: v.Verse,
		VerseEnd:   v.Verse,
	}

	b.H1Class("page-title").T(ref.String())
	b.PClass("page-sub").R(
		b.A("href", ReadURL(v.ReadTranslation, v.Book.Num, v.Chapter)+"#v"+itoa(v.Verse)).
			F("Read %s %d in context", v.Book.Name, v.Chapter),
	)

	v.renderTranslations(b)
	v.renderExternalLinks(b)

	// Phase 2 fills these in. They are stubbed rather than omitted so the
	// page's shape is settled and the placeholders document what is coming.
	b.DivClass("card").R(
		b.H2().T("Notes"),
		b.PClass("empty").T("Note-taking arrives in the next phase."),
	)
	b.DivClass("card").R(
		b.H2().T("Cross-references"),
		b.PClass("empty").T("Cross-referencing arrives in the next phase."),
	)
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
				b.DivClass("translation-body").T(vs.Body),
			)
		}),
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
