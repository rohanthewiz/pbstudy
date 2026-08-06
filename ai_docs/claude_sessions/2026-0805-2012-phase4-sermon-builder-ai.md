# pbstudy — Phase 4: sermon builder, exports, and AI drafting

- **Session ID**: `1ba8e4ff-be95-4300-a428-92015de5dda0`
- **Date**: 2026-08-05
- **Scope**: Implement Phase 4 of `PLAN.md` (sermons CRUD, outline UI,
  `AssembleOutline`, Markdown/HTML export, streaming AI draft)
- **Outcome**: Complete. `go build` / `go vet` / `go test -race` / `gofmt` all
  clean; every behaviour below checked against a live server.

---

## Starting point

Phase 3 left `/sermons` as a `comingSoon` placeholder and the `sermons` table
already in the schema from Phase 1 — `id, title, outline JSONB, draft_md,
status, timestamps`. Nothing had ever written to it, so the first thing checked
was whether bytdb round-trips a `JSONB` column through `database/sql`. It does:
the value comes back as an opaque string with quotes and newlines intact, and a
NULL scans as an invalid `sql.NullString`.

---

## What was built

### `study/sermons.go` — the model

`Section` is one struct with a `Kind` tag (`heading` / `passage` / `note` /
`point`) rather than four types behind an interface: the whole value round-trips
through JSON, and a discriminated union in Go costs a custom `UnmarshalJSON`
for no gain at four variants.

Every outline mutation is a read-modify-write inside a transaction
(`mutateOutline`), which is unavoidable for a whole-column outline but must not
let two tabs interleave a read and a write.

### `study/outline.go` — one assembler, three destinations

`AssembleOutline` produces the Markdown that serves the `.md` download, the
input to the HTML export, **and** the payload sent to the AI drafter. One code
path means the draft is written against exactly the document the user can read,
and an export is exactly what the model saw. It splits into `resolveOutline`
(database) and a pure `formatOutline` (no I/O), which is what makes the document
format testable without a live scripture cache.

### `study/export.go` — a document that leaves the app

`ExportHTML` inlines its whole stylesheet and is light-on-white with a print
block. A `<link>` to `/css/app.css` would render unstyled anywhere pbstudy is
not running, and the app's dark palette is styled for a study session at night,
not for the lectern a sermon is actually delivered from.

### `ai/draft.go` — the Messages API client

net/http only, per the plan. ~60 lines of SSE parsing is the only real code an
SDK would have replaced, against a dependency tree this offline-first binary
would otherwise never carry.

### `web/drafts.go` + `web/handlers_sermons.go` — the streaming path

The registry, the generator goroutine, the SSE framing, and the CRUD/export
handlers.

### `web/ui/sermon.go`, `assets/` — the builder page

Outline rows with per-section move/remove forms, two add forms, the export
links, the live draft panel, and the stored draft rendered underneath it.

---

## Design decisions worth keeping

### The drafting race, and why the POST does not start the work

The plan had `POST /draft` begin generating and `GET /draft/stream` read it.
That races: the generator produces events before the browser has connected, so
they are either buffered indefinitely or lost. The POST now validates and
redirects to `?draft=1`, and **generation begins when the EventSource
connects** — one request, one goroutine, one channel, and no event that
predates its reader. A page refresh mid-draft is handled by the in-flight
registry rather than by a second generation.

### rweb tells you nothing when the client leaves

`sendSSE` detects the disconnect and returns, but exposes no channel, context or
callback to the producing goroutine. A generator that kept sending would block
forever on a channel nobody drains.

The signal used instead is the send itself: a buffered channel (256) with a
timed send, where a send that cannot complete within `Server.draftStall` (45 s)
means the reader has gone. The generator stops, saves the partial — those tokens
were paid for either way — and releases its claim. The timeout fires at most
once per draft, so a departed reader costs one wait, not one per event.

### Two things named wrong would have been silent bugs

- **`event: error` is unusable.** EventSource dispatches its own connection
  failures as an `error` event, so a server-sent one lands on the same handler
  and is indistinguishable from the socket dropping. The failure event is named
  `fail`.
- **Raw text in a `data:` line corrupts the stream.** rweb writes
  `event: %s\ndata: %s\n\n`, and a sermon draft is mostly newlines — a paragraph
  break would terminate the frame mid-word. Fragments travel as JSON.

Both are pinned by tests, because both would look fine until a draft happened to
contain a blank line.

### A stream rejection is a 200, not a 404

An EventSource cannot read a status line or a response body. Every refusal —
no API key, no such sermon, empty outline, already drafting — is delivered as a
one-event `fail` stream on a 200, which the page can actually display.

### Sections carry ids, not positions

An index-addressed Move-up button rendered against position 3 would move
whatever *now* sits at position 3 if the outline changed in another tab. A UUID
minted on append means a stale click either finds its section wherever it
drifted to, or reports that it is gone — which is what the handler does, showing
the outline as it now stands rather than an error page.

### Missing material is marked, never fatal

A deleted note or an uncached passage becomes an italic marker in the document.
The same document goes to a model that must not invent the missing text and to
a preacher who needs to know what to fix; refusing to produce the export because
one of twenty sections lost its source would be the wrong trade.

### Errors carry two messages

serr's user-message channel holds one sentence for the person waiting on a draft
while `Error()` stays the developer's string. The log gets
`status="401 Unauthorized" type=authentication_error detail="invalid x-api-key"
location=...`; the browser gets `invalid x-api-key`. A single message serves
neither audience.

### The request's absent parameters are the correct request

No `temperature`, no `top_p`, no `thinking` block. Sonnet 5 rejects non-default
sampling parameters outright and its adaptive thinking is on by default, so
configuring either would at best be ignored and at worst return a 400.
`TestDraftRequestShape` asserts their **absence**, so a well-meaning future
`"temperature": 0` fails the test rather than every draft.

Adaptive thinking is left on, and `content_block_start` for a thinking block is
surfaced as a "Thinking…" status — with `display` omitted by default there is no
thinking text to stream, and the pause before the first word of prose would
otherwise look like a hang.

---

## Deviations from the plan

- **No JavaScript reorder.** The plan allowed "minimal JS reorder"; the up/down
  buttons are plain single-button POST forms — they work with JS disabled, are
  keyboard-accessible for free, and needed no new endpoint. The only JavaScript
  Phase 4 adds is the draft stream reader, which genuinely cannot be done in
  HTML.
- **Whole-chapter passages cap at 60 inlined verses**, with the cap stated in
  the document. Psalm 119 is 176 verses.
- **`bible.Ref` gained JSON tags.** A Ref is now persisted inside an outline and
  will travel in the Phase 5 sync files, so its wire names are pinned in lower
  camel case. `Section.Ref` is tagged `omitzero`, **not** `omitempty` — the
  latter has no effect on a struct value and would write a zero Ref into every
  heading. (A `go vet`-adjacent diagnostic caught this at the moment the field
  was written.)
- **`comingSoon` deleted.** Every nav destination now resolves to a real page.
- **The dashboard counts sermons**, on the same "only once one exists" terms as
  notes and cross-references.

---

## A race the tests caught

`draftStallTimeout` started as a package-level `var` so tests could shorten it.
`go test -race` failed immediately: one test's deferred restore raced a previous
test's still-running generator goroutine reading it.

The fix improved the design rather than the test — the timeout became
`Server.draftStall`, a per-server field. Each test server now has its own, and
the app has one less piece of mutable package state. Worth noting that the
*unit* tests all passed; only `-race` found it.

---

## End-to-end verification (live server, three translations, seeded data)

| Check | Result |
| --- | --- |
| Create a sermon from the index | 303 → builder |
| Add heading / 2 points / 2 passages / 1 note | all 303 |
| Add `Hezekiah 4:2` | 200, "Could not read … Try the form \"John 3:16-18\"" |
| Outline rows render with kinds | Heading · Point · Point · Passage · Passage · Note |
| Move the note up, then down | order changes and reverts |
| Move the first section up | 303, no-op (not an error) |
| Move a section id that does not exist | 200 with "here is the outline as it stands" |
| `export.md` | John 3:16-18 and Rom 5:8 **verbatim KJV**, note body + anchors |
| `export.md` headers | `text/markdown`, `attachment; filename="the-love-of-god-demonstrated.md"` |
| `export.html` | 2,795 bytes, **zero** external references, `<style>` inline |
| `export.html` headers | `inline; filename="…​.html"` |
| Drafting off: `POST /draft` | 404 |
| Drafting off: the stream | 200 + `event: fail` / "Drafting is off; no API key is configured." |
| Drafting off: the builder | says how to turn it on, renders no draft form |
| Drafting on: `POST /draft` | 303 → `?draft=1` |
| Drafting on: the panel | `data-draft-url`, `-status`, `-text`, `-done` all present |
| Drafting on: the stream (bad key) | reached api.anthropic.com; `event: fail` / "invalid x-api-key" |
| Server log for that draft | full serr context: status, type, detail, location |
| `/css/app.css` | 200, 12,191 bytes, all 8 new rule groups present; 304 on revalidate |
| `/js/app.js` | 200, EventSource reader present |
| Whole route sweep (14 routes) | no 5xx; only the two deliberate 404s |
| Dashboard | counts sermons |
| Hostile title `<script>alert(1)</script>` | escaped in builder, index and HTML export |
| Hostile title → filename | `script-alert-1-script.md` |

The bad-key run is a genuine round trip: the request was accepted as
well-formed and rejected at authentication, which exercises request
construction, the error path and the SSE framing against the real endpoint. No
API key was available in this session, so a *successful* live draft was not
observed — see "Carried forward".

---

## Tests added

| File | Covers |
| --- | --- |
| `study/outline_test.go` | the document format (the highest-value test in the package — it pins all three destinations at once), missing-material markers, truncation notice, JSONB round-trip, decode tolerance, `ValidateSection`, the `FileSlug` allow-list, export self-containment |
| `ai/draft_test.go` | SSE parsing, **absent** sampling parameters, headers, newline preservation, unknown-frame tolerance, truncation, emit-abort returning the partial, API rejection, mid-stream error, refusal, empty draft, missing key |
| `web/drafts_test.go` | one-line JSON framing, the stall detector, claim-once |
| `web/draft_stream_test.go` | the whole path over a real socket against a stand-in API: frames, save-to-database, second-reader refusal, reader-leaves-mid-draft |
| `web/ui/sermon_test.go` | escaping, drafting controls absent without a key, stream-panel contract, `SectionHref` |

`web/draft_stream_test.go` needed one production seam: `Server.aiEndpoint`,
empty in every real build. It is what lets the rweb SSE path — the part reasoned
about from reading rweb's source rather than from running it — actually be
watched writing frames.

---

## State at end of session

Phase 4 complete on `main`. `PLAN.md` carries a full "Phase 4 outcome" section;
`README.md` moved to Phase 4 of 5 and gained "Building a sermon" and "Drafting
with AI" sections.

### Carried forward

- **A successful live AI draft has never been observed.** No `ANTHROPIC_API_KEY`
  was available here. The request shape, the streaming parse, the framing and
  every error path are covered offline, and the real API accepted the request as
  well-formed before rejecting the key — but the happy path against the real
  model wants one run with a real key.
- **ScriptTagger hover popups in a real browser**, still unverified from Phase 2.
  The Chrome instance available here cannot reach this machine's localhost.
- **The escaping rule in `web/ui/escape.go` still has no framework backstop.**
  `ui/sermon_test.go` now pins the sermon-builder half of it.
- **`status` can stick at `drafting`** if the process dies mid-draft. Nothing
  gates on it, so this is cosmetic — but Phase 5's sync engine should not treat
  it as meaningful.
- **Phase 5** is sync: the `syncer` package, JSON export/import with LWW merge,
  the backup route, and the settings page. `sermons` rows are syncable on the
  same terms as notes (UUID, `updated_at`, `deleted_at`), and `bible.Ref` now
  carries the lower-camel JSON names those files will use.
- The dev data dir `/tmp/pbstudy-dev` holds all three translations plus a
  worked sermon ("The love of God demonstrated"), so
  `PBSTUDY_DATA_DIR=/tmp/pbstudy-dev go run . serve` is a ready-made sandbox.
