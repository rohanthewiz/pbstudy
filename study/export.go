package study

import (
	"html"
	"strings"
	"unicode"

	"github.com/rohanthewiz/element"
	"github.com/rohanthewiz/serr"
)

// ExportHTML wraps an assembled outline in a standalone HTML document.
//
// # Why the styles are inlined and light
//
// This file leaves the app: it gets emailed, opened from a downloads folder,
// dropped on a tablet at a lectern, printed. A <link> to /css/app.css would
// render it unstyled anywhere pbstudy is not running, so the whole stylesheet
// travels inside the document.
//
// It is also light-on-white, unlike the app's dark reading surface. The app is
// styled for a long study session at night; this is styled for paper and for a
// bright room, which is where a sermon is actually delivered from.
func ExportHTML(title, markdown string) (string, error) {
	bodyHTML, err := RenderMarkdown(markdown)
	if err != nil {
		return "", serr.Wrap(err, "cannot render sermon markdown")
	}

	b := element.NewBuilder()

	b.Html("lang", "en").R(
		b.Head().R(
			b.Meta("charset", "utf-8"),
			b.Meta("name", "viewport", "content", "width=device-width, initial-scale=1"),
			b.Title().T(html.EscapeString(title)),
			b.Style().T(exportCSS),
		),
		b.Body().R(
			b.Main().R(
				// bodyHTML comes from RenderMarkdown, which runs goldmark with
				// raw-HTML passthrough disabled — anything markup-shaped in the
				// source was escaped there. This is the same narrow exception
				// the note page makes; see web/ui/escape.go.
				b.Wrap(func() { _ = b.WriteString(bodyHTML) }),
			),
			b.FooterClass("export-footer").T("Assembled with pbstudy."),
		),
	)

	return b.String(), nil
}

// exportCSS is the whole stylesheet for an exported sermon.
//
// Hand-written CSS rather than the app's compiled Stylus: this document has
// none of the app's chrome and needs none of its rules, and reusing them would
// mean shipping a dark palette and a nav bar's worth of selectors to style four
// elements. The print block is the part that earns its place — a sermon gets
// printed.
const exportCSS = `
:root { color-scheme: light; }
body {
  margin: 0;
  background: #fff;
  color: #1a1a1a;
  font: 16px/1.6 -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
}
main { max-width: 42rem; margin: 0 auto; padding: 2rem 1.25rem 3rem; }
h1 { font-size: 1.9rem; line-height: 1.2; margin: 0 0 1.5rem; }
h2 { font-size: 1.3rem; margin: 2rem 0 0.6rem; padding-bottom: 0.2rem; border-bottom: 1px solid #e2e2e2; }
h3 { font-size: 1.05rem; margin: 1.5rem 0 0.4rem; }
p, li { margin: 0.5rem 0; }
ul, ol { padding-left: 1.3rem; }
a { color: #17557f; }
em { color: #555; }
blockquote {
  margin: 1rem 0;
  padding: 0.6rem 0 0.6rem 1rem;
  border-left: 3px solid #c9b071;
  background: #faf7ef;
  font-family: "Iowan Old Style", Palatino, Georgia, serif;
  font-size: 1.05rem;
}
blockquote p { margin: 0.35rem 0; }
code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.9em; }
.export-footer {
  max-width: 42rem;
  margin: 0 auto;
  padding: 1rem 1.25rem 2rem;
  color: #888;
  font-size: 0.8rem;
  border-top: 1px solid #e2e2e2;
}
@media print {
  body { font-size: 12pt; }
  main { max-width: none; padding: 0; }
  .export-footer { display: none; }
  h2 { page-break-after: avoid; }
  blockquote { page-break-inside: avoid; background: none; }
}
`

// FileSlug turns a title into a safe download filename stem.
//
// Conservative by construction: it keeps letters, digits and spaces-as-hyphens
// and drops everything else, so a title containing a quote, a slash or a
// newline cannot escape into the Content-Disposition header it lands in.
// Allow-listing is the point — a blocklist of shell- or header-significant
// characters is a list you will one day be missing an entry from.
func FileSlug(title string) string {
	var b strings.Builder
	lastHyphen := true // leading hyphens are suppressed

	for _, r := range strings.ToLower(strings.TrimSpace(title)) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			lastHyphen = false
		case !lastHyphen:
			b.WriteByte('-')
			lastHyphen = true
		}
	}

	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "sermon"
	}
	if len(slug) > 60 {
		slug = strings.Trim(slug[:60], "-")
	}
	return slug
}
