package study

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/rohanthewiz/serr"
	"github.com/yuin/goldmark"

	"github.com/rohanthewiz/pbstudy/blb"
)

// md is the shared Markdown converter.
//
// Built with goldmark's defaults, and the default that matters most is the one
// NOT changed: html.WithUnsafe() is absent, so raw HTML inside a note body is
// dropped rather than emitted. Note bodies are user-authored, but "the user"
// includes a JSON file that arrived from another machine through a sync
// directory, and a study app has no reason to let a note carry markup.
//
// Package-level and reused: goldmark converters are safe for concurrent use
// and building one per render would allocate the whole parser chain per page.
var md = goldmark.New()

// strongsShortcode matches the [[G26]] / [[H430]] form used inside note
// bodies to cite a Strong's number.
//
// The digit cap is deliberate — Strong's runs to G5624 and H8674, so five
// digits is already generous, and a bound keeps a stray "[[H999999999]]" from
// becoming a link to nothing.
var strongsShortcode = regexp.MustCompile(`\[\[([GgHh])(\d{1,5})\]\]`)

// RenderMarkdown converts a note body to HTML for display.
//
// # Two passes, in this order
//
//	[[G26]]  ->  [G26](https://www.blueletterbible.org/lexicon/g26/kjv/tr/)
//	Markdown ->  HTML
//
// Expanding the shortcode into ordinary Markdown link syntax rather than into
// raw HTML is what lets the converter stay in safe mode: the link goes through
// goldmark's own escaping like any other link the user typed. The cost is that
// a shortcode inside a code span or fence expands too — an acceptable trade in
// a study tool, where `[[G26]]` in a code block is not a thing anyone writes.
//
// The rendered HTML is deliberately NOT marked no-tag: Blue Letter Bible's
// ScriptTagger is meant to find the scripture references a user types into a
// note and attach hover popups to them. That is the whole reason the reader's
// own scripture container is excluded instead — see blb.NoTagClass.
func RenderMarkdown(source string) (string, error) {
	expanded := strongsShortcode.ReplaceAllStringFunc(source, func(m string) string {
		parts := strongsShortcode.FindStringSubmatch(m)
		label := strings.ToUpper(parts[1]) + parts[2]
		return "[" + label + "](" + blb.LexiconURL(label) + ")"
	})

	var buf bytes.Buffer
	if err := md.Convert([]byte(expanded), &buf); err != nil {
		return "", serr.Wrap(err, "cannot render note markdown")
	}
	return buf.String(), nil
}

// Excerpt returns a one-line plain-text preview of a note body, for the notes
// list and the verse hub's collapsed view.
//
// It strips the markup a first line commonly carries rather than rendering and
// then un-rendering: the goal is a recognizable label, not a faithful
// downgrade, and running the full converter for a 120-character preview of
// every note in a list would be wasted work.
func Excerpt(source string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = 160
	}

	// Collapse to a single line: a preview that wraps is not a preview.
	line := strings.Join(strings.Fields(stripMarkup(source)), " ")
	if len(line) <= maxLen {
		return line
	}

	// Cut at a word boundary when one is close by, so the ellipsis does not
	// land mid-word.
	cut := line[:maxLen]
	if i := strings.LastIndexByte(cut, ' '); i > maxLen*3/4 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "…"
}

// stripMarkup removes the lightweight markup that would otherwise show up as
// punctuation noise in an excerpt. It is intentionally shallow — headings,
// emphasis, blockquote and list markers, and the two link forms.
func stripMarkup(s string) string {
	s = strongsShortcode.ReplaceAllString(s, "$1$2")
	s = mdLink.ReplaceAllString(s, "$1")   // [text](url) -> text
	s = mdMarker.ReplaceAllString(s, "")   // leading #, >, -, *, digits.
	s = mdEmphasis.ReplaceAllString(s, "") // * _ ` around words
	return s
}

var (
	mdLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	mdMarker   = regexp.MustCompile(`(?m)^\s{0,3}(#{1,6}\s+|>\s?|[-*+]\s+|\d+\.\s+)`)
	mdEmphasis = regexp.MustCompile("[*_`~]")
)
