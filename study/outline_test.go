package study

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/rohanthewiz/pbstudy/bible"
)

// john316 and rom58 are the two references these tests cite.
var (
	john316 = bible.Ref{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 16}
	john317 = bible.Ref{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 17}
	rom5    = bible.Ref{BookNum: 45, Chapter: 5}
)

// TestFormatOutline pins the assembled document's shape.
//
// This is the highest-value test in the package: the same Markdown is the .md
// download, the input to the HTML export, and the payload the AI drafter is
// given. A change to the format here changes all three, so the format is worth
// stating explicitly rather than discovering from an export.
func TestFormatOutline(t *testing.T) {
	sermon := Sermon{
		Title: "The love of God",
		Outline: []Section{
			{ID: "1", Kind: KindHeading, Text: "The problem"},
			{ID: "2", Kind: KindPoint, Text: "We were dead"},
			{ID: "3", Kind: KindPoint, Text: "We could not fix it"},
			{ID: "4", Kind: KindPassage, Ref: john316},
			{ID: "5", Kind: KindPassage, Ref: john317},
			{ID: "6", Kind: KindNote, NoteID: "n1"},
		},
	}

	src := outlineSources{
		translation: "kjv",
		notes: map[string]Note{
			"n1": {
				ID:     "n1",
				Title:  "Demonstrated, not declared",
				BodyMD: "God **showed** his love.",
				Refs:   []NoteRef{{Ref: rom5}},
			},
		},
		verses: map[bible.Ref][]bible.Verse{
			john316: {{Num: 16, Body: "For God so loved the world..."}},
			john317: {
				{Num: 16, Body: "For God so loved the world..."},
				{Num: 17, Body: "For God sent not his Son to condemn..."},
			},
		},
		truncated: map[bible.Ref]bool{},
	}

	got := formatOutline(sermon, src)

	for _, want := range []string{
		"# The love of God",
		"## The problem",
		"- We were dead\n- We could not fix it\n", // consecutive points stay one list
		"> **John 3:16** (KJV)",
		"> For God so loved the world...",
		"> **John 3:16-17** (KJV)",
		"> **16** For God so loved", // numbered once the range spans verses
		"**17** For God sent not",   // ...and both verses in one paragraph
		"### Demonstrated, not declared",
		"*Romans 5*", // the note's own anchors, above its body
		"God **showed** his love.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatOutline() missing %q\n--- got ---\n%s", want, got)
		}
	}

	// A single-verse citation must not repeat its number in the body: the
	// heading line above it already says which verse this is.
	if strings.Contains(got, "> **16** For God so loved the world...\n\n> **John 3:16-17**") {
		t.Error("formatOutline() numbered a single-verse passage")
	}
}

// TestFormatOutlineMarksMissingMaterial is the "do not silently drop it" rule.
//
// A deleted note or an uncached passage has to be visible in the document,
// because the same document is handed to a model that must not invent the
// missing text — and to a preacher who needs to know what to go and fix.
func TestFormatOutlineMarksMissingMaterial(t *testing.T) {
	sermon := Sermon{
		Title: "Gaps",
		Outline: []Section{
			{ID: "1", Kind: KindNote, NoteID: "gone"},
			{ID: "2", Kind: KindPassage, Ref: john316},
			{ID: "3", Kind: "sonnet", Text: "from a newer pbstudy"},
		},
	}
	src := outlineSources{
		translation: "asv",
		notes:       map[string]Note{},
		verses:      map[bible.Ref][]bible.Verse{john316: nil},
		truncated:   map[bible.Ref]bool{},
	}

	got := formatOutline(sermon, src)

	for _, want := range []string{
		"*A note referenced here is no longer available.*",
		"> *No ASV text is cached for this passage.*",
		"*Unrecognized outline section (sonnet).*",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("formatOutline() missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatOutlineStatesTruncation covers the whole-chapter cap: a section that
// was cut short must say so in the document rather than silently ending early.
func TestFormatOutlineStatesTruncation(t *testing.T) {
	sermon := Sermon{
		Title:   "Long",
		Outline: []Section{{ID: "1", Kind: KindPassage, Ref: rom5}},
	}
	src := outlineSources{
		translation: "kjv",
		verses:      map[bible.Ref][]bible.Verse{rom5: {{Num: 1, Body: "Therefore..."}}},
		truncated:   map[bible.Ref]bool{rom5: true},
	}

	got := formatOutline(sermon, src)
	if !strings.Contains(got, "Only the first 60 verses") {
		t.Errorf("formatOutline() did not state the truncation:\n%s", got)
	}
}

// TestFormatOutlineEmpty: an outline with nothing in it still produces a
// document, because the export links are on the page before anything is added.
func TestFormatOutlineEmpty(t *testing.T) {
	got := formatOutline(Sermon{Title: "Nothing yet"}, outlineSources{})

	if !strings.Contains(got, "# Nothing yet") {
		t.Errorf("formatOutline() lost the title:\n%s", got)
	}
	if !strings.Contains(got, "*This outline is empty.*") {
		t.Errorf("formatOutline() did not say the outline was empty:\n%s", got)
	}
}

// TestOutlineRoundTrip checks the JSONB encode/decode pair, including the two
// cases the decoder is expected to repair rather than reject.
func TestOutlineRoundTrip(t *testing.T) {
	in := []Section{
		{ID: "a", Kind: KindHeading, Text: `He said "grace"` + "\nand meant it"},
		{ID: "b", Kind: KindPassage, Ref: john317},
		{ID: "c", Kind: KindNote, NoteID: "n1"},
	}

	encoded, err := encodeOutline(in)
	if err != nil {
		t.Fatalf("encodeOutline() error: %v", err)
	}

	out, err := decodeOutline(sql.NullString{String: encoded, Valid: true})
	if err != nil {
		t.Fatalf("decodeOutline() error: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("decodeOutline() returned %d sections, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("section %d round-tripped as %+v, want %+v", i, out[i], in[i])
		}
	}

	// Only the passage section carries a "ref": omitzero keeps the zero Ref out
	// of the heading and note sections entirely. omitempty would not — it has no
	// effect on a struct value, which is the whole reason for the tag.
	if n := strings.Count(encoded, `"ref"`); n != 1 {
		t.Errorf("encodeOutline() wrote %d refs, want 1: %s", n, encoded)
	}
	// And the stored key names are the app's lower-camel wire names, not Go's
	// field names — the sync engine will read these files.
	if !strings.Contains(encoded, `"bookNum":43`) {
		t.Errorf("encodeOutline() did not use the wire field names: %s", encoded)
	}
}

// TestDecodeOutlineTolerance: an outline column that is NULL, empty or a JSON
// null is an empty outline, not an error. Rows can arrive that way from a sync
// import or from before this feature existed, and they must still open.
func TestDecodeOutlineTolerance(t *testing.T) {
	cases := []sql.NullString{
		{},
		{String: "", Valid: true},
		{String: "   ", Valid: true},
		{String: "null", Valid: true},
		{String: "[]", Valid: true},
	}

	for _, c := range cases {
		got, err := decodeOutline(c)
		if err != nil {
			t.Errorf("decodeOutline(%+v) error: %v", c, err)
		}
		if len(got) != 0 {
			t.Errorf("decodeOutline(%+v) = %d sections, want 0", c, len(got))
		}
	}

	// Sections that arrived without an id get one, so their edit buttons work.
	got, err := decodeOutline(sql.NullString{
		String: `[{"kind":"point","text":"no id here"}]`, Valid: true})
	if err != nil {
		t.Fatalf("decodeOutline() error: %v", err)
	}
	if len(got) != 1 || got[0].ID == "" {
		t.Errorf("decodeOutline() did not mint a missing section id: %+v", got)
	}

	// Genuinely broken JSON is still an error — it means the column holds
	// something that is not an outline, which is worth reporting.
	if _, err := decodeOutline(sql.NullString{String: "{not json", Valid: true}); err == nil {
		t.Error("decodeOutline() accepted malformed JSON")
	}
}

// TestValidateSection covers the form-input gate for each kind.
func TestValidateSection(t *testing.T) {
	cases := []struct {
		name string
		sec  Section
		ok   bool
	}{
		{"a heading needs text", Section{Kind: KindHeading}, false},
		{"a heading with text", Section{Kind: KindHeading, Text: "The problem"}, true},
		{"a whitespace heading is empty", Section{Kind: KindHeading, Text: "   "}, false},
		{"a point needs text", Section{Kind: KindPoint}, false},
		{"a passage needs a real book", Section{Kind: KindPassage}, false},
		{"a passage past the canon", Section{Kind: KindPassage,
			Ref: bible.Ref{BookNum: 99, Chapter: 1}}, false},
		{"a valid passage", Section{Kind: KindPassage, Ref: john316}, true},
		{"a note needs an id", Section{Kind: KindNote}, false},
		{"a note with an id", Section{Kind: KindNote, NoteID: "n1"}, true},
		{"an unknown kind", Section{Kind: "sonnet", Text: "x"}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSection(c.sec)
			if c.ok && err != nil {
				t.Errorf("ValidateSection() rejected a valid section: %v", err)
			}
			if !c.ok && err == nil {
				t.Error("ValidateSection() accepted an invalid section")
			}
		})
	}
}

// TestFileSlug is the download-filename allow-list.
//
// The slug lands inside a Content-Disposition header, so the test that matters
// is the hostile one: a title carrying a quote, a newline or a path separator
// must not be able to put any of them in the header.
func TestFileSlug(t *testing.T) {
	cases := map[string]string{
		"The love of God":                "the-love-of-god",
		"  Grace!  Abounds?  ":           "grace-abounds",
		`"; rm -rf /`:                    "rm-rf",
		"a\nb":                           "a-b",
		"../../etc/passwd":               "etc-passwd",
		"":                               "sermon",
		"!!!":                            "sermon",
		"1 John 2:1":                     "1-john-2-1",
		"Ünicode is dropped, ascii kept": "nicode-is-dropped-ascii-kept",
	}

	for in, want := range cases {
		if got := FileSlug(in); got != want {
			t.Errorf("FileSlug(%q) = %q, want %q", in, got, want)
		}
	}

	// Length is capped, and the cap never leaves a trailing hyphen behind.
	long := FileSlug(strings.Repeat("grace ", 40))
	if len(long) > 60 {
		t.Errorf("FileSlug() returned %d characters, want <= 60", len(long))
	}
	if strings.HasSuffix(long, "-") || strings.HasPrefix(long, "-") {
		t.Errorf("FileSlug() left a bare hyphen at an end: %q", long)
	}
}

// TestExportHTMLIsSelfContained: the export leaves the app, so it must carry
// its own styles and must not reference anything the app serves.
func TestExportHTMLIsSelfContained(t *testing.T) {
	got, err := ExportHTML(`Grace & "truth"`, "# Grace\n\nHe came.\n")
	if err != nil {
		t.Fatalf("ExportHTML() error: %v", err)
	}

	if strings.Contains(got, "/css/") || strings.Contains(got, "<link") {
		t.Errorf("ExportHTML() referenced an external stylesheet:\n%s", got)
	}
	if !strings.Contains(got, "<style>") {
		t.Error("ExportHTML() shipped no styles")
	}
	if !strings.Contains(got, "<h1>Grace</h1>") {
		t.Error("ExportHTML() did not render the markdown")
	}
	// The title is user-supplied and lands in <title>.
	if strings.Contains(got, `Grace & "truth"`) {
		t.Errorf("ExportHTML() left the title unescaped:\n%s", got)
	}
	if !strings.Contains(got, "Grace &amp; &#34;truth&#34;") {
		t.Errorf("ExportHTML() did not escape the title as expected:\n%s", got)
	}
}
