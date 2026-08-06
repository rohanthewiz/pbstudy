package ui

import (
	"strings"

	"github.com/rohanthewiz/element"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/blb"
	"github.com/rohanthewiz/pbstudy/study"
)

// Search scopes. These are URL values, so they are stable strings rather than
// an int enum — a bookmarked ?scope=notes has to keep meaning the same thing.
const (
	ScopeAll       = "all"
	ScopeScripture = "scripture"
	ScopeNotes     = "notes"
)

// NormalizeScope maps a raw ?scope= value onto a known scope, defaulting to
// "all". Anything unrecognized falls back rather than erroring: a scope is a
// filter, and the honest response to a filter nobody understands is to filter
// nothing.
func NormalizeScope(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case ScopeScripture:
		return ScopeScripture
	case ScopeNotes:
		return ScopeNotes
	default:
		return ScopeAll
	}
}

// SearchURL builds a search link, which is what makes every result page
// bookmarkable and every scope tab a plain <a>.
func SearchURL(q, scope, translation string) string {
	return "/search?q=" + urlQueryEscape(q) +
		"&scope=" + urlQueryEscape(scope) +
		"&translation=" + urlQueryEscape(translation)
}

// Search renders the combined search page: scripture text, the user's notes,
// their tags, and the comments on their cross-references.
//
// # Why one page rather than one page per kind
//
// The question a study session actually asks is "where does this come up?",
// and the answer spans scripture and the user's own writing. Splitting them
// across separate pages would mean the user has to already know which half
// holds the answer. Scope narrows the same page when they do know.
//
// Every group is capped and every cap is visible (see the truncation notices).
// A result list that silently stops is worse than no result list, because it
// reads as "there is nothing more".
type Search struct {
	Query       string
	Scope       string
	Translation string
	// Translations is what has been downloaded, for the version picker. A
	// single-translation install gets a hidden field instead of a select.
	Translations []bible.Translation

	// Verses are scripture text hits; VerseLimit is the cap they were fetched
	// under, so the view can say when the list was cut short.
	Verses     []bible.SearchResult
	VerseLimit int

	// Notes, Tags and Xrefs are the study-database side of the results.
	Notes     []study.NoteHit
	NoteLimit int
	Tags      []study.Tag
	Xrefs     []study.CrossRef

	// AnchorRef is set when the query parsed as a reference while searching
	// notes: the results are then the notes anchored to that passage, and this
	// is what they are anchored to.
	AnchorRef *bible.Ref
}

func (s Search) Render(b *element.Builder) (x any) {
	b.H1Class("page-title").T("Search")

	s.renderForm(b)

	if s.Query == "" {
		s.renderHint(b)
		return
	}

	s.renderScopeTabs(b)

	if s.AnchorRef != nil {
		b.PClass("page-sub").R(
			b.T("Notes anchored to "),
			b.A("href", refHref(*s.AnchorRef, s.Translation)).T(esc(s.AnchorRef.String())),
			b.T(". Searching your notes does not jump to a passage — "),
			b.A("href", refHref(*s.AnchorRef, s.Translation)).T("open it directly"),
			b.T(" instead."),
		)
	}

	found := false
	if s.Wants(ScopeScripture) {
		found = s.renderVerses(b) || found
	}
	if s.Wants(ScopeNotes) {
		found = s.renderNotes(b) || found
		found = s.renderTags(b) || found
		found = s.renderXrefs(b) || found
	}

	if !found {
		b.DivClass("notice").R(
			b.Strong().F("Nothing matched %q. ", esc(s.Query)),
			b.T("Try a shorter phrase, or widen the scope."),
		)
	}
	return
}

// Wants reports whether a group of results belongs on the page under the
// current scope.
//
// Exported so the handler can ask the same question before doing the work.
// Scope semantics defined once, in the place that renders them, is what keeps
// a page from querying one thing and displaying another.
func (s Search) Wants(group string) bool {
	return s.Scope == ScopeAll || s.Scope == group
}

// renderForm is the on-page search box.
//
// The header carries one too, but it has no room for scope or version. This
// one is a GET form with every parameter present, so the URL it produces is
// the same URL the scope tabs produce — one canonical shape for a search.
func (s Search) renderForm(b *element.Builder) (x any) {
	b.FormClass("search-form", "method", "get", "action", "/search").R(
		b.Input("type", "search", "name", "q", "value", esc(s.Query),
			"placeholder", "A word, a phrase, or John 3:16",
			"autocomplete", "off", "autofocus", "autofocus"),

		b.Input("type", "hidden", "name", "scope", "value", esc(s.Scope)),

		b.Wrap(func() {
			// One downloaded translation means no choice to offer; the value
			// still has to travel so the search stays in that version.
			if len(s.Translations) < 2 {
				b.Input("type", "hidden", "name", "translation", "value", esc(s.Translation))
				return
			}
			b.Select("name", "translation", "aria-label", "Translation to search").R(
				element.ForEach(s.Translations, func(t bible.Translation) {
					attrs := []string{"value", t.Abbrev, "title", t.Name}
					if t.Abbrev == s.Translation {
						attrs = append(attrs, "selected", "selected")
					}
					b.Option(attrs...).T(upper(t.Abbrev))
				}),
			)
		}),

		b.Button("type", "submit", "class", "btn btn-primary").T("Search"),
	)
	return
}

func (s Search) renderHint(b *element.Builder) (x any) {
	b.PClass("page-sub").R(
		b.T("Search the text of "),
		b.Strong().T(upper(s.Translation)),
		b.T(" and everything you have written — notes, tags, and the comments on "),
		b.T("your cross-references. Type a reference like "),
		b.Code().T("John 3:16"),
		b.T(" to jump straight to it."),
	)
	return
}

// renderScopeTabs offers the three scopes as links, so switching scope keeps
// the query and the version and stays one click with no JavaScript.
func (s Search) renderScopeTabs(b *element.Builder) (x any) {
	tabs := []struct{ id, label string }{
		{ScopeAll, "Everything"},
		{ScopeScripture, "Scripture"},
		{ScopeNotes, "My notes"},
	}

	b.DivClass("scope-tabs").R(
		element.ForEach(tabs, func(t struct{ id, label string }) {
			class := "scope-tab"
			if t.id == s.Scope {
				class += " active"
			}
			b.A("class", class, "href", SearchURL(s.Query, t.id, s.Translation)).T(t.label)
		}),
	)
	return
}

// renderVerses lists scripture hits. Returns whether anything was rendered, so
// the caller can tell an empty page from a full one without re-counting.
func (s Search) renderVerses(b *element.Builder) bool {
	if len(s.Verses) == 0 {
		return false
	}

	b.DivClass("result-group").R(
		b.H2Class("result-heading").R(
			b.T("Scripture"),
			b.SpanClass("result-count").F("%d in %s",
				len(s.Verses), upper(s.Translation)),
		),

		// Results carry our own reference links; ScriptTagger stays out, same
		// as every other container of scripture we render ourselves.
		b.Div("class", "card "+blb.NoTagClass).R(
			element.ForEach(s.Verses, func(r bible.SearchResult) {
				b.DivClass("translation-row").R(
					b.DivClass("translation-tag").R(
						b.A("href", VerseURL(r.BookNum, r.Chapter, r.Num)+"?t="+s.Translation).
							T(shortRef(r.Ref)),
					),
					b.DivClass("translation-body").R(
						highlight(b, r.Body, s.Query),
					),
				)
			}),
		),

		truncationNotice(b, len(s.Verses), s.VerseLimit, "verse"),
	)
	return true
}

func (s Search) renderNotes(b *element.Builder) bool {
	if len(s.Notes) == 0 {
		return false
	}

	b.DivClass("result-group").R(
		b.H2Class("result-heading").R(
			b.T("Notes"),
			b.SpanClass("result-count").T(itoa(len(s.Notes))),
		),

		// No no-tag class: a reference the user typed inside a note is exactly
		// what ScriptTagger should find, in a search result as much as on the
		// note itself.
		b.DivClass("card").R(
			element.ForEach(s.Notes, func(h study.NoteHit) {
				b.DivClass("note-row").R(
					// The title is highlighted too, so a note that matched on
					// its title alone shows why it is here.
					b.AClass("note-title", "href", NoteURL(h.Note.ID)).R(
						highlight(b, h.Note.Title, s.Query),
					),
					b.Wrap(func() {
						if len(h.Note.Refs) == 0 && len(h.Note.Tags) == 0 {
							return
						}
						b.DivClass("chip-row").R(
							element.ForEach(h.Note.Refs, func(r study.NoteRef) {
								b.AClass("chip chip-ref",
									"href", refHref(r.Ref, s.Translation)).
									T(esc(r.Ref.String()))
							}),
							element.ForEach(h.Note.Tags, func(t study.Tag) {
								b.AClass("chip chip-tag", "href", TagURL(t.ID)).T(esc(t.Name))
							}),
						)
					}),
					b.Wrap(func() {
						if h.Snippet == "" {
							return
						}
						b.PClass("note-excerpt").R(
							highlight(b, h.Snippet, s.Query),
						)
					}),
				)
			}),
		),

		truncationNotice(b, len(s.Notes), s.NoteLimit, "note"),
	)
	return true
}

// renderTags surfaces matching topics as chips rather than as rows: a tag is a
// destination, not a result to read, and the chip is the same control the user
// already clicks everywhere else.
func (s Search) renderTags(b *element.Builder) bool {
	if len(s.Tags) == 0 {
		return false
	}

	b.DivClass("result-group").R(
		b.H2Class("result-heading").R(
			b.T("Topics"),
			b.SpanClass("result-count").T(itoa(len(s.Tags))),
		),
		b.DivClass("card").R(
			b.DivClass("chip-row chip-row-large").R(
				element.ForEach(s.Tags, func(t study.Tag) {
					b.AClass("chip chip-tag", "href", TagURL(t.ID),
						"title", esc(t.Descrip)).R(
						b.T(esc(t.Name)),
						b.SpanClass("chip-count").T(itoa(t.NoteCount)),
					)
				}),
			),
		),
	)
	return true
}

// renderXrefs lists correlations whose comment matched.
//
// Both ends are shown with links, unlike the verse hub's one-sided rows: a
// search result is read from outside the link, so neither end is "here".
func (s Search) renderXrefs(b *element.Builder) bool {
	if len(s.Xrefs) == 0 {
		return false
	}

	b.DivClass("result-group").R(
		b.H2Class("result-heading").R(
			b.T("Cross-references"),
			b.SpanClass("result-count").T(itoa(len(s.Xrefs))),
		),
		b.DivClass("card").R(
			element.ForEach(s.Xrefs, func(xr study.CrossRef) {
				b.DivClass("xref-row").R(
					b.AClass("xref-ref", "href", refHref(xr.From, s.Translation)).
						T(esc(xr.From.String())),
					b.SpanClass("xref-arrow").T("→"),
					b.AClass("xref-ref", "href", refHref(xr.To, s.Translation)).
						T(esc(xr.To.String())),
					b.Wrap(func() {
						if xr.Comment == "" {
							return
						}
						b.SpanClass("xref-comment").R(
							highlight(b, xr.Comment, s.Query),
						)
					}),
				)
			}),
		),
	)
	return true
}

// truncationNotice says so when a group hit its cap.
//
// The comparison is >= rather than ==: a limit reached exactly is
// indistinguishable from a limit exceeded, and claiming completeness on the
// boundary is the one case where the notice matters most.
func truncationNotice(b *element.Builder, shown, limit int, noun string) (x any) {
	if limit <= 0 || shown < limit {
		return
	}
	b.PClass("page-sub").F(
		"Showing the first %s. Narrow the query to see the rest.",
		Plural(shown, noun))
	return
}

// shortRef renders "Jn 3:16"-ish labels for the results gutter, using the BLB
// abbreviation since it is the most compact form already in the Book table.
func shortRef(ref bible.Ref) string {
	bk := ref.Book()
	return upper(bk.BLBAbbrev[:1]) + bk.BLBAbbrev[1:] + " " +
		itoa(ref.Chapter) + ":" + itoa(ref.VerseStart)
}

// highlight emits body with every case-insensitive occurrence of q wrapped in
// <mark>.
//
// Two properties worth stating, because both are easy to get wrong:
//
//  1. Matching happens on the raw strings, but every fragment is escaped on
//     its way out (see esc). Escaping AFTER the match is what keeps the
//     offsets honest: escaping first would turn a searched-for "&" into
//     "&amp;" and the index arithmetic below would slice through the middle of
//     an entity. A query of "<script>" therefore highlights literally.
//
//  2. Matching is done on lowercased copies, then sliced out of the ORIGINAL
//     body so the match keeps its real casing. That only works while
//     lowercasing preserves byte offsets, which holds for ASCII but not for
//     every Unicode input. The guard below falls back to unhighlighted text
//     rather than slicing at a wrong offset and corrupting the output.
func highlight(b *element.Builder, body, q string) (x any) {
	lowerBody := strings.ToLower(body)
	lowerQ := strings.ToLower(q)

	if q == "" || len(lowerBody) != len(body) || len(lowerQ) != len(q) {
		b.T(esc(body))
		return
	}

	for {
		i := strings.Index(lowerBody, lowerQ)
		if i < 0 {
			b.T(esc(body))
			return
		}
		if i > 0 {
			b.T(esc(body[:i]))
		}
		b.Ele("mark").T(esc(body[i : i+len(q)]))

		body = body[i+len(q):]
		lowerBody = lowerBody[i+len(q):]
	}
}
