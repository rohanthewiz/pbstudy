package web

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rohanthewiz/pbstudy/study"
)

// TestSameOrigin is the rule itself, as a table. The cases are the request
// shapes real clients produce; each comment says which client.
func TestSameOrigin(t *testing.T) {
	const host = "localhost:8000"

	cases := []struct {
		name         string
		secFetchSite string
		origin       string
		host         string
		want         bool
	}{
		// A form on one of our own pages, in a current browser on localhost.
		{"own form on localhost", "same-origin", "http://localhost:8000", host, true},
		// A form another site's page auto-submits to us.
		{"another site's form", "cross-site", "https://evil.example", host, false},
		// Another program's page on a different localhost port. Same site,
		// different origin, and precisely the neighbour to keep out.
		{"another local port", "same-site", "http://localhost:3000", host, false},
		// A URL typed into the address bar carries "none".
		{"user-initiated", "none", "", host, true},
		// Sec-Fetch-Site wins over a matching Origin; a page cannot set it.
		{"cross-site with a matching origin", "cross-site", "http://localhost:8000", host, false},
		{"an unknown label is refused", "sideways", "", host, false},

		// A phone on the LAN: http://192.168.1.5:8000 is not a secure context,
		// so the browser sends Origin and no Sec-Fetch-Site.
		{"own form over the LAN", "", "http://192.168.1.5:8000", "192.168.1.5:8000", true},
		{"another site, origin only", "", "https://evil.example", host, false},
		{"same host, different port", "", "http://localhost:3000", host, false},
		{"host comparison ignores case", "", "http://LOCALHOST:8000", host, true},
		// A sandboxed frame or a file:// page.
		{"null origin", "", "null", host, false},
		{"malformed origin", "", "://", host, false},

		// curl, a script, this package's tests: neither header.
		{"no browser labels at all", "", "", host, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sameOrigin(c.secFetchSite, c.origin, c.host); got != c.want {
				t.Errorf("sameOrigin(%q, %q, %q) = %v, want %v",
					c.secFetchSite, c.origin, c.host, got, c.want)
			}
		})
	}
}

// TestCrossOriginPostIsRefused drives the guard through a real server: the
// rule above is only worth anything if the middleware is actually in the chain
// and a refused request really does write nothing.
func TestCrossOriginPostIsRefused(t *testing.T) {
	h := newDraftHarness(t, 0, func(w http.ResponseWriter) {})

	// Redirects are the success signal here (every mutation ends in a 303), so
	// the client must report them rather than follow them.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	post := func(headers map[string]string) int {
		t.Helper()

		req, err := http.NewRequest(http.MethodPost, h.baseURL+"/sermons",
			strings.NewReader(url.Values{"title": {"Sent by a form"}}.Encode()))
		if err != nil {
			t.Fatalf("cannot build request: %v", err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("POST /sermons error: %v", err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	sermons := func() int {
		t.Helper()
		n, err := study.CountSermons(h.store.Study)
		if err != nil {
			t.Fatalf("CountSermons() error: %v", err)
		}
		return n
	}

	before := sermons() // the harness created one

	if got := post(map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "cross-site"}); got != http.StatusForbidden {
		t.Errorf("cross-site POST status = %d, want 403", got)
	}
	if got := post(map[string]string{"Origin": "https://evil.example"}); got != http.StatusForbidden {
		t.Errorf("foreign-Origin POST status = %d, want 403", got)
	}
	if got := sermons(); got != before {
		t.Errorf("a refused POST still created a sermon: %d rows, want %d", got, before)
	}

	// The app's own form, as a browser on localhost labels it.
	own := strings.TrimPrefix(h.baseURL, "http://")
	if got := post(map[string]string{"Origin": "http://" + own, "Sec-Fetch-Site": "same-origin"}); got != http.StatusSeeOther {
		t.Errorf("same-origin POST status = %d, want 303", got)
	}
	// The same form from a browser that sends Origin alone (a phone on the
	// LAN). This is the case that depends on the Host header being read off
	// the wire, so it is worth driving through the real server.
	if got := post(map[string]string{"Origin": "http://" + own}); got != http.StatusSeeOther {
		t.Errorf("matching-Origin POST status = %d, want 303", got)
	}
	// And a client with no browser labels, which is what a script looks like.
	if got := post(nil); got != http.StatusSeeOther {
		t.Errorf("unlabelled POST status = %d, want 303", got)
	}
	if got := sermons(); got != before+3 {
		t.Errorf("after three allowed POSTs there are %d sermons, want %d", got, before+3)
	}
}

// TestCrossOriginDraftStreamIsRefused: the draft stream is a GET that spends
// money, so it makes the origin check itself. A refusal arrives as a "fail"
// event on a 200 stream, like every other refusal on that endpoint.
func TestCrossOriginDraftStreamIsRefused(t *testing.T) {
	called := false
	h := newDraftHarness(t, 0, func(w http.ResponseWriter) { called = true })

	req, err := http.NewRequest(http.MethodGet,
		h.baseURL+"/sermons/"+h.sermonID+"/draft/stream", nil)
	if err != nil {
		t.Fatalf("cannot build request: %v", err)
	}
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cannot open the draft stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	names, _ := readStream(t, resp.Body)
	if len(names) != 1 || names[0] != sseFail {
		t.Errorf("event names = %v, want a single %q", names, sseFail)
	}
	if called {
		t.Error("the Messages API was called for a cross-site request")
	}
}

// TestScriptsRevalidate: the embedded scripts are served "no-cache", which is
// only cheap if the browser has a validator to send back. The first response
// must carry an ETag, and presenting it must get a 304 with no body.
func TestScriptsRevalidate(t *testing.T) {
	h := newDraftHarness(t, 0, func(w http.ResponseWriter) {})

	first, err := http.Get(h.baseURL + "/js/app.js")
	if err != nil {
		t.Fatalf("GET /js/app.js error: %v", err)
	}
	body, _ := io.ReadAll(first.Body)
	_ = first.Body.Close()

	etag := first.Header.Get("ETag")
	if first.StatusCode != http.StatusOK || etag == "" || len(body) == 0 {
		t.Fatalf("first response: status %d, ETag %q, %d bytes; want 200 with a tag and a body",
			first.StatusCode, etag, len(body))
	}

	req, err := http.NewRequest(http.MethodGet, h.baseURL+"/js/app.js", nil)
	if err != nil {
		t.Fatalf("cannot build request: %v", err)
	}
	req.Header.Set("If-None-Match", etag)
	second, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional GET error: %v", err)
	}
	again, _ := io.ReadAll(second.Body)
	_ = second.Body.Close()

	if second.StatusCode != http.StatusNotModified || len(again) != 0 {
		t.Errorf("conditional response: status %d with %d bytes, want 304 with none",
			second.StatusCode, len(again))
	}

	// A stale tag (an older binary's copy) still gets the current script.
	req.Header.Set("If-None-Match", `"0000"`)
	third, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stale conditional GET error: %v", err)
	}
	_ = third.Body.Close()
	if third.StatusCode != http.StatusOK {
		t.Errorf("stale-tag response status = %d, want 200", third.StatusCode)
	}
}
