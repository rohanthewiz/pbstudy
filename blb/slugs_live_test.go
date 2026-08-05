package blb_test

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/blb"
)

// TestBLBSlugsResolve checks that every book's Blue Letter Bible slug actually
// resolves. The slugs follow no derivable rule (Song → sng, Jude → jde,
// James → jam), so this is the only real guard against a typo silently
// shipping a dead link.
//
// Opt-in rather than skipped-by-default-with-short: it makes 66 requests
// against a third-party site, which has no business running on every
// `go test ./...`. Run it after editing the Books table:
//
//	PBSTUDY_LIVE_TESTS=1 go test ./blb/ -run BLBSlugs -v
func TestBLBSlugsResolve(t *testing.T) {
	if os.Getenv("PBSTUDY_LIVE_TESTS") == "" {
		t.Skip("live network test; set PBSTUDY_LIVE_TESTS=1 to run")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	for i := range bible.Books {
		bk := &bible.Books[i]
		url := blb.VerseURL("kjv", bk.Num, 1, 1)
		resp, err := client.Head(url)
		if err != nil {
			t.Errorf("%s (%s): %v", bk.Name, bk.BLBAbbrev, err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s (%s) -> %d  %s", bk.Name, bk.BLBAbbrev, resp.StatusCode, url)
		}
		time.Sleep(120 * time.Millisecond)
	}
}
