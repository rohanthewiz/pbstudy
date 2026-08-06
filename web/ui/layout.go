// Package ui renders pbstudy's HTML using the element builder.
//
// Every page is a Page: a small struct carrying page-specific data plus a
// Render method. Page composes the shell (head, header, nav, footer) around a
// body component, so no page has to know about the stylesheet link, the
// ScriptTagger snippet, or the nav.
package ui

import (
	"strconv"

	"github.com/rohanthewiz/element"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/blb"
)

// Nav identifiers, used to highlight the active tab.
const (
	NavRead     = "read"
	NavNotes    = "notes"
	NavTags     = "tags"
	NavSearch   = "search"
	NavSermons  = "sermons"
	NavSettings = "settings"
)

// Page is the shell every response is rendered into.
type Page struct {
	Title string
	// Active is one of the Nav* constants; empty for pages with no tab.
	Active string
	// Translation drives the ScriptTagger configuration and the search
	// form's hidden field, so popups quote the version being read.
	Translation string
	// Body is the page-specific content.
	Body element.Component
}

// Render satisfies element.Component and emits the whole document.
func (p Page) Render(b *element.Builder) (x any) {
	translation := p.Translation
	if translation == "" {
		translation = bible.DefaultTranslation
	}

	title := p.Title
	if title == "" {
		title = "pbstudy"
	} else {
		title += " · pbstudy"
	}

	b.Html("lang", "en").R(
		b.Head().R(
			b.Meta("charset", "utf-8"),
			b.Meta("name", "viewport", "content", "width=device-width, initial-scale=1"),
			// <title> is RCDATA, so most markup inside it is inert — but
			// a literal "</title>" still closes the element and anything
			// after it lands in the document. Page titles carry note
			// titles and search queries, so this escape is load-bearing,
			// not belt-and-braces.
			b.Title().T(esc(title)),
			b.Link("rel", "stylesheet", "href", "/css/app.css"),

			// ScriptTagger reads BLB.Tagger when it executes, so the config
			// must be assigned before the script tag — assigning it after
			// is silently ignored. The script itself is deferred: if BLB is
			// unreachable the page still renders and every local feature
			// still works, we just lose the hover popups.
			b.Script().T(blb.TaggerConfigJS(translation)),
			b.Script("src", blb.ScriptTaggerURL, "defer", "defer").R(),

			b.Script("src", "/js/app.js", "defer", "defer").R(),
		),
		b.Body().R(
			p.renderHeader(b, translation),
			b.Main().R(
				b.Wrap(func() {
					if p.Body != nil {
						p.Body.Render(b)
					}
				}),
			),
			b.FooterClass("site-footer").R(
				b.T("pbstudy — local study database. "),
				b.A("href", blb.BaseURL, "target", "_blank", "rel", "noopener").
					T("Blue Letter Bible"),
				b.T(" for lexicon and interlinear."),
			),
		),
	)
	return
}

// navItem is one entry in the top nav.
type navItem struct {
	id    string
	label string
	href  string
}

var navItems = []navItem{
	{NavRead, "Read", "/read"},
	{NavNotes, "Notes", "/notes"},
	{NavTags, "Tags", "/tags"},
	{NavSermons, "Sermons", "/sermons"},
	{NavSettings, "Settings", "/settings"},
}

func (p Page) renderHeader(b *element.Builder, translation string) (x any) {
	b.HeaderClass("site-header").R(
		b.AClass("brand", "href", "/").T("pbstudy"),

		b.NavClass("site-nav").R(
			element.ForEach(navItems, func(it navItem) {
				class := ""
				if it.id == p.Active {
					class = "active"
				}
				b.A("class", class, "href", it.href).T(it.label)
			}),
		),

		// A GET form so a search is a bookmarkable URL. The translation
		// rides along hidden so results stay in the version being read.
		//
		// The translation reaches here from a URL path segment. Handlers
		// validate it against the known set, so this escape is the second
		// line rather than the first — but an attribute value built from
		// anything that came off the wire gets escaped regardless of who
		// promised to have checked it.
		b.FormClass("header-search", "method", "get", "action", "/search").R(
			b.Input("type", "hidden", "name", "translation", "value", esc(translation)),
			b.Input("type", "search", "name", "q", "placeholder", "Search or go to John 3:16",
				"data-search-input", "1", "autocomplete", "off"),
			b.Button("type", "submit").T("Go"),
		),
	)
	return
}

// ---- small shared helpers ------------------------------------------------

// ReadURL is the reader link for a chapter.
func ReadURL(translation string, bookNum, chapter int) string {
	return "/read/" + translation + "/" + strconv.Itoa(bookNum) + "/" + strconv.Itoa(chapter)
}

// VerseURL is the verse-hub link. The hub is translation-independent — it
// shows every downloaded translation — so no translation segment.
func VerseURL(bookNum, chapter, verse int) string {
	return "/verse/" + strconv.Itoa(bookNum) + "/" + strconv.Itoa(chapter) + "/" +
		strconv.Itoa(verse)
}

// itoa is a local shorthand; strconv.Itoa reads noisily inside render trees.
func itoa(n int) string { return strconv.Itoa(n) }
