package web

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rohanthewiz/rweb"
)

// TestSendEventIsOneLine is the load-bearing test for the SSE transport.
//
// rweb writes an event as "event: <name>\ndata: <payload>\n\n". A sermon draft
// is mostly newlines, so handing the raw text to that formatter would end the
// event at the first paragraph break and push the rest of the draft onto the
// wire as malformed frames. JSON encoding is what keeps a multi-line fragment
// on the single line the protocol allows.
func TestSendEventIsOneLine(t *testing.T) {
	text := "## Heading\n\nA line.\r\nAnother \"line\" with a \\ backslash.\n"

	ch := make(chan any, 1)
	if err := sendEvent(ch, sseDelta, text, defaultDraftStall); err != nil {
		t.Fatalf("sendEvent() error: %v", err)
	}

	ev, ok := (<-ch).(rweb.SSEvent)
	if !ok {
		t.Fatal("sendEvent() did not put an rweb.SSEvent on the channel")
	}
	if ev.Type != sseDelta {
		t.Errorf("event name = %q, want %q", ev.Type, sseDelta)
	}

	payload, ok := ev.Data.(string)
	if !ok {
		// rweb formats Data with %s, so anything but a string arrives at the
		// browser as Go's default struct rendering.
		t.Fatalf("event data is %T, want a string", ev.Data)
	}
	if strings.ContainsAny(payload, "\n\r") {
		t.Errorf("event data carried a raw newline, which would truncate the frame: %q", payload)
	}

	// And it decodes back to exactly what went in — no fragment may be altered
	// in transit, or the saved draft and the streamed one would differ.
	var decoded struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("event data was not JSON: %v (%q)", err, payload)
	}
	if decoded.Text != text {
		t.Errorf("round-tripped text = %q, want %q", decoded.Text, text)
	}
}

// TestSendEventReportsAGoneReader covers the disconnect detector: rweb gives us
// no signal when the browser leaves, so a send that cannot complete IS the
// signal. Without it the generator goroutine would block on a full channel
// forever.
func TestSendEventReportsAGoneReader(t *testing.T) {
	// A full channel with nobody draining stands in for a departed browser.
	ch := make(chan any, 1)
	ch <- "occupied"

	// A short wait here; the production value is deliberately long enough that
	// a backgrounded tab is never mistaken for a closed one.
	start := time.Now()
	err := sendEvent(ch, sseDelta, "nobody will read this", 20*time.Millisecond)

	if !errors.Is(err, errClientGone) {
		t.Fatalf("sendEvent() error = %v, want errClientGone", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("sendEvent() waited %v before giving up", elapsed)
	}
}

// TestDraftJobsClaimsOnce is what stops a reconnecting EventSource — or a
// second browser tab — from starting a parallel generation for the same sermon.
func TestDraftJobsClaimsOnce(t *testing.T) {
	jobs := newDraftJobs()

	if !jobs.begin("s1") {
		t.Fatal("the first claim on a sermon was refused")
	}
	if jobs.begin("s1") {
		t.Error("a second claim on the same sermon was allowed")
	}
	if !jobs.begin("s2") {
		t.Error("a claim on a different sermon was refused")
	}

	jobs.end("s1")
	if !jobs.begin("s1") {
		t.Error("the sermon could not be claimed again after its draft ended")
	}
}

// TestJSONString covers the refusal payloads, which are built by hand rather
// than from an event struct.
func TestJSONString(t *testing.T) {
	got := jsonString(`he said "no"` + "\nand meant it")

	if strings.ContainsAny(got, "\n\r") {
		t.Errorf("jsonString() left a raw newline: %q", got)
	}

	var back string
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatalf("jsonString() did not produce a JSON string: %v (%q)", err, got)
	}
	if want := "he said \"no\"\nand meant it"; back != want {
		t.Errorf("jsonString() round-tripped to %q, want %q", back, want)
	}
}
