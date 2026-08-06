package study

import (
	"strings"
	"testing"
)

// TestRenderMarkdownShortcodes covers the [[G26]] expansion, which is the one
// piece of note rendering this app invented rather than inherited from
// goldmark.
func TestRenderMarkdownShortcodes(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		contains []string
		absent   []string
	}{
		{
			name:     "greek shortcode becomes a lexicon link",
			in:       "The word here is [[G26]].",
			contains: []string{`href="https://www.blueletterbible.org/lexicon/g26/kjv/tr/"`, ">G26<"},
		},
		{
			name: "hebrew shortcode uses the hebrew corpus",
			in:   "[[H430]] opens Genesis.",
			// wlc, not tr — the corpus is chosen from the prefix letter.
			contains: []string{"/lexicon/h430/kjv/wlc/"},
		},
		{
			name:     "lowercase prefix is normalized in the label",
			in:       "[[g26]]",
			contains: []string{">G26<", "/lexicon/g26/"},
		},
		{
			name:     "ordinary markdown still renders",
			in:       "# Heading\n\nSome **bold** text.",
			contains: []string{"<h1>", "<strong>bold</strong>"},
		},
		{
			name: "raw html is not passed through",
			in:   `<script>alert(1)</script>`,
			// goldmark runs without WithUnsafe, so the script never reaches
			// the page. This is the property that makes it safe to write the
			// result with b.WriteString.
			absent: []string{"<script>"},
		},
		{
			name:   "a non-shortcode double bracket is left alone",
			in:     "[[not a strongs number]]",
			absent: []string{"/lexicon/"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := RenderMarkdown(c.in)
			if err != nil {
				t.Fatalf("RenderMarkdown(%q) errored: %v", c.in, err)
			}
			for _, want := range c.contains {
				if !strings.Contains(got, want) {
					t.Errorf("RenderMarkdown(%q) = %q, want it to contain %q", c.in, got, want)
				}
			}
			for _, unwanted := range c.absent {
				if strings.Contains(got, unwanted) {
					t.Errorf("RenderMarkdown(%q) = %q, want it NOT to contain %q", c.in, got, unwanted)
				}
			}
		})
	}
}

func TestExcerpt(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		maxLen int
		want   string
	}{
		{"plain text passes through", "A short thought.", 0, "A short thought."},
		{"heading marker stripped", "## On grace\nmore", 0, "On grace more"},
		{"list marker stripped", "- first\n- second", 0, "first second"},
		{"emphasis stripped", "the **grace** of God", 0, "the grace of God"},
		{"link keeps its text", "see [John 3:16](/verse/43/3/16) here", 0, "see John 3:16 here"},
		{"shortcode keeps its label", "the word [[G26]] here", 0, "the word G26 here"},
		{"blank stays blank", "", 0, ""},
		{"newlines collapse", "one\n\n\ntwo", 0, "one two"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Excerpt(c.in, c.maxLen); got != c.want {
				t.Errorf("Excerpt(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestExcerptTruncates checks the length cap and that the cut lands on a word
// boundary rather than mid-word.
func TestExcerptTruncates(t *testing.T) {
	long := strings.Repeat("word ", 60)

	got := Excerpt(long, 40)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("Excerpt of a long body = %q, want a trailing ellipsis", got)
	}
	if len(got) > 44 { // 40 plus the multi-byte ellipsis
		t.Errorf("Excerpt(%d chars, max 40) = %q (%d bytes), want it near the cap",
			len(long), got, len(got))
	}
	if strings.Contains(got, "wor…") {
		t.Errorf("Excerpt cut mid-word: %q", got)
	}
}

func TestSplitTagNames(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"simple list", "Grace, Covenant", []string{"Grace", "Covenant"}},
		{"whitespace trimmed", "  Grace  ,  Covenant ", []string{"Grace", "Covenant"}},
		{"empties dropped", "Grace,,  ,Covenant", []string{"Grace", "Covenant"}},
		{"internal whitespace collapsed", "New   Covenant", []string{"New Covenant"}},
		// Case-insensitive de-duplication keeping the first spelling is what
		// keeps "grace" and "Grace" from becoming two tags.
		{"case-insensitive dedupe", "grace, Grace, GRACE", []string{"grace"}},
		{"blank yields nothing", "   ", nil},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SplitTagNames(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("SplitTagNames(%q) = %v, want %v", c.in, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("SplitTagNames(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
				}
			}
		})
	}
}

// TestExpand covers the range expansion behind the reader's indicator dots,
// including the whole-chapter convention and the sanity clamp.
func TestExpand(t *testing.T) {
	t.Run("single verse", func(t *testing.T) {
		if got := expand(16, 16); len(got) != 1 || got[0] != 16 {
			t.Errorf("expand(16,16) = %v, want [16]", got)
		}
	})

	t.Run("range is inclusive at both ends", func(t *testing.T) {
		got := expand(16, 18)
		want := []int{16, 17, 18}
		if len(got) != len(want) {
			t.Fatalf("expand(16,18) = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("expand(16,18) = %v, want %v", got, want)
			}
		}
	})

	t.Run("whole chapter maps to the chapter key", func(t *testing.T) {
		if got := expand(0, 0); len(got) != 1 || got[0] != ChapterVerseKey {
			t.Errorf("expand(0,0) = %v, want [%d]", got, ChapterVerseKey)
		}
	})

	t.Run("backwards range is treated as a single verse", func(t *testing.T) {
		if got := expand(18, 16); len(got) != 1 || got[0] != 18 {
			t.Errorf("expand(18,16) = %v, want [18]", got)
		}
	})

	t.Run("absurd end is clamped", func(t *testing.T) {
		// A hand-edited row must not turn into an unbounded loop.
		if got := expand(1, 10_000_000); len(got) != 176 {
			t.Errorf("expand(1,10000000) produced %d verses, want 176", len(got))
		}
	})
}

// TestNoteDraftNormalize covers the title derivation, which is what lets the
// verse hub's quick-add be a single textarea and a button.
func TestNoteDraftNormalize(t *testing.T) {
	cases := []struct {
		name  string
		draft NoteDraft
		want  string
	}{
		{"explicit title wins", NoteDraft{Title: "Grace", BodyMD: "other"}, "Grace"},
		{"title derived from first line", NoteDraft{BodyMD: "On grace\nmore text"}, "On grace"},
		{"heading marker stripped", NoteDraft{BodyMD: "# On grace\nmore"}, "On grace"},
		{"empty everything gets a placeholder", NoteDraft{}, "Untitled note"},
		{"whitespace-only body gets a placeholder", NoteDraft{BodyMD: "   \n  "}, "Untitled note"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := c.draft
			d.normalize()
			if d.Title != c.want {
				t.Errorf("normalize() title = %q, want %q", d.Title, c.want)
			}
		})
	}
}
