// Package blb builds Blue Letter Bible links and the ScriptTagger snippet.
//
// BLB is used two ways, both of which keep pbstudy usable offline:
//
//  1. Deep links — plain <a> tags to blueletterbible.org for interlinear,
//     lexicon, and commentary work. Dead links when offline; nothing else
//     breaks.
//  2. ScriptTagger — BLB's own script, which finds scripture references in
//     page text and attaches hover popups. Loaded with `defer`, so a failed
//     fetch costs nothing but the popups.
//
// Deliberately NOT used: iframes. BLB sends no X-Frame-Options so framing
// would technically work, but embedding a whole third-party site inside a
// personal study tool is fragile and slow; links and the tagger are the
// supported integration path.
package blb

import (
	"strconv"
	"strings"

	"github.com/rohanthewiz/pbstudy/bible"
)

// BaseURL is the site root. All link builders hang off this.
const BaseURL = "https://www.blueletterbible.org"

// ScriptTaggerURL is BLB's official reference-tagging script.
const ScriptTaggerURL = BaseURL + "/assets/scripts/blbToolTip/BLB_ScriptTagger-min.js"

// NoTagClass marks a subtree ScriptTagger must leave alone.
//
// This matters more than it looks: the reader already renders every verse
// with its own reference and its own links. Letting the tagger loose in there
// would double-link the entire chapter. Excluding the scripture container
// while leaving note bodies taggable is precisely the behaviour we want —
// references the user types into a note get popups; scripture we rendered
// ourselves does not.
const NoTagClass = "blb-no-tag"

// VerseURL deep-links a single verse, e.g.
// https://www.blueletterbible.org/kjv/jhn/3/16/.
func VerseURL(translation string, bookNum, chapter, verse int) string {
	bk := bible.MustByNum(bookNum)
	var sb strings.Builder
	sb.WriteString(BaseURL)
	sb.WriteByte('/')
	sb.WriteString(normalizeTranslation(translation))
	sb.WriteByte('/')
	sb.WriteString(bk.BLBAbbrev)
	sb.WriteByte('/')
	sb.WriteString(strconv.Itoa(chapter))
	sb.WriteByte('/')
	sb.WriteString(strconv.Itoa(verse))
	sb.WriteByte('/')
	return sb.String()
}

// ChapterURL deep-links a whole chapter. BLB accepts a chapter path with
// verse 1 as the anchor, which is what its own chapter links use.
func ChapterURL(translation string, bookNum, chapter int) string {
	return VerseURL(translation, bookNum, chapter, 1)
}

// InterlinearURL opens the verse in BLB's interlinear view — the Hebrew or
// Greek behind the English, which is the main reason to leave the app.
func InterlinearURL(translation string, bookNum, chapter, verse int) string {
	return VerseURL(translation, bookNum, chapter, verse) + "?t=" +
		strings.ToUpper(normalizeTranslation(translation))
}

// LexiconURL links a Strong's number to its lexicon entry, e.g. G26 (agape)
// -> https://www.blueletterbible.org/lexicon/g26/kjv/tr/.
//
// The textus-receptus ("tr") corpus is correct for Greek; Hebrew entries use
// "wlc" (Westminster Leningrad Codex). The prefix letter selects which.
func LexiconURL(strongs string) string {
	s := strings.ToLower(strings.TrimSpace(strongs))
	if s == "" {
		return BaseURL + "/lexicon/"
	}
	corpus := "tr" // Greek (New Testament)
	if strings.HasPrefix(s, "h") {
		corpus = "wlc" // Hebrew (Old Testament)
	}
	return BaseURL + "/lexicon/" + s + "/kjv/" + corpus + "/"
}

// SearchURL runs a text search on BLB — the escape hatch for anything
// pbstudy's own ILIKE search cannot do (proximity, morphology, etc.).
func SearchURL(q string) string {
	return BaseURL + "/search/search.cfm?Criteria=" + urlQueryEscape(q) + "&t=KJV"
}

// normalizeTranslation maps a pbstudy translation abbrev to BLB's URL segment.
// BLB does not carry the World English Bible, so WEB readers are sent to the
// KJV page for the same verse rather than to a 404.
func normalizeTranslation(t string) string {
	switch strings.ToLower(t) {
	case "asv":
		return "asv"
	case "kjv", "web", "":
		return "kjv"
	default:
		return "kjv"
	}
}

// TaggerConfigJS is the configuration ScriptTagger reads at load time.
//
// It must appear BEFORE the script tag: the tagger snapshots BLB.Tagger on
// execution, so config assigned afterwards is ignored. Getting this wrong is
// cosmetic (default styling instead of ours), never fatal.
func TaggerConfigJS(translation string) string {
	return `var BLB = BLB || {};
BLB.Tagger = {
  Translation: "` + strings.ToUpper(normalizeTranslation(translation)) + `",
  HyperLinks: true,
  TargetNewWindow: true,
  DarkTheme: true,
  Style: "default"
};`
}

// urlQueryEscape percent-encodes a query value. Written out rather than
// importing net/url so this package stays dependency-light for the one call
// site that needs it.
func urlQueryEscape(s string) string {
	const hex = "0123456789ABCDEF"
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			sb.WriteByte(c)
		case c == ' ':
			sb.WriteByte('+')
		default:
			sb.WriteByte('%')
			sb.WriteByte(hex[c>>4])
			sb.WriteByte(hex[c&0x0F])
		}
	}
	return sb.String()
}
