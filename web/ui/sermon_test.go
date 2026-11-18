package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/element"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/study"
)

// TestSermonBuilderEscapes is the escaping check for the one page that renders
// three separate streams of user-controlled text: the sermon title, a note
// title reached through the outline, and a heading the user typed.
//
// The element builder escapes nothing (see escape.go), so this is the test that
// would catch a b.T() that lost its esc() wrapper.
func TestSermonBuilderEscapes(t *testing.T) {
	hostile := `<img src=x onerror="alert('grace')">`

	view := SermonBuilder{
		Sermon: study.Sermon{
			ID:        "s1",
			Title:     hostile,
			UpdatedAt: time.Now(),
			Outline: []study.Section{
				{ID: "sec1", Kind: study.KindHeading, Text: hostile},
			},
		},
		Rows: []OutlineRow{
			{
				Section: study.Section{ID: "sec1", Kind: study.KindHeading, Text: hostile},
				Label:   hostile,
			},
		},
		Translation: "kjv",
		KnownNotes:  []study.Note{{ID: "n1", Title: hostile}},
		Problem:     hostile,
	}

	b := element.NewBuilder()
	view.Render(b)
	got := b.String()

	if strings.Contains(got, "<img src=x") {
		t.Errorf("SermonBuilder rendered unescaped markup:\n%s", got)
	}
	if !strings.Contains(got, "&lt;img src=x") {
		t.Errorf("SermonBuilder did not escape the hostile text at all:\n%s", got)
	}
	// The note picker puts the same text in an attribute value as well as in
	// element content, and a stray quote there would break out of value="".
	if strings.Contains(got, `onerror="alert`) {
		t.Errorf("SermonBuilder let a quote escape an attribute value:\n%s", got)
	}
}

// TestSermonBuilderHidesDraftingWithoutKey: the drafting controls are absent
// rather than present-and-failing when no API key is configured. A button that
// explains why it cannot work is worse than no button.
func TestSermonBuilderHidesDraftingWithoutKey(t *testing.T) {
	base := SermonBuilder{
		Sermon:      study.Sermon{ID: "s1", Title: "Grace", UpdatedAt: time.Now()},
		Rows:        []OutlineRow{{Section: study.Section{ID: "a", Kind: study.KindPoint}, Label: "a point"}},
		Translation: "kjv",
	}

	off := element.NewBuilder()
	base.Render(off)
	if strings.Contains(off.String(), `action="/sermons/s1/draft"`) {
		t.Error("the draft form was rendered with no API key configured")
	}
	if !strings.Contains(off.String(), "ANTHROPIC_API_KEY") {
		t.Error("the page did not say how to turn drafting on")
	}

	base.AIEnabled = true
	on := element.NewBuilder()
	base.Render(on)
	if !strings.Contains(on.String(), `action="/sermons/s1/draft"`) {
		t.Error("the draft form was missing with an API key configured")
	}

	// The exports do not depend on the key: the outline is the document.
	for _, want := range []string{"/sermons/s1/export.md", "/sermons/s1/export.html"} {
		if !strings.Contains(off.String(), want) {
			t.Errorf("export link %q missing when drafting is off", want)
		}
	}
}

// TestSermonBuilderStreamPanel: the panel carries its endpoint in a data
// attribute and appears only while streaming, which is the whole contract with
// app.js.
func TestSermonBuilderStreamPanel(t *testing.T) {
	view := SermonBuilder{
		Sermon:      study.Sermon{ID: "s1", Title: "Grace", UpdatedAt: time.Now()},
		Rows:        []OutlineRow{{Section: study.Section{ID: "a", Kind: study.KindPoint}, Label: "a point"}},
		Translation: "kjv",
		AIEnabled:   true,
	}

	quiet := element.NewBuilder()
	view.Render(quiet)
	if strings.Contains(quiet.String(), "data-draft-url") {
		t.Error("the stream panel was rendered outside a drafting request")
	}

	view.Streaming = true
	live := element.NewBuilder()
	view.Render(live)
	if !strings.Contains(live.String(), `data-draft-url="/sermons/s1/draft/stream"`) {
		t.Errorf("the stream panel did not carry its endpoint:\n%s", live.String())
	}
}

// TestSectionHref points each kind at the right destination — and headings and
// points at none, since they exist nowhere but the outline.
func TestSectionHref(t *testing.T) {
	cases := []struct {
		sec  study.Section
		want string
	}{
		{study.Section{Kind: study.KindNote, NoteID: "n1"}, "/notes/n1"},
		{study.Section{Kind: study.KindHeading, Text: "The problem"}, ""},
		{study.Section{Kind: study.KindPoint, Text: "a point"}, ""},
	}

	for _, c := range cases {
		if got := SectionHref(c.sec, "kjv"); got != c.want {
			t.Errorf("SectionHref(%+v) = %q, want %q", c.sec, got, c.want)
		}
	}

	// A whole-chapter passage belongs in the reader, a single verse on the verse
	// hub — the same split refHref makes everywhere else.
	chapter := SectionHref(study.Section{Kind: study.KindPassage,
		Ref: bible.Ref{BookNum: 43, Chapter: 3}}, "kjv")
	if chapter != "/read/kjv/43/3" {
		t.Errorf("SectionHref(chapter) = %q, want the reader", chapter)
	}

	verse := SectionHref(study.Section{Kind: study.KindPassage,
		Ref: bible.Ref{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 16}}, "kjv")
	if verse != "/verse/43/3/16?t=kjv" {
		t.Errorf("SectionHref(verse) = %q, want the verse hub", verse)
	}
}

// TestSermonBuilderSectionEditing covers the in-place edit controls: which
// rows offer a pencil, and what the row being edited turns into.
func TestSermonBuilderSectionEditing(t *testing.T) {
	rows := []OutlineRow{
		{Section: study.Section{ID: "sec-h", Kind: study.KindHeading, Text: "The problem"}, Label: "The problem"},
		{Section: study.Section{ID: "sec-n", Kind: study.KindNote, NoteID: "n1"}, Label: "A note", Href: "/notes/n1"},
	}
	view := SermonBuilder{
		Sermon:      study.Sermon{ID: "s1", Title: "T", UpdatedAt: time.Now()},
		Rows:        rows,
		Translation: "kjv",
	}

	render := func(v SermonBuilder) string {
		b := element.NewBuilder()
		v.Render(b)
		return b.String()
	}

	// Nothing open: the heading has a pencil that names it, the note does not
	// (a note section is edited by editing the note), and no form is rendered.
	closed := render(view)
	// The link must not carry a fragment: browsers skip autofocus on a URL
	// that has one, and the form's input depends on autofocus.
	if !strings.Contains(closed, `href="/sermons/s1?edit=sec-h"`) {
		t.Errorf("no fragment-free edit link for the heading row:\n%s", closed)
	}
	if strings.Contains(closed, "?edit=sec-n") {
		t.Errorf("a note row was offered an edit link:\n%s", closed)
	}
	if strings.Contains(closed, "/sections/sec-h/edit") {
		t.Errorf("an edit form was rendered with no row open:\n%s", closed)
	}
	// Each row is addressable, which is what the fragment in those links hits.
	if !strings.Contains(closed, `id="sec-sec-h"`) || !strings.Contains(closed, `id="sec-sec-n"`) {
		t.Errorf("outline rows are missing their anchors:\n%s", closed)
	}

	// The heading open: a form posting to its edit route, holding the text the
	// handler supplied (escaped), and no pencil on the row that is already open.
	view.EditingID = "sec-h"
	view.EditingText = `The "real" problem`
	open := render(view)
	if !strings.Contains(open, `action="/sermons/s1/sections/sec-h/edit"`) {
		t.Errorf("no edit form for the open row:\n%s", open)
	}
	if !strings.Contains(open, `value="The &#34;real&#34; problem"`) {
		t.Errorf("the edit input does not hold the escaped text:\n%s", open)
	}
	if strings.Contains(open, "?edit=sec-h") {
		t.Errorf("the open row still shows its edit link:\n%s", open)
	}

	// ?edit= naming a note section must not open a form the server refuses.
	view.EditingID = "sec-n"
	if got := render(view); strings.Contains(got, "/sections/sec-n/edit") {
		t.Errorf("a note row was rendered as an edit form:\n%s", got)
	}
}
