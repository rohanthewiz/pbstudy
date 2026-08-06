package study

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/pbstudy/bible"
)

// TestSnippet covers the windowing arithmetic, which is the only part of
// search that can produce visibly wrong output without erroring.
func TestSnippet(t *testing.T) {
	// A body long enough that every window is a real cut, with the match
	// deliberately placed late so the "slide the window back" path is used.
	long := strings.Repeat("padding words here ", 20) + "the covenant of grace " +
		strings.Repeat("more words follow ", 20)

	cases := []struct {
		name     string
		source   string
		query    string
		width    int
		contains []string
		absent   []string
	}{
		{
			name:     "short body is returned whole and unmarked",
			source:   "Grace abounds.",
			query:    "grace",
			width:    200,
			contains: []string{"Grace abounds."},
			absent:   []string{"…"},
		},
		{
			name:     "window centres on the match",
			source:   long,
			query:    "covenant",
			width:    60,
			contains: []string{"covenant"},
		},
		{
			name:   "window is capped near the requested width",
			source: long,
			query:  "covenant",
			width:  60,
			// The ellipses are added outside the window, so allow for them.
			contains: []string{"…"},
		},
		{
			name:   "markdown markup is stripped from the window",
			source: "# Heading\n\n" + long + "\n\n[a link](https://example.com/x)",
			query:  "covenant",
			width:  80,
			absent: []string{"#", "https://example.com"},
		},
		{
			name:   "a match only inside markup falls back to an excerpt",
			source: "See [the lexicon](https://example.com/g26) " + long,
			query:  "example.com",
			width:  60,
			// The URL was stripped, so there is no honest window to show —
			// the leading excerpt is what comes back.
			absent: []string{"example.com"},
		},
		{
			name:     "an empty query yields a leading excerpt",
			source:   long,
			query:    "",
			width:    60,
			contains: []string{"padding words"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Snippet(c.source, c.query, c.width)

			for _, want := range c.contains {
				if !strings.Contains(got, want) {
					t.Errorf("Snippet() = %q, want it to contain %q", got, want)
				}
			}
			for _, unwanted := range c.absent {
				if strings.Contains(got, unwanted) {
					t.Errorf("Snippet() = %q, want it NOT to contain %q", got, unwanted)
				}
			}
			// The ellipses are decoration outside the window, so the budget is
			// the width plus their bytes. This is the guard that would catch a
			// window that ran away.
			if limit := c.width + len("……"); len(got) > limit && len(c.source) > limit {
				t.Errorf("Snippet() len = %d, want <= %d: %q", len(got), limit, got)
			}
		})
	}
}

// TestSnippetNeverSplitsARune guards the offset arithmetic against non-ASCII
// input, where lowercasing can change byte length and a naive slice would cut
// a rune in half.
func TestSnippetNeverSplitsARune(t *testing.T) {
	source := strings.Repeat("İstanbul ağrı ", 40) + "grace" + strings.Repeat(" more", 40)

	for _, q := range []string{"grace", "İstanbul", "ağrı"} {
		got := Snippet(source, q, 60)
		if !isValidUTF8(got) {
			t.Errorf("Snippet(_, %q) produced invalid UTF-8: %q", q, got)
		}
	}
}

func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// TestLikeContains covers the metacharacter escaping. Without it a search for
// "100%" matches every row in the table.
func TestLikeContains(t *testing.T) {
	cases := map[string]string{
		"grace": `%grace%`,
		"100%":  `%100\%%`,
		"a_b":   `%a\_b%`,
		`a\b`:   `%a\\b%`,
	}

	for in, want := range cases {
		if got := likeContains(in); got != want {
			t.Errorf("likeContains(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRefsAcross covers the topical passage list: distinct anchors in canonical
// order, gathered from notes that know nothing about each other.
func TestRefsAcross(t *testing.T) {
	ref := func(book, chapter, start, end int) bible.Ref {
		return bible.Ref{BookNum: book, Chapter: chapter, VerseStart: start, VerseEnd: end}
	}

	notes := []Note{
		{Refs: []NoteRef{
			{Ref: ref(45, 5, 8, 8)},   // Romans 5:8
			{Ref: ref(43, 3, 16, 18)}, // John 3:16-18
		}},
		{Refs: []NoteRef{
			{Ref: ref(43, 3, 16, 16)}, // John 3:16 — narrower, same start
			{Ref: ref(45, 5, 8, 8)},   // duplicate of the first note's anchor
			{Ref: ref(1, 1, 0, 0)},    // Genesis 1, whole chapter
		}},
	}

	got := RefsAcross(notes)
	want := []bible.Ref{
		ref(1, 1, 0, 0),
		ref(43, 3, 16, 16),
		ref(43, 3, 16, 18),
		ref(45, 5, 8, 8),
	}

	if len(got) != len(want) {
		t.Fatalf("RefsAcross() returned %d refs, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("RefsAcross()[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}
