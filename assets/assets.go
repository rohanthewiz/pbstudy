// Package assets embeds the app's static resources into the binary.
//
// Everything ships inside the executable so pbstudy is a single file to copy
// between machines — no asset directory to keep in sync with the binary, and
// no chance of a stale stylesheet after an upgrade.
package assets

import (
	"embed"
	"io/fs"

	"github.com/rohanthewiz/serr"
)

//go:embed styles/*.styl
var stylesFS embed.FS

//go:embed js/*.js
var jsFS embed.FS

// Styles returns the Stylus source root, rooted so that a request for
// "/css/app.css" resolves to "app.styl".
//
// Note that embed.FS carries no modification times, so go-styl compiles each
// stylesheet once per process and serves it from cache thereafter. That is
// the right trade for a shipped binary; during development, point
// stylserve.Options.Dir at the source directory instead to get live reload.
func Styles() (fs.FS, error) {
	sub, err := fs.Sub(stylesFS, "styles")
	if err != nil {
		return nil, serr.Wrap(err, "cannot open embedded styles")
	}
	return sub, nil
}

// JS returns the JavaScript root for the /js/ route.
func JS() (fs.FS, error) {
	sub, err := fs.Sub(jsFS, "js")
	if err != nil {
		return nil, serr.Wrap(err, "cannot open embedded js")
	}
	return sub, nil
}
