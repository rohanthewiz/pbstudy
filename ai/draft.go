// Package ai drafts sermons from an assembled outline using the Anthropic
// Messages API.
//
// # Why net/http and not the SDK
//
// This package makes exactly one kind of request to exactly one endpoint, and
// pbstudy is a single-binary local app whose whole point is that it works
// offline. Adding an SDK (and its dependency tree) to gain features this app
// will never call — batching, files, tool use, agents — would be the larger
// commitment. The streaming parser below is the only real code the SDK would
// have replaced, and it is about sixty lines.
//
// # The feature is optional, and the key never lands anywhere
//
// Drafting is enabled only when ANTHROPIC_API_KEY is set. The key is read once
// at startup into cfg.Config, held in memory, and passed here as a field. It is
// never written to either database, to a sync export, or to a backup — the one
// piece of state in this app that is deliberately not persisted.
package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rohanthewiz/serr"
)

// DefaultModel is the model sermon drafting uses.
//
// Sonnet-tier rather than Opus: this is long-form prose generation from
// material the user already wrote and already believes, not a reasoning problem
// — the hard thinking happened while they were taking the notes. Sonnet 5
// writes it well at a fraction of the cost, which matters for a feature someone
// pays for out of their own pocket on their own laptop.
const DefaultModel = "claude-sonnet-5"

const (
	apiEndpoint = "https://api.anthropic.com/v1/messages"

	// apiVersion is the Anthropic API version header. It is a date, not a
	// semver: pinning it is what keeps a future API change from altering this
	// app's behaviour without a code change.
	apiVersion = "2023-06-01"

	// defaultMaxTokens bounds the whole response — the model's thinking and its
	// visible output share this budget. Generous because a sermon draft is long
	// and a truncated one is worthless; the request streams, so a large ceiling
	// costs nothing but the tokens actually produced.
	defaultMaxTokens = 64000

	// defaultTimeout bounds one whole draft. A sermon takes a few minutes to
	// generate; ten is slack enough that a slow response is not mistaken for a
	// hang, and short enough that a wedged connection cannot pin a goroutine
	// for the life of the process.
	defaultTimeout = 10 * time.Minute
)

// EventKind distinguishes what a streamed Event carries.
type EventKind string

const (
	// EventText is a fragment of the draft. Concatenating every EventText in
	// order reproduces the finished document exactly.
	EventText EventKind = "text"
	// EventStatus is progress the user should see but that is not part of the
	// draft — chiefly "the model is thinking", which would otherwise be a long
	// silence with nothing on screen.
	EventStatus EventKind = "status"
)

// Event is one thing worth telling the browser about.
type Event struct {
	Kind EventKind
	Text string
}

// Client talks to the Messages API.
type Client struct {
	APIKey    string
	Model     string
	MaxTokens int
	// HTTP is exported so tests can point the client at a local server; nil
	// means a default client is built per call.
	HTTP *http.Client
	// Endpoint overrides the API URL. Empty means the real one. Tests set it.
	Endpoint string
}

// NewClient builds a client with the defaults filled in.
func NewClient(apiKey string) Client {
	return Client{
		APIKey:    apiKey,
		Model:     DefaultModel,
		MaxTokens: defaultMaxTokens,
	}
}

// Request is what to draft.
type Request struct {
	// Title is the sermon's title, used only to orient the model.
	Title string
	// OutlineMD is the assembled outline — the same Markdown the export
	// downloads produce. One document, three destinations; see
	// study.AssembleOutline.
	OutlineMD string
}

// Result is what a completed draft came to.
type Result struct {
	// Text is the whole draft, which is also the concatenation of every
	// EventText emitted.
	Text string
	// StopReason is the API's reason for ending, verbatim.
	StopReason string
	// Truncated reports that the draft ran into MaxTokens and is incomplete.
	// Worth surfacing: a draft that stops mid-sentence looks like a bug rather
	// than a budget.
	Truncated bool
}

// Draft streams a sermon draft, calling emit for each event as it arrives.
//
// emit is called from the calling goroutine, in order, and an error it returns
// aborts the draft — that is how a browser closing its EventSource stops the
// generation instead of leaving it running for nobody. The text accumulated so
// far is still returned alongside the error, so a caller that lost its reader
// can still save what was paid for.
func (c Client) Draft(ctx context.Context, req Request, emit func(Event) error) (Result, error) {
	var result Result

	if strings.TrimSpace(c.APIKey) == "" {
		return result, userErr(serr.NewSErr("no Anthropic API key configured"),
			"No API key is configured, so there is nothing to draft with.")
	}

	body, err := json.Marshal(c.buildPayload(req))
	if err != nil {
		return result, serr.Wrap(err, "cannot encode draft request")
	}

	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	endpoint := c.Endpoint
	if endpoint == "" {
		endpoint = apiEndpoint
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return result, serr.Wrap(err, "cannot build draft request")
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", c.APIKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	client := c.HTTP
	if client == nil {
		// No client-level timeout: the context above bounds the whole call, and
		// an http.Client timeout would also cut the response body mid-stream,
		// which is exactly what a long draft looks like.
		client = &http.Client{}
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return result, userErr(serr.WrapAsSErr(err, "cannot reach the Anthropic API"),
			"Could not reach the Anthropic API. Check the machine's network connection.")
	}
	defer func() {
		// Drain before closing so the connection can be reused, but cap the
		// drain: a wedged server must not be able to hold this goroutine.
		_, _ = io.CopyN(io.Discard, resp.Body, 4<<10)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return result, apiFailure(resp)
	}

	return c.readStream(resp.Body, emit)
}

// --- request shape ---------------------------------------------------------

type payload struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	Stream    bool      `json:"stream"`
	System    string    `json:"system"`
	Messages  []message `json:"messages"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// buildPayload assembles the request body.
//
// Note what is absent: no temperature, no top_p, no thinking block. Sonnet 5
// rejects non-default sampling parameters outright, and its adaptive thinking
// is on by default — configuring either would at best be ignored and at worst
// return a 400. The absence is the correct request, not an oversight.
func (c Client) buildPayload(req Request) payload {
	model := c.Model
	if model == "" {
		model = DefaultModel
	}
	maxTokens := c.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = "this sermon"
	}

	user := "Here is the assembled outline for a sermon titled \"" + title + "\".\n\n" +
		"<outline>\n" + req.OutlineMD + "\n</outline>\n\n" +
		"Write the draft."

	return payload{
		Model:     model,
		MaxTokens: maxTokens,
		Stream:    true,
		System:    systemPrompt,
		Messages:  []message{{Role: "user", Content: user}},
	}
}

// systemPrompt is the whole instruction set.
//
// Its through-line is that the preacher's own study is the argument and the
// model is writing it up, not thinking it up. Every rule below exists to keep
// the draft anchored to material the user can verify: the outline's scripture
// came out of a local Bible cache and its notes are the user's own words, so a
// model that paraphrases a verse or invents a supporting citation has produced
// something worse than nothing — text that looks sourced and is not.
const systemPrompt = `You are helping a Christian preacher turn their own study into a sermon draft.

You will be given an outline assembled from their study database. It contains headings and points they wrote, scripture quoted verbatim from a local Bible cache, and the text of their own study notes.

Write a sermon draft in Markdown that follows that outline.

- Follow the outline's order. Its headings are the sermon's movements; keep them as headings.
- Scripture appears as blockquotes with its citation. Quote it exactly as given. Never reword a quoted verse, and never cite a verse that is not in the outline — you do not have a Bible in front of you, only what the outline supplied.
- Where the outline marks text as missing or a note as unavailable, write nothing in its place. Do not reconstruct it.
- The preacher's notes and points are the argument. Develop them; do not replace them with your own thesis.
- Write prose that can be spoken aloud: full sentences, plain words, concrete images. A sermon is not a bulleted list.
- Do not add an introduction, a closing prayer, an altar call, or application points that the outline does not ask for.
- Output the draft and nothing else. No preamble, no commentary on what you wrote.`

// --- streaming -------------------------------------------------------------

// streamEvent is the union of the server-sent event payloads this app reads.
//
// One struct rather than a type per event: the fields do not collide, encoding/
// json ignores what it does not find, and the alternative is a two-pass decode
// (once for the type, once for the body) for no benefit.
type streamEvent struct {
	Type         string `json:"type"`
	ContentBlock struct {
		Type string `json:"type"`
	} `json:"content_block"`
	Delta struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Error *apiErrorBody `json:"error"`
}

// maxSSELine bounds one server-sent event line. Text deltas are a few words;
// a megabyte is far past any legitimate frame and stops a malformed stream from
// growing the buffer without limit.
const maxSSELine = 1 << 20

// readStream parses the event stream, emitting as it goes.
//
// The wire format is one `field: value` line per field and a blank line between
// events. Only `data:` carries anything this app needs — the `event:` line
// duplicates the payload's own "type" field — so the parser reads data lines
// and ignores everything else, including the `:` keep-alive comments.
func (c Client) readStream(r io.Reader, emit func(Event) error) (Result, error) {
	var (
		result   Result
		draft    strings.Builder
		thinking bool
	)

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), maxSSELine)

	for scanner.Scan() {
		data, ok := strings.CutPrefix(scanner.Text(), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" {
			continue
		}

		var ev streamEvent
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			// A frame we cannot read is skipped rather than fatal: the stream is
			// a sequence of independent events, and a future API addition must
			// not be able to abort a draft that is otherwise arriving fine.
			continue
		}

		switch ev.Type {
		case "error":
			result.Text = draft.String()
			return result, ev.Error.err()

		case "content_block_start":
			// Thinking blocks arrive with their text omitted by default, so
			// there is nothing to stream — but the pause before the first word
			// of prose is long enough to look like a failure. Say what is
			// happening instead.
			if ev.ContentBlock.Type == "thinking" && !thinking {
				thinking = true
				if err := emit(Event{Kind: EventStatus, Text: "Thinking…"}); err != nil {
					result.Text = draft.String()
					return result, err
				}
			}

		case "content_block_delta":
			if ev.Delta.Type != "text_delta" || ev.Delta.Text == "" {
				continue
			}
			if thinking {
				thinking = false
				if err := emit(Event{Kind: EventStatus, Text: "Writing…"}); err != nil {
					result.Text = draft.String()
					return result, err
				}
			}
			draft.WriteString(ev.Delta.Text)
			if err := emit(Event{Kind: EventText, Text: ev.Delta.Text}); err != nil {
				result.Text = draft.String()
				return result, err
			}

		case "message_delta":
			if ev.Delta.StopReason != "" {
				result.StopReason = ev.Delta.StopReason
			}
		}
	}

	result.Text = draft.String()

	if err := scanner.Err(); err != nil {
		return result, userErr(serr.WrapAsSErr(err, "the draft stream ended early"),
			"The connection to the Anthropic API dropped before the draft finished.")
	}

	result.Truncated = result.StopReason == "max_tokens"

	// A refusal is a successful HTTP response carrying no draft, so it has to be
	// reported here or it reads as an empty result with no explanation.
	if result.StopReason == "refusal" {
		return result, userErr(serr.NewSErr("the model declined to write this draft"),
			"The model declined to draft from this outline.")
	}
	if strings.TrimSpace(result.Text) == "" {
		return result, userErr(
			serr.NewSErr("the model returned an empty draft", "stopReason", result.StopReason),
			"The model returned an empty draft.")
	}

	return result, nil
}

// --- errors ----------------------------------------------------------------

// userErr attaches a message fit to show the user to an error whose own text is
// written for a log.
//
// The two audiences want different things: the log wants the request status,
// the error type and the endpoint, while the person waiting on a draft wants
// one sentence saying what to do next. serr carries both on the same value —
// Error() stays the developer's string and UserMsgFromErr reads this one — so
// nothing downstream has to choose between logging a useless message and
// showing a raw one.
func userErr(se *serr.SErr, userMsg string) error {
	se.SetUserMsg(userMsg, serr.Severity.Error)
	return se
}

type apiErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (e *apiErrorBody) err() error {
	if e == nil {
		return userErr(serr.NewSErr("the Anthropic API reported an error"),
			"The Anthropic API reported an error mid-stream.")
	}
	return userErr(
		serr.NewSErr("the Anthropic API reported an error",
			"type", e.Type, "detail", e.Message),
		firstNonEmpty(e.Message, "The Anthropic API reported an error mid-stream."))
}

// apiFailure turns a non-200 response into an error carrying the API's own
// explanation.
//
// The API's message is promoted to the user message rather than summarized,
// because it is usually the only thing that distinguishes one 400 from another:
// "credit balance is too low" and "model not found" arrive with the same status
// code, and only one of them means "go top up your account". The body is read
// under a cap so a hostile or broken response cannot be read into memory
// without limit.
func apiFailure(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))

	var envelope struct {
		Error *apiErrorBody `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err == nil && envelope.Error != nil {
		return userErr(
			serr.NewSErr("the Anthropic API rejected the request",
				"status", resp.Status, "type", envelope.Error.Type,
				"detail", envelope.Error.Message),
			firstNonEmpty(envelope.Error.Message,
				"The Anthropic API rejected the request ("+resp.Status+")."))
	}

	return userErr(
		serr.NewSErr("the Anthropic API rejected the request",
			"status", resp.Status, "body", strings.TrimSpace(string(body))),
		"The Anthropic API rejected the request ("+resp.Status+").")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
