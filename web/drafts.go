package web

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/serr"

	"github.com/rohanthewiz/pbstudy/ai"
	"github.com/rohanthewiz/pbstudy/study"
)

// Server-sent event names.
//
// Note what is NOT here: an event named "error". EventSource dispatches its own
// connection failures as an "error" event, so a server-sent one would arrive at
// the same handler and be indistinguishable from the socket dropping. "fail" is
// unambiguous.
const (
	sseStatus = "status" // progress worth showing, not part of the draft
	sseDelta  = "delta"  // a fragment of the draft
	sseDone   = "done"   // the draft finished and was saved
	sseFail   = "fail"   // it did not finish; the payload says why
)

const (
	// draftBuffer is how many events may sit between the generator and the
	// browser. Deltas arrive in bursts of a few words; a few hundred slots
	// absorb a burst without the generator ever waiting on the socket.
	draftBuffer = 256

	// defaultDraftStall is how long a send waits before concluding nobody is
	// listening.
	//
	// This is the only way the generator learns the browser has gone: rweb's SSE
	// loop detects the disconnect and returns, but it has no channel to tell us
	// through, so a full buffer that never drains IS the signal. Long enough
	// that a backgrounded tab or a slow link is never mistaken for a departure,
	// short enough that an abandoned draft cannot outlive its reader by more
	// than a minute.
	//
	// It is carried on the Server rather than read from here directly, so a
	// test can shorten it for one server without reaching into package state
	// that another server's goroutines are reading.
	defaultDraftStall = 45 * time.Second
)

// errClientGone reports that the browser stopped reading, which aborts the
// draft rather than failing it — there is nobody left to tell.
var errClientGone = errors.New("the browser stopped listening")

// draftJobs tracks which sermons are being drafted right now.
//
// # Why this exists
//
// EventSource reconnects automatically when a stream closes, and a page refresh
// opens a second one. Without a guard, either would start a second generation
// for the same sermon — two goroutines writing the same draft_md, and two bills.
// The registry makes the stream endpoint idempotent per sermon: the first
// request generates, the rest are told a draft is already running.
type draftJobs struct {
	mu     sync.Mutex
	active map[string]bool
}

func newDraftJobs() *draftJobs { return &draftJobs{active: map[string]bool{}} }

// draftClient builds the client one draft runs on.
//
// The endpoint override is the single seam the end-to-end test needs: it points
// the drafter at a local stand-in for the Messages API so the whole path —
// handler, goroutine, channel, rweb's SSE writer, and the save back to the
// study database — can be exercised without a network call or an API key. It is
// empty in every real build, and ai.Client falls back to the real endpoint.
func (s *Server) draftClient() ai.Client {
	c := ai.NewClient(s.cfg.AnthropicKey)
	c.Endpoint = s.aiEndpoint
	return c
}

// begin claims a sermon, reporting false if it was already claimed.
func (d *draftJobs) begin(sermonID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.active[sermonID] {
		return false
	}
	d.active[sermonID] = true
	return true
}

func (d *draftJobs) end(sermonID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.active, sermonID)
}

// runDraft generates a draft, streaming it to ch and saving it when done.
//
// It owns ch: it closes it on every exit path, which is what ends the SSE
// response cleanly, and it releases the registry claim on the way out.
func (s *Server) runDraft(sermonID, title, outlineMD string, ch chan any) {
	defer s.drafts.end(sermonID)
	defer close(ch)

	client := s.draftClient()

	// context.Background rather than the request's: this goroutine deliberately
	// outlives the handler that started it (the handler returns before rweb
	// begins streaming), and ai.Draft applies its own timeout. The browser
	// leaving is handled by sendEvent below, not by cancellation.
	result, err := client.Draft(context.Background(),
		ai.Request{Title: title, OutlineMD: outlineMD},
		func(ev ai.Event) error {
			if ev.Kind == ai.EventStatus {
				return sendEvent(ch, sseStatus, ev.Text, s.draftStall)
			}
			return sendEvent(ch, sseDelta, ev.Text, s.draftStall)
		})

	// Save before reporting. Whatever arrived was generated and paid for, so it
	// is stored even when the run ended badly — a draft cut off two paragraphs
	// early is worth more than an empty page, and the user can see for
	// themselves that it stops mid-sentence.
	s.saveDraft(sermonID, result.Text)

	if err != nil {
		if errors.Is(err, errClientGone) {
			// Nobody is reading, so there is nothing to send and no error to
			// report — the user closed the tab. Worth a log line because the
			// tokens were still spent.
			logger.Info("draft abandoned by the browser", "sermon", sermonID,
				"chars", len(result.Text))
			return
		}
		logger.LogErr(err, "sermon draft failed", "sermon", sermonID)
		_ = sendEvent(ch, sseFail, serr.UserMsgFromErr(err,
			"Drafting failed. The server log has the detail."), s.draftStall)
		return
	}

	if result.Truncated {
		_ = sendEvent(ch, sseStatus,
			"The draft reached its length limit and stops early.", s.draftStall)
	}
	_ = sendEvent(ch, sseDone, "", s.draftStall)
}

// saveDraft stores what was generated and moves the sermon to the matching
// status. A failure here is logged rather than surfaced: the draft is already
// on the user's screen, and telling them it could not be saved is only useful
// if they can act on it — which the log line, naming the database, lets them do.
func (s *Server) saveDraft(sermonID, text string) {
	var err error
	if strings.TrimSpace(text) == "" {
		// Nothing came back, so leave the sermon exactly as it was rather than
		// recording an empty draft over a previous good one.
		err = study.SetStatus(s.store.Study, sermonID, study.StatusOutline)
	} else {
		err = study.SetDraft(s.store.Study, sermonID, text, study.StatusDrafted)
	}
	if err != nil {
		logger.LogErr(err, "cannot save sermon draft", "sermon", sermonID)
	}
}

// sendEvent puts one event on the wire, or reports that nobody is there.
//
// stall is the disconnect detector described on defaultDraftStall. Note that it
// fires at most once per draft: the first stall returns errClientGone and the
// generator stops, so a departed reader costs one wait, not one per event.
func sendEvent(ch chan any, name, text string, stall time.Duration) error {
	payload, err := json.Marshal(struct {
		Text string `json:"text"`
	}{Text: text})
	if err != nil {
		// Only unencodable values can fail here and Text is a string, so this
		// is unreachable — but dropping the event silently would be worse than
		// saying so.
		return serr.Wrap(err, "cannot encode draft event")
	}

	timer := time.NewTimer(stall)
	defer timer.Stop()

	select {
	// JSON, not the raw text: the SSE wire format is line-based, and a draft is
	// full of newlines. A bare "data: " line carrying a paragraph break would
	// terminate the event mid-word and hand the browser a corrupt stream.
	// Encoding escapes every newline into the one line the protocol allows.
	case ch <- rweb.SSEvent{Type: name, Data: string(payload)}:
		return nil
	case <-timer.C:
		return errClientGone
	}
}
