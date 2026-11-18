package web

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
)

// This file is the app's defence against another web page driving it.
//
// # The problem
//
// Every mutation here is a plain form POST with no token in it, and the server
// answers on localhost. A browser will happily submit a form from any page the
// user has open to http://localhost:8000, so without a check any site could
// create notes, run a sync or a backup, and (given an id) delete a note or
// start a draft that is billed to the user's API key. No login is involved, so
// there is no cookie to protect and the usual CSRF-token machinery has nothing
// to hang off. What can be checked is where the request says it came from.
//
// # The check
//
// Browsers label requests themselves, and a page cannot forge either label:
//
//	Sec-Fetch-Site: same-origin | same-site | cross-site | none
//	Origin:         scheme://host:port of the page that made the request
//
// The decision, in order:
//
//	Sec-Fetch-Site present ──▶ allow "same-origin" (our own pages) and
//	                           "none" (typed URL, bookmark); refuse the rest
//	else Origin present    ──▶ allow when its host:port equals the Host header
//	else                   ──▶ allow
//
// "same-site" is refused on purpose. A site ignores the port, so another
// program serving pages on localhost:3000 is same-site with this one and is
// exactly the kind of neighbour this is meant to keep out.
//
// The Origin fallback is not redundant. Browsers send Sec-Fetch-Site only to
// destinations they consider secure; http://localhost qualifies, but a phone
// reaching this server at http://192.168.1.5:8000 does not, and its forms
// arrive with an Origin header alone.
//
// A request carrying neither header is let through: that is curl, a script,
// or this package's own tests, not a browser being steered by someone else's
// page. This is a guard against cross-site requests, not authentication; a
// client that can reach the port directly is a different question (the listen
// address, see cfg.Address).

// sameOrigin reports whether a request's own account of where it came from is
// compatible with it having come from this app's pages.
//
// Split from the middleware and written over plain strings so the rule can be
// tested as a table without standing up a server.
func sameOrigin(secFetchSite, origin, host string) bool {
	switch strings.ToLower(strings.TrimSpace(secFetchSite)) {
	case "same-origin", "none":
		return true
	case "":
		// Not sent; fall through to the Origin header.
	default:
		// "cross-site", "same-site", or a value this code does not know.
		// Unknown is refused: the header exists to be trusted when present.
		return false
	}

	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true // neither label present: not a browser form or fetch
	}

	// "null" is what a sandboxed frame or a file:// page sends, and it fails
	// here because it has no host. A malformed value fails the same way.
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, strings.TrimSpace(host))
}

// requestIsSameOrigin applies sameOrigin to a live request.
//
// The Host header is read directly rather than through Request.Host(), which
// rweb derives from the request line and which is "localhost" for every
// ordinary request regardless of the port or address the browser used.
func requestIsSameOrigin(req rweb.ItfRequest) bool {
	return sameOrigin(req.Header("Sec-Fetch-Site"), req.Header("Origin"), req.Header("Host"))
}

// guardCrossOrigin refuses state-changing requests that another site sent.
//
// Method-based rather than a list of routes: every mutation in this app is a
// POST (see registerRoutes), and a route added later is covered without anyone
// remembering to add it here. GET, HEAD and OPTIONS pass untouched, since a
// link to a note from another page is a perfectly good thing to follow. The one
// GET that is not harmless, the draft stream, makes this same check itself.
func (s *Server) guardCrossOrigin(ctx rweb.Context) error {
	req := ctx.Request()

	switch req.Method() {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return ctx.Next()
	}

	if !requestIsSameOrigin(req) {
		logger.Warn("refused a cross-origin request",
			"method", req.Method(), "path", req.Path(),
			"origin", req.Header("Origin"), "secFetchSite", req.Header("Sec-Fetch-Site"))
		return s.renderError(ctx, http.StatusForbidden, "Not allowed from another site",
			"This request was sent by a page that is not pbstudy, so it was not carried out. "+
				"If you pressed a button in pbstudy itself, reload the page and try again.", nil)
	}
	return ctx.Next()
}
