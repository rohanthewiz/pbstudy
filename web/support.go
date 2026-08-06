package web

import (
	"net/http"
	"strings"

	"github.com/rohanthewiz/rweb"
)

// studyUnavailable is the common failure page for a study-database read.
//
// Its own helper rather than a repeated renderError call because it is the one
// error the user can actually act on — the study database is the irreplaceable
// half of the storage, so the message points at a backup rather than
// suggesting a retry.
func (s *Server) studyUnavailable(ctx rweb.Context, err error) error {
	return s.renderError(ctx, http.StatusInternalServerError,
		"Cannot read your study database",
		"The notes database could not be queried. If this persists, restore the most "+
			"recent snapshot written by `pbstudy backup`.", err)
}

// safeReturn validates a caller-supplied redirect target and falls back when
// it is not a plain local path.
//
// The "return" field on every form is attacker-controllable in principle —
// this is a local app, but a link someone is sent still arrives as a request —
// and handing it to Redirect unchecked is an open redirect. Three rules, each
// closing a specific hole:
//
//	""                        -> fallback (nothing was asked for)
//	"//evil.example/x"        -> fallback (protocol-relative URL: a browser
//	                             reads this as a host, not a path)
//	"https://evil.example/x"  -> fallback (absolute URL, caught by the same
//	                             "must start with a single slash" rule)
//
// Anything that survives is a path on this server, which is the only thing a
// form on this server has business asking for.
func safeReturn(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") {
		return fallback
	}
	// A backslash is treated as a path separator by some browsers, so
	// "/\evil.example" would resolve off-site. Reject rather than rewrite.
	if strings.ContainsAny(raw, "\\\r\n") {
		return fallback
	}
	return raw
}
