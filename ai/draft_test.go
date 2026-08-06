package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rohanthewiz/serr"
)

// sseServer stands in for the Messages API: it records the request it received
// and replays a canned event stream.
//
// A fake server rather than a fake io.Reader, because half of what is worth
// testing here is the request — the headers, and the parameters that must NOT
// be present.
func sseServer(t *testing.T, frames ...string) (*httptest.Server, *http.Request, *[]byte) {
	t.Helper()

	var gotReq http.Request
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = *r.Clone(context.Background())
		body := make([]byte, 1<<20)
		n, _ := r.Body.Read(body)
		gotBody = body[:n]

		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		for _, f := range frames {
			_, _ = w.Write([]byte(f))
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &gotReq, &gotBody
}

// frame builds one server-sent event in the API's shape: a named event line
// followed by its JSON payload.
func frame(name, payload string) string {
	return "event: " + name + "\ndata: " + payload + "\n\n"
}

func textDelta(s string) string {
	esc, _ := json.Marshal(s)
	return frame("content_block_delta",
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+
			string(esc)+`}}`)
}

func newTestClient(endpoint string) Client {
	c := NewClient("test-key")
	c.Endpoint = endpoint
	return c
}

// collect drains a draft into its events and its text.
func collect(t *testing.T, c Client) (Result, []Event, error) {
	t.Helper()

	var events []Event
	result, err := c.Draft(context.Background(), Request{Title: "The love of God", OutlineMD: "# outline"},
		func(ev Event) error {
			events = append(events, ev)
			return nil
		})
	return result, events, err
}

// TestDraftStreamsText is the happy path: thinking, then prose, then a clean
// stop.
func TestDraftStreamsText(t *testing.T) {
	srv, _, _ := sseServer(t,
		frame("message_start", `{"type":"message_start","message":{"id":"msg_1"}}`),
		frame("content_block_start",
			`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`),
		frame("content_block_delta",
			`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`),
		frame("content_block_stop", `{"type":"content_block_stop","index":0}`),
		frame("content_block_start",
			`{"type":"content_block_start","index":1,"content_block":{"type":"text","text":""}}`),
		textDelta("# The love of God\n\n"),
		textDelta("God so loved."),
		frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`),
		frame("message_stop", `{"type":"message_stop"}`),
	)

	result, events, err := collect(t, newTestClient(srv.URL))
	if err != nil {
		t.Fatalf("Draft() error: %v", err)
	}

	if want := "# The love of God\n\nGod so loved."; result.Text != want {
		t.Errorf("Draft() text = %q, want %q", result.Text, want)
	}
	if result.StopReason != "end_turn" {
		t.Errorf("Draft() stop reason = %q, want end_turn", result.StopReason)
	}
	if result.Truncated {
		t.Error("Draft() reported truncation on a clean stop")
	}

	// The thinking block must produce a status, or the pause before the first
	// word of prose is a blank panel with no explanation.
	if len(events) < 3 ||
		events[0].Kind != EventStatus || events[0].Text != "Thinking…" ||
		events[1].Kind != EventStatus || events[1].Text != "Writing…" {
		t.Errorf("Draft() events = %+v, want a Thinking then Writing status first", events)
	}

	// Concatenating the text events must reproduce the draft exactly — that is
	// the contract the browser's live panel relies on.
	var joined strings.Builder
	for _, ev := range events {
		if ev.Kind == EventText {
			joined.WriteString(ev.Text)
		}
	}
	if joined.String() != result.Text {
		t.Errorf("text events joined to %q, want %q", joined.String(), result.Text)
	}
}

// TestDraftRequestShape pins the request.
//
// The absent parameters matter more than the present ones: Sonnet 5 rejects
// non-default sampling parameters and manual thinking budgets outright, so a
// well-meaning future addition of "temperature": 0 would turn every draft into
// a 400. This test is the thing that would catch it.
func TestDraftRequestShape(t *testing.T) {
	srv, req, body := sseServer(t,
		textDelta("draft"),
		frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`),
	)

	if _, _, err := collect(t, newTestClient(srv.URL)); err != nil {
		t.Fatalf("Draft() error: %v", err)
	}

	if got := req.Header.Get("x-api-key"); got != "test-key" {
		t.Errorf("x-api-key = %q, want the configured key", got)
	}
	if got := req.Header.Get("anthropic-version"); got != apiVersion {
		t.Errorf("anthropic-version = %q, want %q", got, apiVersion)
	}

	var sent map[string]any
	if err := json.Unmarshal(*body, &sent); err != nil {
		t.Fatalf("request body was not JSON: %v\n%s", err, *body)
	}

	if sent["model"] != DefaultModel {
		t.Errorf("model = %v, want %q", sent["model"], DefaultModel)
	}
	if sent["stream"] != true {
		t.Errorf("stream = %v, want true", sent["stream"])
	}
	if sent["max_tokens"] != float64(defaultMaxTokens) {
		t.Errorf("max_tokens = %v, want %d", sent["max_tokens"], defaultMaxTokens)
	}
	for _, banned := range []string{"temperature", "top_p", "top_k", "thinking"} {
		if _, present := sent[banned]; present {
			t.Errorf("request carried %q, which this model rejects", banned)
		}
	}

	// The outline has to actually reach the model, and the title with it.
	msgs, _ := sent["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v, want exactly one user turn", sent["messages"])
	}
	first, _ := msgs[0].(map[string]any)
	content, _ := first["content"].(string)
	if !strings.Contains(content, "# outline") || !strings.Contains(content, "The love of God") {
		t.Errorf("user message did not carry the outline and title: %q", content)
	}
}

// TestDraftPreservesNewlines guards the seam between this package and the SSE
// transport in web/: a draft is mostly newlines, and losing or doubling them
// would be invisible in a rendered view and obvious in the .md export.
func TestDraftPreservesNewlines(t *testing.T) {
	want := "## One\n\nLine one.\nLine two.\n\n> Quoted.\n"

	srv, _, _ := sseServer(t,
		textDelta("## One\n\nLine one.\n"),
		textDelta("Line two.\n\n> Quoted.\n"),
		frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`),
	)

	result, _, err := collect(t, newTestClient(srv.URL))
	if err != nil {
		t.Fatalf("Draft() error: %v", err)
	}
	if result.Text != want {
		t.Errorf("Draft() text = %q, want %q", result.Text, want)
	}
}

// TestDraftIgnoresUnknownFrames: the stream carries events this app does not
// read, and will carry more of them in future. None of them may abort a draft
// that is otherwise arriving fine.
func TestDraftIgnoresUnknownFrames(t *testing.T) {
	srv, _, _ := sseServer(t,
		": keep-alive comment\n\n",
		frame("ping", `{"type":"ping"}`),
		"event: something_new\ndata: {\"type\":\"something_new\",\"payload\":{\"a\":1}}\n\n",
		"data: {this is not json}\n\n",
		textDelta("survived"),
		frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`),
	)

	result, _, err := collect(t, newTestClient(srv.URL))
	if err != nil {
		t.Fatalf("Draft() error: %v", err)
	}
	if result.Text != "survived" {
		t.Errorf("Draft() text = %q, want %q", result.Text, "survived")
	}
}

// TestDraftTruncation: hitting the token ceiling is not an error, but it has to
// be reported — a draft that stops mid-sentence otherwise reads as a bug.
func TestDraftTruncation(t *testing.T) {
	srv, _, _ := sseServer(t,
		textDelta("it ends mid-"),
		frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`),
	)

	result, _, err := collect(t, newTestClient(srv.URL))
	if err != nil {
		t.Fatalf("Draft() error: %v", err)
	}
	if !result.Truncated {
		t.Error("Draft() did not report truncation on stop_reason=max_tokens")
	}
}

// TestDraftEmitAbort covers the browser closing its EventSource: the draft
// stops, and whatever arrived is still returned so the caller can save it.
func TestDraftEmitAbort(t *testing.T) {
	srv, _, _ := sseServer(t,
		textDelta("kept "),
		textDelta("also kept"),
		textDelta("never seen"),
		frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`),
	)

	stop := errors.New("reader went away")
	seen := 0

	result, err := newTestClient(srv.URL).Draft(context.Background(), Request{OutlineMD: "x"},
		func(ev Event) error {
			if ev.Kind != EventText {
				return nil
			}
			seen++
			if seen == 2 {
				return stop
			}
			return nil
		})

	if !errors.Is(err, stop) {
		t.Fatalf("Draft() error = %v, want the emit error", err)
	}
	if result.Text != "kept also kept" {
		t.Errorf("Draft() text = %q, want the fragments emitted before the abort", result.Text)
	}
}

// TestDraftAPIRejection: a non-200 carries the API's own explanation, and that
// explanation is what the user is shown — "credit balance is too low" and
// "model not found" are both 400s, and only one means "go top up".
func TestDraftAPIRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(
			`{"type":"error","error":{"type":"invalid_request_error","message":"credit balance is too low"}}`))
	}))
	defer srv.Close()

	_, _, err := collect(t, newTestClient(srv.URL))
	if err == nil {
		t.Fatal("Draft() accepted a 400")
	}
	if msg := serr.UserMsgFromErr(err); !strings.Contains(msg, "credit balance is too low") {
		t.Errorf("user message = %q, want the API's own explanation", msg)
	}
}

// TestDraftMidStreamError: an error frame arrives on a 200 response, so it has
// to be detected in the parser rather than from the status code.
func TestDraftMidStreamError(t *testing.T) {
	srv, _, _ := sseServer(t,
		textDelta("partial draft"),
		frame("error", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`),
	)

	result, _, err := collect(t, newTestClient(srv.URL))
	if err == nil {
		t.Fatal("Draft() ignored a mid-stream error frame")
	}
	if result.Text != "partial draft" {
		t.Errorf("Draft() text = %q, want the fragment received before the error", result.Text)
	}
	if msg := serr.UserMsgFromErr(err); !strings.Contains(msg, "Overloaded") {
		t.Errorf("user message = %q, want the API's own explanation", msg)
	}
}

// TestDraftRefusal: a refusal is a 200 with no draft, so silence here would
// show the user an empty panel and no reason for it.
func TestDraftRefusal(t *testing.T) {
	srv, _, _ := sseServer(t,
		frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"refusal"}}`),
	)

	if _, _, err := collect(t, newTestClient(srv.URL)); err == nil {
		t.Fatal("Draft() reported success on a refusal")
	}
}

// TestDraftEmptyResponse: a stream that carries no text at all is an error, not
// an empty draft silently overwriting a good one.
func TestDraftEmptyResponse(t *testing.T) {
	srv, _, _ := sseServer(t,
		frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`),
	)

	if _, _, err := collect(t, newTestClient(srv.URL)); err == nil {
		t.Fatal("Draft() accepted an empty draft")
	}
}

// TestDraftNeedsKey: the feature is opt-in, and calling it without a key must
// fail before any request is built.
func TestDraftNeedsKey(t *testing.T) {
	c := NewClient("")
	c.Endpoint = "http://127.0.0.1:1" // would fail loudly if it were reached

	if _, err := c.Draft(context.Background(), Request{}, func(Event) error { return nil }); err == nil {
		t.Fatal("Draft() ran without an API key")
	}
}
