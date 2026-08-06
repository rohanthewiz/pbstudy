package web

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	_ "github.com/rohanthewiz/bytdb/stdlib"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/cfg"
	"github.com/rohanthewiz/pbstudy/store"
	"github.com/rohanthewiz/pbstudy/study"
)

// The tests in this file drive a real server over a real socket.
//
// Everything else in the drafting path is unit-testable in isolation, but the
// seam that matters most is not: rweb turns a channel of events into bytes on a
// socket, and this app's whole SSE contract — one line per event, JSON payloads,
// a closed channel ending the stream cleanly — rests on that behaviour. Reading
// rweb's source is not the same as watching it write the frames.
//
// The Anthropic API is stood in for by a local server (see Server.draftClient),
// so these run offline and without an API key.

// draftHarness is a running pbstudy with a fake Messages API behind it.
type draftHarness struct {
	baseURL  string
	sermonID string
	store    *store.Store
}

// newDraftHarness starts a study database, a stand-in API that replays frames,
// and the app itself; it returns once the app is answering requests.
//
// stall sets this server's disconnect timeout. It is a per-server field rather
// than a package variable precisely so these tests can run concurrently without
// one test's shortened timeout being read by another server's goroutines; pass
// 0 for the production value.
func newDraftHarness(t *testing.T, stall time.Duration, frames func(w http.ResponseWriter)) *draftHarness {
	t.Helper()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		frames(w)
	}))
	t.Cleanup(api.Close)

	conf := cfg.Config{
		DataDir:      t.TempDir(),
		Port:         freePort(t),
		AnthropicKey: "test-key",
	}

	st, err := store.Open(conf)
	if err != nil {
		t.Fatalf("store.Open() error: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := bible.SeedBooks(st.Bible); err != nil {
		t.Fatalf("SeedBooks() error: %v", err)
	}

	sermonID, err := study.CreateSermon(st.Study, "The love of God")
	if err != nil {
		t.Fatalf("CreateSermon() error: %v", err)
	}
	if _, err := study.AppendSection(st.Study, sermonID,
		study.Section{Kind: study.KindPoint, Text: "God loved first"}); err != nil {
		t.Fatalf("AppendSection() error: %v", err)
	}

	// No syncer: drafting has nothing to do with sync, and a nil one is the
	// same "sync is off" path a machine without a sync directory takes.
	srv, err := New(conf, st, nil)
	if err != nil {
		t.Fatalf("web.New() error: %v", err)
	}
	srv.aiEndpoint = api.URL
	if stall > 0 {
		srv.draftStall = stall
	}

	go func() { _ = srv.Run() }()

	base := "http://127.0.0.1:" + strconv.Itoa(conf.Port)
	waitForServer(t, base)

	return &draftHarness{baseURL: base, sermonID: sermonID, store: st}
}

// freePort asks the kernel for a port and hands it back. A race with another
// process is possible in principle and has never mattered in practice; the
// alternative is a fixed port, which races with the developer's own server.
func freePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("cannot find a free port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

func waitForServer(t *testing.T, base string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/sermons")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the server never came up")
}

// sseFrame writes one event and flushes it, so the reader sees it immediately
// rather than when the handler returns.
func sseFrame(w http.ResponseWriter, name, payload string) {
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, payload)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func sseTextDelta(w http.ResponseWriter, text string) {
	esc, _ := json.Marshal(text)
	sseFrame(w, "content_block_delta",
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+
			string(esc)+`}}`)
}

// TestDraftStreamEndToEnd is the whole path: an EventSource-shaped request in,
// framed events out, and the finished draft in the database.
func TestDraftStreamEndToEnd(t *testing.T) {
	// A draft full of newlines, because that is the case the JSON framing
	// exists for — a raw newline in a data line would truncate the frame.
	const drafted = "## The love of God\n\nHe loved us first.\n\n> Quoted line.\n"

	h := newDraftHarness(t, 0, func(w http.ResponseWriter) {
		sseFrame(w, "content_block_start",
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}`)
		sseTextDelta(w, "## The love of God\n\n")
		sseTextDelta(w, "He loved us first.\n\n")
		sseTextDelta(w, "> Quoted line.\n")
		sseFrame(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
		sseFrame(w, "message_stop", `{"type":"message_stop"}`)
	})

	resp, err := http.Get(h.baseURL + "/sermons/" + h.sermonID + "/draft/stream")
	if err != nil {
		t.Fatalf("cannot open the draft stream: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	names, texts := readStream(t, resp.Body)

	// Every frame the browser's handlers are registered for must arrive, and
	// "done" must be last — it is what closes the EventSource.
	if len(names) == 0 || names[len(names)-1] != sseDone {
		t.Errorf("event names = %v, want the last one to be %q", names, sseDone)
	}
	if !contains(names, sseStatus) {
		t.Errorf("event names = %v, want a status event for the thinking block", names)
	}
	if got := strings.Join(texts[sseDelta], ""); got != drafted {
		t.Errorf("streamed draft = %q, want %q", got, drafted)
	}

	// And the same text was saved, so the reload the panel offers shows exactly
	// what was on screen.
	sermon := reloadSermon(t, h, study.StatusDrafted)
	if sermon.DraftMD != drafted {
		t.Errorf("saved draft = %q, want %q", sermon.DraftMD, drafted)
	}
}

// TestDraftStreamRefusesASecondReader covers the reconnect guard: EventSource
// retries on its own, and a retry must not start a second generation (or a
// second bill) for the same sermon.
func TestDraftStreamRefusesASecondReader(t *testing.T) {
	release := make(chan struct{})

	h := newDraftHarness(t, 0, func(w http.ResponseWriter) {
		sseTextDelta(w, "first draft")
		<-release // hold the first generation open
		sseFrame(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
	})

	first, err := http.Get(h.baseURL + "/sermons/" + h.sermonID + "/draft/stream")
	if err != nil {
		t.Fatalf("cannot open the first stream: %v", err)
	}
	defer func() { _ = first.Body.Close() }()

	// Read one frame so the first generation is definitely under way.
	firstReader := bufio.NewReader(first.Body)
	if _, err := firstReader.ReadString('\n'); err != nil {
		t.Fatalf("the first stream produced nothing: %v", err)
	}

	second, err := http.Get(h.baseURL + "/sermons/" + h.sermonID + "/draft/stream")
	if err != nil {
		t.Fatalf("cannot open the second stream: %v", err)
	}
	defer func() { _ = second.Body.Close() }()

	names, texts := readStream(t, second.Body)
	if !contains(names, sseFail) {
		t.Errorf("second stream events = %v, want a %q", names, sseFail)
	}
	if msg := strings.Join(texts[sseFail], ""); !strings.Contains(msg, "already being written") {
		t.Errorf("second stream said %q, want it to explain the draft is already running", msg)
	}

	close(release)
}

// TestDraftStreamSurvivesAReaderLeaving is the "kill it mid-stream" case: the
// browser goes away, and the generator must notice, stop, and still save what
// was produced rather than blocking on a channel nobody drains.
func TestDraftStreamSurvivesAReaderLeaving(t *testing.T) {
	// The stall detector only fires once the buffer between the generator and
	// the socket is full, so the stand-in produces more frames than it holds.
	h := newDraftHarness(t, 250*time.Millisecond, func(w http.ResponseWriter) {
		for i := 0; i < draftBuffer*2; i++ {
			sseTextDelta(w, "word ")
		}
		sseFrame(w, "message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`)
	})

	resp, err := http.Get(h.baseURL + "/sermons/" + h.sermonID + "/draft/stream")
	if err != nil {
		t.Fatalf("cannot open the draft stream: %v", err)
	}

	// Read one frame, then walk away mid-draft.
	if _, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil {
		t.Fatalf("the stream produced nothing: %v", err)
	}
	_ = resp.Body.Close()

	// The generator should give up within a stall timeout and save the partial:
	// those tokens were produced and paid for either way.
	sermon := reloadSermon(t, h, study.StatusDrafted)
	if !strings.Contains(sermon.DraftMD, "word") {
		t.Errorf("the abandoned draft saved %q, want the fragments produced before the reader left",
			sermon.DraftMD)
	}

	// And the sermon is claimable again, so the next attempt is not locked out
	// by a generation that has already stopped.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(h.baseURL + "/sermons/" + h.sermonID + "/draft/stream")
		if err == nil {
			names, _ := readStream(t, resp.Body)
			_ = resp.Body.Close()
			if !contains(names, sseFail) {
				return // the claim was released
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("the sermon stayed claimed after its reader left")
}

// --- helpers ---------------------------------------------------------------

// readStream drains a server-sent event stream into the event names in order
// and the decoded text of each, keyed by name.
func readStream(t *testing.T, r interface{ Read([]byte) (int, error) }) ([]string, map[string][]string) {
	t.Helper()

	var names []string
	texts := map[string][]string{}
	current := ""

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()

		if name, ok := strings.CutPrefix(line, "event: "); ok {
			current = name
			names = append(names, name)
			continue
		}

		data, ok := strings.CutPrefix(line, "data: ")
		if !ok {
			continue
		}
		// A raw newline in a payload would land here as a line that is not
		// valid JSON — which is exactly the corruption the framing prevents.
		var decoded struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(data), &decoded); err != nil {
			t.Errorf("event payload was not JSON: %v (%q)", err, data)
			continue
		}
		texts[current] = append(texts[current], decoded.Text)
	}
	return names, texts
}

// reloadSermon polls until the sermon reaches the wanted status, since the
// generator saves from its own goroutine after the stream ends.
func reloadSermon(t *testing.T, h *draftHarness, wantStatus string) study.Sermon {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var last study.Sermon

	for time.Now().Before(deadline) {
		sermon, err := study.GetSermon(h.store.Study, h.sermonID)
		if err != nil {
			t.Fatalf("GetSermon() error: %v", err)
		}
		last = sermon
		if sermon.Status == wantStatus {
			return sermon
		}
		time.Sleep(25 * time.Millisecond)
	}

	t.Fatalf("sermon status stayed %q, want %q", last.Status, wantStatus)
	return last
}

func contains(vals []string, want string) bool {
	for _, v := range vals {
		if v == want {
			return true
		}
	}
	return false
}
