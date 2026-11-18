# pbstudy — Next list

The project's one living list of follow-ups. Sessions edit this file in place
(`/sess-save`) instead of copying a Next section forward from the previous
session doc, so an item that leaves the list does so as a visible edit in git
history rather than by being left out of a paste. `/next-list` re-checks every
item's premise against the code and restores anything that lapsed.

Seeded 2026-10-12 from all seven session docs (`2026-0805-1823` through
`2026-0807-0954`), whose Next sections were titled "Carried forward".

## Conventions

- **IDs are permanent and never reused.** `N-###`, one sequence for the whole
  file. A closed or declined item keeps its ID.
- **`raised`** is the stem of the session doc an item first appeared in, even
  if a later doc reworded it.
- **Age is computed, never stored:** the number of session docs since `raised`
  (an item raised in the newest doc has age 0). It counts sessions, not days.
- **Value** is the payoff of doing the item, not the effort:
  - `high`: something is being worked around today, or a second independent
    consumer has arrived
  - `medium`: it blocks one named thing, or it is a visible defect nobody has
    to route around yet
  - `low`: a gap nobody has bumped into, or contingent on something that does
    not exist
- **Sections:**
  - **Open** is the next build work: code to write, a defect to fix, a
    decision to make.
  - **Validate** is the next testing: run, look, hear, measure, or write or
    repair a test. No product change is planned unless the check finds a
    defect.
  - **Roadmap** is wanted, but later.
  - **Non-goals** is likely never, kept so the decision stays visible.
- **Nothing leaves Open, Validate or Roadmap without a line in another
  section** (Closed, Non-goals, or a move between the three).
- **Open, Validate and Roadmap stay in ID order.** Never renumber.

**Next ID:** N-039

## Open

- **N-003** · raised `2026-0805-1917-phase2-notes-tags-xrefs` · value low
  The reader loads `ChapterMarks` on every chapter render
  (`web/handlers_read.go:143`). Fine at present scale; if it ever shows up, the
  fix is a per-chapter cache invalidated on study writes, not a schema change.
  Lapsed once: dropped from `2026-0805-1935` without being done.
  *Measured 2026-11-18 (`2026-1118-1346-deep-look-reliability-performance-usability`):*
  with every note in the database anchored in the one chapter, `ChapterMarks`
  took 1.2 ms at 500 notes and 5.8 ms at 2000. Nothing to fix at any size a
  personal database reaches.

- **N-005** · raised `2026-0805-2048-phase5-sync` · value low
  Live-tag duplicate files still accumulate, one per machine per name.
  Compaction clears them once they are *tombstoned* (the orphan branch in
  `syncer/compact.go`), but a shared live tag keeps its redundant file on every
  machine. A different cleanup than compaction: remove a file whose id no
  longer exists anywhere.
  *Premise corrected 2026-10-14 (`2026-1014-0741-work-next-list-n019-n005-n001`):*
  that cleanup cannot work for a live tag. The redundant file's id still
  exists on the machine that minted it (`ApplyTag` keeps both local ids, by
  design), and export rewrites any local row whose file is missing
  (`syncer.go`, `doExport`). So deleting it here just makes the other
  machine write it back on its next pass. The fix that would converge is
  **id convergence** on a name merge: both machines adopt one deterministic
  id (say, the lexicographically smaller UUID), re-point `note_tags`, and
  tombstone the losing id so its file is cleared by compaction. That changes
  tag identity across machines, which `ApplyTag`'s comment currently argues
  is unnecessary. **Question for the user:** worth doing for one small file
  per machine per shared tag, or move this to Non-goals?

- **N-007** · raised `2026-0805-2238-compaction` · value low
  Compaction is per machine and has to be run on each. A machine that has not
  compacted re-exports its tombstone files; the others import and remove them
  again next pass. Bounded churn, converges once both compact, and what comes
  back is a tombstone, invisible to every read in `study/`.

- **N-020** · raised `2026-1014-0741-work-next-list-n019-n005-n001` · value low
  Sonnet 5.5's safety classifiers can decline a request (`stop_reason:
  "refusal"`). `ai/draft.go` already reports that as a user-facing error
  rather than an empty draft. The API also offers server-side fallbacks
  (beta `server-side-fallback-2026-07-01` + `fallbacks: "default"`), which
  re-serve a declined request on another model inside the same call. Not
  added with N-019 because it adds a beta header to a hand-rolled client and
  changes how a refusal is billed. A sermon draft is unlikely to trip a
  classifier, so this matters only if refusals are ever seen (N-004's live
  run would be the first chance).

- **N-027** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · value medium
  *Reliability; a decision.* The server binds every interface with no
  authentication (`cfg.Address` returns `":port"`, deliberately, for
  reading from a phone on the LAN). On a shared network anyone can read and
  delete the notes and spend the API key through the draft endpoint. N-026
  does not cover this: these are direct requests, not a browser being
  tricked. Options: (a) bind `127.0.0.1` by default and add
  `PBSTUDY_BIND=0.0.0.0` for LAN use; (b) keep the default and add the
  variable for people who want loopback only; (c) leave it. Related: with a
  name-based `Host` allowlist, DNS rebinding could also be shut out.
  **Question for the user:** which default?

- **N-029** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · value medium
  *Reliability; a decision.* Snapshots are the stated safety net for N-008,
  but one is written only when the user presses the button or runs
  `pbstudy backup`, and none is ever removed, so the synced backups folder
  either stays empty or grows forever. Proposal: `serve` writes a snapshot
  at startup when the newest one is more than a day old, and keeps the
  newest N files that match its own `study-<stamp>.bytdb` naming.
  **Question for the user:** is automatic pruning in the sync folder
  acceptable, and how many to keep (14 suggested)?

- **N-030** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · value low
  *Usability.* Restoring a snapshot is undocumented, and it fights sync. The
  error page says "restore the most recent snapshot" but nothing says how
  (stop pbstudy, replace `study.bytdb` with the snapshot). With sync on, a
  restored database has older `updated_at` values than the files in the
  sync folder, so the next import re-applies everything newer and undoes
  the restore for those rows. Recovering one lost note needs the opposite:
  read a snapshot beside the live database and copy one row out. Wanted: a
  README section at least, and possibly `pbstudy restore <file>` or a
  read-only "open snapshot" mode.

- **N-031** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · value low
  *Reliability.* `cfg.checkDirsDisjoint` compares cleaned paths without
  resolving symlinks, so a data directory reached through a link into the
  sync folder (or the reverse, e.g. `~/iCloud` → `~/Library/Mobile
  Documents/...`) passes the one check that keeps the live database away
  from the sync daemon. Fix: resolve both through `filepath.EvalSymlinks`
  (the deepest existing ancestor for a sync dir not created yet) before
  comparing.

- **N-032** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · value low
  *Performance.* The first import on a new machine commits once per record,
  and each commit is an fsync: measured 3.0 ms per note, so 1.5 s for 500
  notes and 6.1 s for 2000, before the first page is served. It happens
  once per machine. If it ever matters, apply each entity's files inside
  one transaction. Everything else measured at 2000 notes is fine: the
  debounced no-op export 52 ms, `SearchNotes` 32 ms, `ListNotes` 62 ms,
  `ChapterMarks` 6 ms.

- **N-034** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · value low
  *Usability.* The notes list, the sermons list and the sermon builder's
  note picker stop at 500 rows without saying so (`DefaultNoteLimit`,
  `DefaultSermonLimit`); note 501 is reachable only by search. The picker
  also loads every note whole (body, anchors, tags) to print titles. Fix
  when someone gets near the cap: a "showing the newest 500" notice like the
  search page's, and a titles-only query for the picker.

- **N-036** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · value medium
  *Usability.* A deleted note or sermon cannot be brought back from the UI,
  although the row, and a note's anchors, are kept as a tombstone until
  `pbstudy compact` removes them (90 days by default). The delete button
  says the note is "retired rather than erased" and then offers no way to
  see it again. Wanted: a "Recently deleted" page with Restore (clear
  `deleted_at`, bump `updated_at` so the undelete wins the sync clock).
  Open detail: a note's tag links survive its own delete but not the
  delete of the tag.

- **N-038** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · value low
  *Usability.* The AI draft can be read only on the builder page. Both
  exports are the outline; the draft, the thing that was paid for, has no
  download. Fix: `/sermons/:id/draft.md` and a button beside the draft.

## Validate

Items whose remaining work is purely testing: run, look, hear, measure, or
write or repair a test. No product change is planned unless the check finds a
defect.

- **N-004** · raised `2026-0805-2012-phase4-sermon-builder-ai` · value medium
  A successful live AI draft has never been observed (no `ANTHROPIC_API_KEY`
  in any session so far). The request shape, the streaming parse, the framing
  and every error path are covered offline, and the real API took the request
  as far as rejecting the key. The happy path against the real model wants
  one run with a real key. Since N-019 the model is `claude-sonnet-5-5`, so
  that run observes the model that ships: confirm a thinking status appears
  before the first prose and the draft saves.

- **N-037** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · value medium
  *Usability.* The note editor dropped unsaved text without a word. Built
  2026-11-18: forms marked `data-guard-unsaved` (the note editor and the
  verse hub's quick note) ask before the page is left with unsaved typing,
  and Cmd/Ctrl+S saves (`assets/js/app.js`). Checked in Chrome on a scratch
  server with a synthetic `beforeunload` event: no prompt before typing, a
  prompt requested after typing, Cmd+S saved and left without one, and an
  editor re-rendered after a rejected save starts out unsaved. **Still to
  see:** the browser's real "Leave site?" dialog on an actual navigation
  (not triggered through the extension, because a modal dialog blocks it),
  and the same in Safari. Type in a note, click a nav link, expect the
  prompt.

## Roadmap

Wanted, but deferred. Parked, not declined. No session doc has deferred an
item yet; items move here from Open when the user decides they are "later".

## Non-goals

- **N-008** · declined `2026-0805-2048-phase5-sync` — Two machines editing the
  same note while both are offline still loses one side (last write wins).
  Accepted for a single user; the snapshots are the net.
- **N-009** · declined `2026-0805-2048-phase5-sync` — Translation download
  buttons on the settings page. Downloading 66 books from a web handler needs
  its own progress stream and concurrency guard, and `pbstudy download`
  already does the job.
- **N-010** · declined `2026-0805-2012-phase4-sermon-builder-ai` — JavaScript
  reorder for outline sections. The up/down buttons are plain POST forms that
  work with JS disabled and are keyboard-accessible for free.
- **N-011** · declined `2026-0805-2238-compaction` — A compaction button in the
  settings page. Compaction is neither additive nor reversible, and the
  retention window that makes it safe is one only the user can choose; it
  stays a command.
- **N-012** · declined `2026-0805-2238-compaction` — A database-only
  compaction mode. bytdb's file is append-only, so `DELETE` reclaims nothing on
  disk; the saving is in the sync folder. A smaller database is
  `pbstudy backup`. *Premise loosened 2026-10-12:* bytdb v0.19+ has `VACUUM`
  (`Engine.Compact`), which does shrink the file on demand, and btypedb
  auto-compacts the log once it has grown enough. The decision may deserve a
  second look. Since 2026-10-14 (N-017), `compact` ends every real pass by
  compacting the file, but it still requires a sync directory, so there is
  no database-only mode.

## Closed

Closures before this file existed are recorded in the session docs' bodies.
The entries below are the ones found done while seeding the file.

- **N-035** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · closed 2026-11-18, `2026-1118-1346-deep-look-reliability-performance-usability` —
  An outline section could not be edited. Heading, point and passage rows
  now carry a pencil that reopens the page with `?edit=<section id>`, which
  renders that row as a form; `POST /sermons/:id/sections/:sectionId/edit`
  saves through the new `study.UpdateSection`, which keeps the section's id,
  kind and position. A rejected reference comes back with the row still open
  on what was typed. No JavaScript. Checked in Chrome: fixed "The problm" in
  place and landed back on the row. That check found that Chrome skips
  `autofocus` when the URL has a fragment, so the edit link carries none.
  Tests: `TestUpdateSection`, `TestSermonBuilderSectionEditing`,
  `TestSectionEditEndToEnd`.
- **N-033** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · closed 2026-11-18, `2026-1118-1346-deep-look-reliability-performance-usability` —
  `/js/*` had no validator. `serveJS` now sends an ETag (hash of the bytes)
  and answers `If-None-Match` with 304. The scratch server's log showed
  `GET "/js/app.js" -> 304` on every page after the first.
  `TestScriptsRevalidate` pins it.
- **N-028** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · closed 2026-11-18, `2026-1118-1346-deep-look-reliability-performance-usability` —
  `pbstudy download` failed on the first bad response. Each book is now
  tried up to three times (waits of 1 s and 4 s) on a network error, a 5xx,
  a 429 or a truncated body; any other 4xx fails at once. Tested against a
  local stand-in server (`bible/download_test.go`). Not run against
  getbible.net.
- **N-026** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · closed 2026-11-18, `2026-1118-1346-deep-look-reliability-performance-usability` —
  No cross-origin guard on POST. New `web/origin.go`: a middleware refuses
  any non-GET request whose `Sec-Fetch-Site` is not `same-origin`/`none`, or
  whose `Origin` host differs from the `Host` header; requests with neither
  header pass. The draft stream (a GET that spends money) makes the same
  check. Tests cover the rule as a table and through a real server; in
  Chrome the app's own forms still save. A cross-site page was not built to
  attack it for real. Who may reach the port at all is N-027.
- **N-025** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · closed 2026-11-18, `2026-1118-1346-deep-look-reliability-performance-usability` —
  Conflict copies were read as records. The failing test showed the older
  copy winning on the first pass ("Loser"), and a stray copy bringing a
  compacted note back as a row. Now a `.json` file not named for the id it
  carries is never imported, never counts as the file's stamp for export,
  and is named as a problem on every pass until removed (`strayCopy` in
  `syncer/files.go`). Not merged even when newer, because it outlives the
  record and would resurrect it after compaction. `serve` now also prints
  the problem lines at startup, not only their count.
- **N-024** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · closed 2026-11-18, `2026-1118-1346-deep-look-reliability-performance-usability` —
  An unclean exit stranded the last edit. SIGHUP now runs the shutdown
  flush, and `serve` schedules one debounced export after its startup
  import. Checked on a scratch server: closing the pty it ran under left the
  note's file written and exit status 0; after `kill -9` inside the debounce
  the file was missing, and 3.5 s into the next start it was there with no
  edit made.
- **N-023** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · closed 2026-11-18, `2026-1118-1346-deep-look-reliability-performance-usability` —
  A finished AI draft was not exported. `saveDraft` now calls
  `Syncer.Touch`. `TestSaveDraftSchedulesSyncExport` checks the pending flag
  through the new exported `Syncer.Pending`.
- **N-022** · raised `2026-1118-1346-deep-look-reliability-performance-usability` · closed 2026-11-18, `2026-1118-1346-deep-look-reliability-performance-usability` —
  A draft stream that stopped without a stop reason was saved as finished.
  `readStream` now returns an error (with the partial text, which is still
  saved) unless it saw a `stop_reason` or `message_stop`.
  `TestDraftStreamCutShort` failed before the fix. Not seen against the real
  API; N-004's live run is the chance.
- **N-021** · raised `2026-1014-0741-work-next-list-n019-n005-n001` · closed 2026-10-14, `2026-1014-0741-work-next-list-n019-n005-n001` —
  Found by N-001's check: the ScriptTagger config never took effect. The
  tagger replaces `BLB.Tagger` with its defaults on load, so the inline
  object emitted *before* the script tag was discarded: popups quoted NKJV,
  `HyperLinks` stayed `"none"` (it takes `"all"/"none"/"hover"`, not a
  bool), and `blb-no-tag` was never excluded, so scripture containers were
  tagged too (a control page in Chrome tagged a `blb-no-tag` div). Now a
  deferred `/js/blb-tagger.js` runs right after the tagger, reads
  translation and exclusion class from data attributes, and sets the
  documented properties. Checked in Chrome on the real app: `Translation`
  KJV, `HyperLinks` "all", `NoSearchClasses` includes `blb-no-tag`, popup
  reads "Romans 8:28 (KJV)". `TestTaggerConfigFollowsTagger` pins the order.
- **N-001** · raised `2026-0805-1823-phase1-skeleton-reader` · closed
  2026-10-14, `2026-1014-0741-work-next-list-n019-n005-n001` — ScriptTagger hover popups in a real browser, inside
  note bodies. Chrome (via the extension) now reaches localhost. On a
  scratch server, a note body's "John 3:16", "Romans 8:28" and "Eph 2:8-9"
  were all tagged, and hovering one opened the BLB popup with the verse
  text. The app's own reference chip and the scripture card were not tagged.
  The check also found that the tagger config was being ignored, raised and
  fixed as N-021. After the fix the popup quotes KJV.
- **N-019** · raised `2026-1012-0131-bytdb-v0212-upgrade-next-list-seed` ·
  closed 2026-10-14, `2026-1014-0741-work-next-list-n019-n005-n001` — `DefaultModel` is now
  `claude-sonnet-5-5` (same price as `claude-sonnet-5`). Re-checked against
  Sonnet 5.5's rules: the request sends no sampling params, no `thinking`
  and no `tool_choice` (each would 400), and adaptive thinking still streams
  thinking blocks, so the status event is unchanged. `TestDraftRequestShape`
  now also bans `tool_choice`; `ai` and `web` tests green. Not observed live;
  that is N-004.
- **N-017** · raised `2026-1012-0131-bytdb-v0212-upgrade-next-list-seed` ·
  closed 2026-10-14, `2026-1014-0734-n002-n006-n017-n018-escape-backstop-sigterm-vacuum-sidecar` — `pbstudy compact` grew the file it purged. New
  `Store.CompactStudy` calls `Engine.Compact` (the native form of `VACUUM`,
  no transaction), and every non-dry-run `compact` ends with it and prints
  `Study database  before → after bytes`. Checked on a scratch data dir:
  deleting 4 of 6 notes left `study.bytdb` at 16,595 bytes after the purge,
  and 5,346 after compaction. `TestCompactStudyShrinksAfterDeletes` pins the
  shrink and that the surviving row reads back through the same handle.
  README and usage text updated.
- **N-018** · raised `2026-1012-0131-bytdb-v0212-upgrade-next-list-seed` ·
  closed 2026-10-14, `2026-1014-0734-n002-n006-n017-n018-escape-backstop-sigterm-vacuum-sidecar` — Backup left a `.lock` sidecar in the synced backups
  folder. `BackupStudy` now removes it once `Backup` returns
  (`removeLockSidecar`, symlink-resolved the way btypedb names it). The
  sidecar race btypedb guards against needs a second opener of the same path,
  and nothing opens a freshly minted snapshot. `store/store_test.go` checks
  that only the `*.bytdb` file remains, directly and through a symlinked
  folder; both tests fail with the removal disabled.
- **N-006** · raised `2026-0805-2048-phase5-sync` · closed 2026-10-14, `2026-1014-0734-n002-n006-n017-n018-escape-backstop-sigterm-vacuum-sidecar` —
  No signal handler without sync. `installShutdownFlush` now takes a nil
  syncer (skips the flush, still closes the store) and `cmdServe` installs it
  unconditionally. Checked on a scratch server with no `PBSTUDY_SYNC_DIR`:
  `SIGTERM` printed "Stopping (terminated) ..." and the process exited through
  `st.Close()`.
- **N-002** · raised `2026-0805-1917-phase2-notes-tags-xrefs` · closed
  2026-10-14, `2026-1014-0734-n002-n006-n017-n018-escape-backstop-sigterm-vacuum-sidecar` — No framework backstop for the escaping rule. Added
  `web/ui/escape_test.go`: it walks every view struct with reflection,
  poisons every exported string it can reach with `"'><script>`, renders,
  and fails on any raw payload. Fields that are constrained upstream (validated
  translations, `NormalizeScope`, handler-built URLs, sync-report counts) are
  listed in `trustedFields`, each with where the constraint lives. A new field
  is poisoned automatically. The first run found four sinks, all fixed:
  `ErrorPage` heading/detail (echo URL path segments; latent because browsers
  percent-encode `<` in paths, but reproduced with a raw request on a scratch
  server), Settings `kv()` (environment paths), `kindLabel`'s fallback (a
  sync-file kind), and record ids in hrefs/actions (sync import accepts any
  non-empty id). Ids now go through `pathSeg()` (`url.PathEscape`) in
  `NoteURL`/`TagURL`/`SermonURL`, section buttons and xref delete; that is a
  no-op on UUIDs.
- **N-013** · raised `2026-0805-1823-phase1-skeleton-reader` · closed
  2026-10-12 — Confirm the exact model ID for the AI draft rather than
  trusting `claude-sonnet-5` from `PLAN.md`. Checked against the current model
  list: `claude-sonnet-5` (`ai/draft.go:41`, `DefaultModel`) is a valid,
  still-served ID. It is no longer the newest Sonnet; see the session report.
- **N-014** · raised `2026-0805-1917-phase2-notes-tags-xrefs` · closed
  `2026-0807-0954-bytdb-v091-workaround-removal` — bytdb's `DISTINCT` +
  qualified `ORDER BY` constraint. Fixed upstream in bytdb v0.9.1; the
  workaround was removed, and `study/queries_test.go` guards it.
- **N-015** · raised `2026-0805-2048-phase5-sync` · closed
  `2026-0805-2238-compaction` — Nothing prunes tombstones. `pbstudy compact`
  removes expired tombstones from the database and the sync folder.
- **N-016** · raised `2026-0805-2012-phase4-sermon-builder-ai` · closed
  `2026-0805-2048-phase5-sync` — `status` can stick at `drafting` if the
  process dies mid-draft; sync must not treat it as meaningful. Import
  normalizes it (`study/sync.go:558`): `drafting` becomes `drafted` or
  `outline`. Locally the stuck value stays harmless. No UI shows sermon status
  and nothing gates on it; the draft lock is in memory (`s.drafts.begin`).
