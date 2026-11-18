# Deep look: reliability, performance, usability

Session: `49bfcda7-a9df-4887-a9af-3725e21d443e` · 2026-11-18

The ask: take a deep look at pbstudy, find what can be improved in
reliability, performance and usability, make a plan, raise Next-list items,
and start on it.

Outcome: 17 items raised (N-022 to N-038). Eight are fixed and closed, one is
built and waiting on a by-hand check, and eight are open, three of them as
questions for the user. The work is one commit on `main`, with this doc.

---

## How the survey was done

Read every non-test file in `store`, `cfg`, `syncer`, `study`, `web`, `ai`
and `bible`, the two scripts, and the views for notes and sermons. Then
measured, with a throwaway test in `syncer/` (deleted afterwards), what the
code does at 500 and 2000 notes, each with two anchors and two tags.

| Operation | 500 notes | 2000 notes |
| --- | --- | --- |
| Create a note (one commit) | 3.0 ms | 3.0 ms |
| First export of everything | 84 ms | 314 ms |
| Debounced export with nothing to write | 13 ms | 52 ms |
| First import on a new machine | 1.5 s | 6.1 s |
| `ListNotes` (capped at 500 rows) | 10 ms | 62 ms |
| `SearchNotes` | 10 ms | 32 ms |
| `ChapterMarks`, every note in one chapter | 1.2 ms | 5.8 ms |
| `ListTags` | 2 ms | 7 ms |

**Performance is not where the problems are.** The only number a person
would notice is the first import on a new machine, which happens once
(N-032). The findings that matter are in reliability and usability.

---

## The plan

Everything is in `ai_docs/todo/next-list.md`. In short:

| ID | Area | What | State |
| --- | --- | --- | --- |
| N-022 | Reliability | A cut-off draft stream was saved as finished | closed |
| N-023 | Reliability | A finished draft was not exported to the sync folder | closed |
| N-024 | Reliability | An unclean exit stranded the last edit | closed |
| N-025 | Reliability | Sync conflict copies were read as records | closed |
| N-026 | Reliability | No cross-origin guard on POST | closed |
| N-027 | Reliability | Listens on every interface with no auth | open, question |
| N-028 | Reliability | Download failed on the first bad response | closed |
| N-029 | Reliability | Backups are manual and never pruned | open, question |
| N-030 | Usability | Restore is undocumented and fights sync | open |
| N-031 | Reliability | Dir check does not resolve symlinks | open |
| N-032 | Performance | First import fsyncs once per record | open |
| N-033 | Performance | Scripts had no cache validator | closed |
| N-034 | Usability | Lists stop at 500 rows without saying so | open |
| N-035 | Usability | Outline sections could not be edited | closed |
| N-036 | Usability | No way to restore a deleted note or sermon | open |
| N-037 | Usability | Editor dropped unsaved text silently | built, in Validate |
| N-038 | Usability | The AI draft has no export | open |

---

## What changed

### N-022: a draft stream that just stops (`ai/draft.go`)

`readStream` treated a clean end of the response body as success whenever
some text had arrived. A connection closed in an orderly way mid-draft
therefore produced "Draft finished and saved." over half a draft. It now
returns an error unless it saw a `stop_reason` or `message_stop`. The partial
text still comes back with the error, so `runDraft` saves it.
`TestDraftStreamCutShort` failed before the fix.

### N-023: the draft never reached the sync folder (`web/drafts.go`)

`syncOnWrite` schedules an export after a POST. The draft is saved by a
goroutine minutes later, on a GET stream, so nothing scheduled one.
`saveDraft` now calls `Syncer.Touch`. `Syncer.Pending` is new (exported so
the web test can see the flag without waiting out the debounce).

### N-024: unclean exits (`main.go`)

- SIGHUP joins SIGINT and SIGTERM in `installShutdownFlush`, so closing the
  terminal window flushes.
- `cmdServe` calls `sync.Touch()` after a successful startup import. That
  writes only rows newer than their files, which after a clean exit is none.

Checked on a scratch server with a script that ran it under a pty:

1. Saved a note, closed the pty at once: exit status 0, the note's file was
   written.
2. Saved a note, `kill -9` inside the debounce: no file for it.
3. Started again, made no edit, waited 3.5 s: the file was there.

### N-025: conflict copies (`syncer/files.go`)

Syncthing's `<id>.sync-conflict-….json` and iCloud's `<id> 2.json` end in
`.json` and carry the real record's id. The failing tests showed two things:
the older copy winning on a new machine's first pass, and a stray copy
bringing a compacted note back as a row.

The rule now: a `.json` file not named for the id it carries is not a record.
It is never imported, never used as the file's stamp by the export, and named
as a problem on every pass until it is removed. It is not merged even when it
is the newer one, because it outlives the record: after the note is deleted
and compacted, the stray would be the only file with that id and would
resurrect it. `serve` now prints the problem lines at startup, where it used
to print only their count.

### N-026: cross-origin POST guard (`web/origin.go`, new)

A middleware refuses any non-GET request unless the browser's own labels say
it came from this app: `Sec-Fetch-Site` must be `same-origin` or `none`; when
that header is absent, `Origin`'s host must equal the `Host` header; when
both are absent (curl, scripts, tests) the request passes. `same-site` is
refused, since another program on a different localhost port is same-site.
The draft stream, a GET that spends money, makes the same check.

rweb's `Request.Host()` comes from the request line and is always
`localhost`, so the check reads the `Host` header directly. The end-to-end
test covers that path.

### N-028: download retry (`bible/download.go`)

Each book gets up to three attempts with waits of 1 s and 4 s. A network
error, a 5xx, a 429 and a truncated body are retried; any other 4xx fails at
once. `getBibleBaseURL` became a variable so the test can point it at a local
server. Not run against getbible.net.

### N-033: script caching (`web/server.go`)

`serveJS` sent `Cache-Control: no-cache` with nothing to revalidate against.
It now sends an ETag and answers `If-None-Match` with 304. The scratch
server's log showed `GET "/js/app.js" -> 304` from the second page on.

### N-035: editing an outline section

- `study.UpdateSection` replaces a heading's or point's text, or a passage's
  reference, and keeps the section's id, kind and position. Note sections are
  refused. `study.SectionEditable` says which kinds qualify.
- `POST /sermons/:id/sections/:sectionId/edit` (`handleSectionEdit`). A
  reference that does not parse comes back with the row still open on what
  was typed.
- The builder row gets a pencil link to `?edit=<section id>`, and that row
  renders as a form. No JavaScript.

Checked in Chrome on a scratch server. The first try found a defect the tests
could not: **Chrome skips `autofocus` when the URL has a fragment.** The edit
link carried `#sec-<id>` to scroll to the row, so the input opened unfocused
and the first keystroke went to the page. The link now has no fragment;
autofocus scrolls the input into view on its own. Cancel and the redirect
after a save keep the fragment. After the fix: clicked the pencil on "The
problm", typed the correction, pressed Return, and landed back on the row
with the heading fixed in place.

### N-037: unsaved text (`assets/js/app.js`)

Forms marked `data-guard-unsaved` (the note editor and the verse hub's quick
note) ask before the page is left with unsaved typing, and Cmd/Ctrl+S saves.
An editor re-rendered after a rejected save is marked `dirty` by the server,
so it starts out unsaved.

Checked in Chrome with a synthetic `beforeunload` event: no prompt requested
before typing, one requested after, Cmd+S saved and left cleanly, and the
rejected-save page asked without any typing. The real "Leave site?" dialog
was not triggered, because a modal dialog blocks the browser extension. That
check is what keeps N-037 in Validate.

### Docs

`README.md`: the pencil, the catch-up export, conflict copies (with the real
report text), the unsaved-text guard, and a new section on refused cross-site
requests. `syncer/syncer.go`'s package comment states the one-file-per-record
rule.

---

## State at end of session

- Committed on top of `653673f` together with this doc, and pushed.
- `go build ./...`, `go vet ./...` and `go test -race ./...` pass.
- The scratch servers were stopped by PID and the Chrome tab was closed. The
  scratch data lives only in the session scratchpad.

## Next

Closed: N-022, N-023, N-024, N-025, N-026, N-028, N-033, N-035.
Declined: None. Raised: N-022 to N-038. Deferred: None. Promoted: None.
Moved: N-037 → Validate. Updated: N-003 (measured; nothing to fix at
realistic sizes). Full list: `ai_docs/todo/next-list.md`.
