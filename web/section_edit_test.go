package web

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/study"
)

// TestSectionEditEndToEnd drives the in-place outline edit through a real
// server: the route exists, a good edit lands in the database and redirects
// back to its row, and a bad one changes nothing and hands back what was typed.
func TestSectionEditEndToEnd(t *testing.T) {
	h := newDraftHarness(t, 0, func(w http.ResponseWriter) {})

	// The harness outline is one point. Add a passage so both input shapes
	// (plain text, parsed reference) are exercised.
	passageID, err := study.AppendSection(h.store.Study, h.sermonID, study.Section{
		Kind: study.KindPassage,
		Ref:  bible.Ref{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 16},
	})
	if err != nil {
		t.Fatalf("AppendSection() error: %v", err)
	}
	sermon := reloadSermon(t, h, study.StatusOutline)
	pointID := sermon.Outline[0].ID

	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	edit := func(sectionID, text string) (status int, location, body string) {
		t.Helper()
		resp, err := client.PostForm(
			h.baseURL+"/sermons/"+h.sermonID+"/sections/"+sectionID+"/edit",
			url.Values{"text": {text}})
		if err != nil {
			t.Fatalf("POST edit error: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("Location"), string(raw)
	}

	// A point reworded.
	status, location, _ := edit(pointID, "  God loved us first  ")
	if status != http.StatusSeeOther || !strings.HasSuffix(location, "#sec-"+pointID) {
		t.Errorf("point edit: status %d, Location %q; want 303 back to the row", status, location)
	}
	// A passage widened, typed with an abbreviation the parser accepts.
	if status, _, _ := edit(passageID, "Jn 3:16-18"); status != http.StatusSeeOther {
		t.Errorf("passage edit: status %d, want 303", status)
	}

	sermon = reloadSermon(t, h, study.StatusOutline)
	if got := sermon.Outline[0]; got.ID != pointID || got.Text != "God loved us first" {
		t.Errorf("point after edit = %+v, want the same id with the new text", got)
	}
	wantRef := bible.Ref{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 18}
	if got := sermon.Outline[1]; got.ID != passageID || got.Ref != wantRef {
		t.Errorf("passage after edit = %+v, want the same id at %v", got, wantRef)
	}

	// A reference that does not parse: nothing saved, and the page comes back
	// with the row still open on what was typed.
	status, _, body := edit(passageID, "Hezekiah 3:16")
	if status != http.StatusOK {
		t.Errorf("rejected edit: status %d, want the page re-rendered (200)", status)
	}
	if !strings.Contains(body, "Not changed") || !strings.Contains(body, `value="Hezekiah 3:16"`) {
		t.Errorf("rejected edit did not re-render the form with the typed text")
	}
	if got := reloadSermon(t, h, study.StatusOutline).Outline[1].Ref; got != wantRef {
		t.Errorf("a rejected edit changed the passage to %v", got)
	}

	// A section that is no longer there: the outline as it stands, no error page.
	if status, _, body := edit("no-such-section", "x"); status != http.StatusOK ||
		!strings.Contains(body, "already been removed") {
		t.Errorf("stale edit: status %d, want the outline with an explanation", status)
	}

	// And the GET that opens the form fills it from the stored section.
	resp, err := http.Get(h.baseURL + "/sermons/" + h.sermonID + "?edit=" + passageID)
	if err != nil {
		t.Fatalf("GET ?edit error: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(raw), `value="John 3:16-18"`) {
		t.Errorf("the edit form was not pre-filled with the stored reference")
	}
}
