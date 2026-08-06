package ui

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/element"
)

// TestNormalizeScope pins the fallback: a scope arrives from a URL, and an
// unknown one must widen the search rather than error or narrow it to nothing.
func TestNormalizeScope(t *testing.T) {
	cases := map[string]string{
		"":           ScopeAll,
		"all":        ScopeAll,
		"scripture":  ScopeScripture,
		"SCRIPTURE":  ScopeScripture,
		"  notes  ":  ScopeNotes,
		"everything": ScopeAll,
		"'; drop":    ScopeAll,
	}

	for in, want := range cases {
		if got := NormalizeScope(in); got != want {
			t.Errorf("NormalizeScope(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSearchURLEscapes covers the scope tabs' links: the query rides in a
// query string, so a query containing '&' or '#' must not split the URL.
func TestSearchURLEscapes(t *testing.T) {
	got := SearchURL("law & grace #1", ScopeNotes, "kjv")

	if strings.Contains(got, "& grace") || strings.Contains(got, "#1") {
		t.Errorf("SearchURL() left the query unescaped: %q", got)
	}
	for _, want := range []string{"q=law+%26+grace+%231", "scope=notes", "translation=kjv"} {
		if !strings.Contains(got, want) {
			t.Errorf("SearchURL() = %q, want it to contain %q", got, want)
		}
	}
}

// TestHighlightEscapes is the security-relevant one.
//
// highlight() is the only place in the package that slices a string by byte
// offset and then emits the pieces, so it is the only place where escaping
// could be skipped by accident. Both the body and the query are attacker-
// supplied — the query arrives in a URL anyone can send.
func TestHighlightEscapes(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		query    string
		contains []string
		absent   []string
	}{
		{
			name:     "a hostile body is escaped around the match",
			body:     `<script>alert("grace")</script>`,
			query:    "grace",
			contains: []string{"&lt;script&gt;", "<mark>grace</mark>"},
			absent:   []string{"<script>"},
		},
		{
			name:     "a hostile query highlights literally",
			body:     `he wrote <script> in his note`,
			query:    "<script>",
			contains: []string{"<mark>&lt;script&gt;</mark>"},
			absent:   []string{"<script>"},
		},
		{
			name:     "an ampersand in the body survives as an entity",
			body:     "law & grace",
			query:    "grace",
			contains: []string{"law &amp; ", "<mark>grace</mark>"},
		},
		{
			name:  "case-insensitive matching keeps the original casing",
			body:  "Grace and GRACE",
			query: "grace",
			contains: []string{
				"<mark>Grace</mark>",
				"<mark>GRACE</mark>",
			},
		},
		{
			name:  "non-ascii input falls back to plain escaped text",
			body:  "İstanbul grace",
			query: "İstanbul",
			// Lowercasing changes the byte length here, so highlighting is
			// skipped rather than sliced at a wrong offset.
			absent: []string{"<mark>"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := element.NewBuilder()
			highlight(b, c.body, c.query)
			got := b.String()

			for _, want := range c.contains {
				if !strings.Contains(got, want) {
					t.Errorf("highlight() = %q, want it to contain %q", got, want)
				}
			}
			for _, unwanted := range c.absent {
				if strings.Contains(got, unwanted) {
					t.Errorf("highlight() = %q, want it NOT to contain %q", got, unwanted)
				}
			}
		})
	}
}
