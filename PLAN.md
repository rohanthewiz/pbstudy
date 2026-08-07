# pbstudy — Personal Bible Study App

## Context

Rohan wants a personal Christian Bible study app to (1) track notes and correlations between scriptures, (2) study topics generally, and (3) quickly assemble sermons/teachings from collected notes and references. Confirmed decisions: offline-first public-domain Bible text (KJV, WEB, ASV); Blue Letter Bible integration via their official ScriptTagger widget + deep links (no iframes needed — BLB sends no X-Frame-Options, but links/tagger are the cleaner path); sermon generation is BOTH a mechanical outline builder and optional "Draft with AI" via the Anthropic API; data store is **bytdb** (his own embedded SQL DB, `github.com/rohanthewiz/bytdb@v0.8.0`); sync is file-sync-friendly (iCloud/Syncthing/git on a sync dir); local web app on rweb + element, styled with **go-styl** (Stylus).

Greenfield: `/Users/ro/projs/go/pbstudy` is empty, not a git repo yet.

### Key constraints discovered in exploration
- **bytdb**: single append-only file, memory-resident, Postgres-flavored SQL (`$1` params, serial/text/timestamp/text[]/jsonb). **No file locking — never let a sync daemon touch the live file (torn-tail corruption)**; safe snapshot via `Engine.Backup(destPath)`. Declare PKs at CREATE TABLE time (no ALTER ADD PK). No full-text search — ILIKE scans are fine at this scale (~31ms/100k rows; 3 translations ≈ 93k verse rows). Driver: `sql.Open("bytdb", path)` with `_ "github.com/rohanthewiz/bytdb/stdlib"`; `stdlib.OpenEngine(e)` / `stdlib.Engine(ctx, db)` bridge to the native engine for `Backup()`.
- **getbible.net v2**: per-book JSON verified — `GET https://api.getbible.net/v2/{kjv|web|asv}/{bookNr}.json` → `{nr, name, chapters:[{chapter, verses:[{chapter, verse, text}]}]}` (arrays, not maps).
- **BLB**: ScriptTagger script `https://www.blueletterbible.org/assets/scripts/blbToolTip/BLB_ScriptTagger-min.js` with `BLB.Tagger.{Translation, HyperLinks, TargetNewWindow, DarkTheme, Style}` config; deep links `https://www.blueletterbible.org/kjv/jhn/3/16/` and Strong's `https://www.blueletterbible.org/lexicon/g26/kjv/tr/`.
- **go-styl**: rweb ships the adapter — `rweb/middleware/stylus.Handler(stylserve.Options{FS: sub})` mounted at `GET /css/*path`; compiles embedded `.styl` with ETag/304 caching.

## Architecture

**Two DB files, one process** (`~/.pbstudy/` data dir):
- `bible.bytdb` — scripture cache. Never synced; rebuildable via `pbstudy download`. Relaxed sync policy.
- `study.bytdb` — notes/tags/xrefs/sermons (the precious data). Default durability. Snapshots via `Backup()` into the sync dir.

**Sync model**: live DBs stay local; a user-chosen `sync/` dir (pointed at iCloud/Syncthing/git) holds one JSON file per syncable entity (UUID + `updatedAt` + tombstones) → last-writer-wins merge import on startup / on demand. Bible text is never synced.

## Directory layout

```
pbstudy/
├── main.go                  # subcommands: serve (default) | download <kjv|web|asv|all> | backup
├── cfg/cfg.go               # DataDir (~/.pbstudy), SyncDir, Port, AnthropicKey (env)
├── store/store.go,schema.go # open DBs, idempotent CREATE TABLE/INDEX bootstrap, BackupStudy()
├── bible/books.go           # canonical 66 books: num, name, OSIS, BLB slug, aliases, chapter count
│       ref.go               # ParseRef("John 3:16-18") → Ref
│       query.go             # Chapter/Verse/VerseRange/SearchText (ILIKE)
│       download.go          # getbible.net per-book downloader (66 reqs/translation, ~100ms delay)
├── study/notes.go tags.go xrefs.go sermons.go search.go   # CRUD + outline assembly + MD/HTML export
├── blb/blb.go               # VerseURL(), LexiconURL("G26"), ScriptTagger snippet
├── syncer/export.go import.go syncer.go   # JSON export, LWW import, debounced auto-export
├── ai/draft.go              # Anthropic /v1/messages streaming client (net/http only)
├── web/server.go handlers_*.go
│   └── ui/layout.go reader.go verse.go notes.go search.go sermon.go settings.go   # element builders
└── assets/styles/*.styl (embedded), js/app.js
```

Deps: rweb, element, go-styl, bytdb, serr, logger, google/uuid, yuin/goldmark (markdown).

## Schema (bytdb SQL)

**bible.bytdb**: `translations(abbrev PK, name, lang, downloaded_at)`; `books(book_num PK, name, osis, blb_abbrev, testament, chapter_count)`; `verses(id SERIAL PK, translation, book_num, chapter, verse, body)` + index on `(translation, book_num, chapter, verse)`.

**study.bytdb** (syncable rows: TEXT uuid PK, `updated_at`, `deleted_at` tombstone):
- `notes(id, title, body_md, created_at, updated_at, deleted_at)` — body may contain `[[G26]]` Strong's shortcodes → BLB lexicon links.
- `note_refs(id, note_id, book_num, chapter, verse_start, verse_end, updated_at)` — verse-range anchors; children of notes, replaced wholesale on sync import (no own tombstones). Indexes on `note_id` and `(book_num, chapter)`.
- `tags(id, name, descrip, updated_at, deleted_at)`; `note_tags(id, note_id, tag_id, updated_at)` + indexes.
- `cross_refs(id, from_book, from_chapter, from_verse, to_book, to_chapter, to_verse_start, to_verse_end, comment, created_at, updated_at, deleted_at)` + indexes both directions — the scripture-correlation feature.
- `sermons(id, title, outline JSONB, draft_md, status, created_at, updated_at, deleted_at)` — outline = ordered sections `[{kind: heading|passage|note|point, ...}]`.

## Routes (rweb)

`/` dashboard · `/read/:translation/:book/:chapter` reader (note/xref indicator dots, BLB verse links, prev/next) · `/verse/:book/:chapter/:verse` verse hub (all translations, attached notes, xrefs in/out, quick-add forms, BLB deep links) · `/notes` CRUD (`POST /notes`, `/notes/:id`, `/notes/:id/edit`, tombstone delete) · `/xrefs` create/delete · `/tags`, `/tags/:id` topical study pages · `/search?q=&scope=all|scripture|notes&translation=` (ILIKE; a query that parses as a reference like "John 3:16" jumps straight to the verse hub) · `/sermons` builder + `/sermons/:id/export.{md,html}` + `POST /sermons/:id/draft` → `GET /sermons/:id/draft/stream` (SSE via `s.SetupSSE`) · `/settings` (sync dir, download buttons, API-key presence) · `POST /sync/run`, `POST /backup` · `/css/*path` (stylus middleware) · `/js/*path`.

Layout shell includes ScriptTagger (config before script tag, `DarkTheme: true`, `defer`); reader's own scripture container gets a no-tag class so BLB doesn't double-link scripture we render — tagger stays active in note bodies, which is the point.

## Sermon generation

1. **Mechanical (offline)**: `study.AssembleOutline(sermonID)` walks outline sections, inlines verse text from bibleDB and note markdown → Markdown doc (`## headings`, `> John 3:16 (KJV) — text`, note bodies). Export as .md download or standalone HTML via goldmark.
2. **AI draft (optional)**: enabled only when `ANTHROPIC_API_KEY` env is set. Plain net/http POST to `https://api.anthropic.com/v1/messages`, `model: claude-sonnet-5`, `stream: true`, `max_tokens: 64000`, **no temperature/thinking fields** (Sonnet 5 rules). Prompt payload = the same assembled outline Markdown (one code path). Parse SSE `content_block_delta` → `text_delta` fragments, stream to browser through rweb SSE + `EventSource`, save accumulated draft to `sermons.draft_md`. Key never persisted or synced.

## Sync engine

- `sync/notes/<uuid>.json` (note bundles its refs + tag *names* — names are the cross-machine identity; missing tags auto-created on import), `sync/tags/`, `sync/xrefs/`, `sync/sermons/`, `sync/backups/study-<ts>.bytdb`.
- Merge: file `updatedAt` (RFC3339Nano UTC) newer than DB → upsert (incl. tombstones); DB newer → re-export; atomic writes via `.tmp` + rename.
- Triggers: `ImportAll()` at startup; debounced (~2s) auto-export after study writes; export-all on graceful shutdown; `POST /sync/run` with a rendered report.

## Implementation phases (each ends browsable)

1. ~~**Skeleton + reader**~~ — **DONE** (see "Phase 1 outcome" below): go mod init, cfg, store/schema (bible), books table, ref parser, downloader, `pbstudy download kjv`, server + layout + stylus + reader + verse hub with BLB links.
2. ~~**Notes/tags/xrefs**~~ — **DONE** (see "Phase 2 outcome" below): study package, note/tag/xref CRUD, note editor + goldmark + `[[G26]]` links, verse-hub quick-add forms, reader indicator dots, tags browser.
3. ~~**Search + topical study**~~ — **DONE** (see "Phase 3 outcome" below): scripture ILIKE search, notes search, combined page, ref fast-path; download WEB+ASV, parallel-translation verse hub.
4. ~~**Sermon builder + AI**~~ — **DONE** (see "Phase 4 outcome" below): sermons CRUD, outline UI (form round-trips), AssembleOutline, exports, streaming AI draft.
5. ~~**Sync**~~ — **DONE** (see "Phase 5 outcome" below): syncer package, backup
   route, settings; tested with two DataDirs on one machine simulating two hosts.

Then `git init` + initial commit (user said data should sync; repo hosting is his call later).

## Verification

- Every phase: `go build ./...` && `go vet ./...`, run server, check logs.
- P1: `SELECT COUNT(*) FROM verses` ≈ 31,102 after KJV download; `curl localhost:8000/read/kjv/43/3 | grep Nicodemus`; `/css/app.css` → 200+ETag, then 304.
- P2: curl-create a note anchored to John 3:16 → appears on verse hub + reader dot; xref both directions; browser hover-popup on a reference inside a note.
- P3: `/search?q=love&scope=scripture` returns hits <100ms; `/search?q=John+3:16` → verse hub.
- P4: export.md contains verse text verbatim; with key: watch SSE stream, kill mid-stream (no goroutine leak); without key: button absent. **All three verified** — the mid-stream kill and the framing are covered offline by `web/draft_stream_test.go` against a stand-in API, since no API key was available in the implementing session.
- P5: two-instance round-trip — create on A → appears on B; edit on B wins on A; tombstone propagates; live `.bytdb` never appears in sync dir; a backup snapshot opens in a fresh DataDir.

## Risks / verify-at-implementation

- ~~bytdb `INT` col type & `CREATE TABLE IF NOT EXISTS` support~~ — **resolved.** `INT`/`SERIAL`/`TEXT`/`JSONB`/`TEXT[]`/`TIMESTAMP`/`BOOLEAN` all work. `CREATE TABLE IF NOT EXISTS` did **not** parse at v0.8.0, so the bootstrap probed `information_schema.tables` and `pg_class` and created only the difference. bytdb v0.9.0 added the guard clause for tables and **v0.9.1 extended it to `CREATE [UNIQUE] INDEX`**, so as of v0.9.1 the probe is gone: every statement in `store/schema.go` carries `IF NOT EXISTS` and `applySchema` just runs them in order. The guard is a name check only — it does not reconcile a changed column list, so an actual schema change still needs a real migration. `ALTER TABLE ADD PRIMARY KEY` remains unsupported by design, which is why every primary key is declared inline and the full study schema ships from Phase 1.
- ~~A few BLB book slugs (`rth/phl/jde/jam`)~~ — **resolved.** All 66 slugs verified live (HTTP 200). Guarded by `blb/slugs_live_test.go`, opt-in via `PBSTUDY_LIVE_TESTS=1`.
- ~~`BLB.Tagger` config timing~~ — **resolved.** Config is emitted before the script tag; verified in the rendered head.
- ~~rweb path-param accessor~~ — **resolved.** `Param` and `PathParam` are identical aliases over the same slice; either works for both named and wildcard segments.
- LWW granularity: simultaneous same-note edits on two machines lose one side (acceptable, single user; backups are the net).

## Phase 1 outcome (implemented)

**Verified**: KJV downloads to exactly **31,102 verses**; re-download is idempotent (still 31,102, no duplicates); all 66 chapter counts match the compiled canon table (total 1,189); `/read/kjv/43/3` renders 36 verses including Nicodemus and v16; `/css/app.css` returns 200 + ETag then 304 on `If-None-Match`; every route smoke-tested; `go build`/`go vet`/`go test`/`gofmt` all clean.

### Deviations from the plan above, and why

- **`verses` uses a composite natural PK** `(translation, book_num, chapter, verse)` instead of `id SERIAL PK` + a secondary index on the same columns. bytdb stores rows in PK order in one key space; `EXPLAIN` confirms a chapter read becomes `Index Scan using verses_pkey` with a prefix `Index Cond`, already sorted by verse. The surrogate key would have cost a parallel index maintained on every one of the 31k inserts for a strictly worse read path. It also makes re-download idempotent via a single bounded `DELETE ... WHERE translation = $1 AND book_num = $2`. Measured: 33k-row bulk load 75ms, chapter read 41µs, ILIKE scan 241µs.
- **The full study schema ships in Phase 1**, not Phase 2. It is declarative, the store opens both databases anyway, and PKs cannot be added later in bytdb — so getting the tables right up front is safer. Phase 2 is now purely CRUD + UI.
- **Scripture search landed early.** `bible.SearchText` (ILIKE) and the reference fast-path are wired to `/search` because the header search box is on every page and would otherwise 404. Notes search and the combined-scope page remain Phase 3.
- **Added a data-directory lock** (`store/lock_unix.go`, flock-based, released by the kernel on exit). Not in the plan, but bytdb does no file locking and the plan's own torn-tail warning applies just as much to two pbstudy processes as to a sync daemon — a `download` or `backup` run while `serve` was live would have interleaved appends into `study.bytdb`. Verified: the second process is refused and told which PID holds the directory. Non-unix builds get a documented no-op.
- **`serr.Wrap(nil, …)` is not a no-op** — it logs "Not wrapping a nil error". Commits and similar are checked explicitly rather than wrapped unconditionally.
- Nav links whose features land later (`/notes`, `/tags`, `/sermons`) are registered as honest placeholders instead of 404s.

### Phase 1 file map

```
main.go                     serve | download | backup | help
cfg/cfg.go                  env-driven config, DataDir creation
store/store.go              open both DBs, BackupStudy(), Close()
     schema.go              probe-and-create bootstrap; bible + study schemas
     lock_unix.go           flock advisory lock (lock_other.go = no-op)
bible/books.go              66 books: num, name, OSIS, BLB slug, aliases, chapters
     ref.go                 ParseRef / LooksLikeRef / Ref.String
     ref_test.go            parser, rejection, round-trip, canon-total tests
     query.go               Chapter/VerseRange/One/AllTranslationsOf/SearchText
     download.go            getbible.net v2 downloader, SeedBooks, count verify
blb/blb.go                  VerseURL/InterlinearURL/LexiconURL/TaggerConfigJS
   slugs_live_test.go       opt-in live check of all 66 slugs
web/server.go               routes, render helpers, embedded-JS handler
   handlers_read.go         dashboard, reader, verse hub, settings
   handlers_search.go       ref fast-path + scripture text search
   ui/layout.go             page shell, nav, ScriptTagger placement
   ui/reader.go             chapter view, picker, prev/next
   ui/verse.go              verse hub
   ui/search.go             results with XSS-safe <mark> highlighting
   ui/pages.go              dashboard, settings, error page
assets/assets.go            embed.FS for styles + js
   styles/app.styl          dark reading-first stylesheet
   js/app.js                arrow-key chapter nav, "/" search focus, chapter picker
```

## Phase 2 outcome (implemented)

**Verified** against a live server on a scratch data dir (31,102 KJV verses): a note created by `POST /notes` with `refs=John 3:16-18; Rom 5:8` derives its title from the body's first line, creates both tags, renders `[[G26]]` as a BLB lexicon link, inlines the KJV text of both anchors, and appears on the verse hub for verses 16, 17 **and** 18 (range containment) plus as reader dots on all three. A cross-reference created from John 3:16 → Romans 5:8 shows as "From here" on John 3:16 and "To here" on Romans 5:8 without being created there. Editing a note to drop an anchor removes the corresponding hub entry and dot; deleting it 404s the permalink, clears its dots, and empties its tag page while leaving the cross-reference intact. `/css/app.css` still compiles. `go build`/`go vet`/`go test`/`gofmt` all clean.

### Deviations from the plan above, and why

- **`element` does not escape anything** — not text passed to `b.T()`, not attribute values. Phase 1's `web/ui/search.go` carried a comment asserting the opposite. Harmless while every rendered string came from the compiled canon or getbible.net; an injection hole the moment notes became user input. Fixed by `web/ui/escape.go` (`esc()`, applied to every string that originated outside the binary) with the rule written down there. Two live holes were found and closed by the fix: a note titled `</title><script>…</script>` broke out of the document `<title>`, and the search page echoed its query unescaped. Scripture bodies are now escaped too — they arrive over the network and were never ours either.
- **Translations are validated at the handler boundary**, not just defaulted. `/read/:translation/…` 404s an unknown translation and `?t=` falls back to the default, because that value flows into hrefs, a hidden form field and the ScriptTagger config. Escaping it correctly in four places is a worse bet than checking it once.
- ~~**bytdb rejects a qualified ORDER BY under SELECT DISTINCT**~~ — **fixed upstream in bytdb v0.9.0**, which resolves a qualified ORDER BY against the select list as Postgres does. At v0.8.0 `ORDER BY n.updated_at` failed with "ORDER BY expressions must appear in select list" even when `n.updated_at` was selected, and the three DISTINCT queries (`NotesForVerse`, `NotesForChapter`, `NotesForTag`) ordered by the bare `updated_at` to avoid it; they now qualify the key, which is what a two-table join wanted to say all along. go.mod's bytdb floor is what keeps that legal. DISTINCT itself remains load-bearing: one note can carry several anchors covering the same verse. Originally found by the live smoke test, not by the compiler — the failure emptied the verse hub silently.
- **`DeleteNote` keeps the note's refs and tag links**, tombstoning only the note row. Every read path joins back to `notes` and filters `deleted_at IS NULL`, so the children are inert rather than stale, and an undelete stays one UPDATE away. `DeleteTag` is the asymmetric case — it *does* remove `note_tags` rows, because a retired tag must stop appearing on notes immediately.
- **Tag identity is case-folded in Go, not in SQL.** The unique index on `tags.name` is case-sensitive, so "Grace" and "grace" could both be inserted. `writeTagLinks` reads the (tens-of-rows) tag table once per save and matches on a lowercased key, which also avoids escaping LIKE metacharacters out of user-typed tag names.
- **Note anchors are typed as text**, not picked from selects: the `References` field takes `John 3:16-18; Rom 5:8` through the same parser as the search box (`bible.ParseRefList`). Partial success is deliberate — three good anchors and one typo saves nothing and reports the typo, rather than silently storing two of three.
- **Destructive actions hide behind `<details>`** rather than a JavaScript `confirm()`: no script, keyboard accessible for free, and two deliberate actions instead of a modal dismissed by reflex.
- **Every form carries a `return` field**, so the verse hub's quick-adds post to the same `/notes` and `/xrefs` endpoints the full editor uses and come back where they started. `web.safeReturn` rejects protocol-relative, absolute, and backslash/CRLF targets — a local app still serves whatever request arrives.
- **Added `bible.MaxChapterVerses`** (176, Psalm 119) purely as a clamp when expanding a stored verse range into indicator dots, so a hand-edited row cannot drive an unbounded loop.
- **Dashboard gained note and cross-reference counts**, shown only once non-zero.

### Not verified

- **ScriptTagger hover popups in a real browser.** The Chrome instance available here cannot reach this machine's localhost (`ERR_CONNECTION_REFUSED` on `localhost` and `127.0.0.1` alike, while curl gets 200 on both), so the popup behaviour was not exercised. What *is* confirmed from the served HTML: the `BLB.Tagger` config precedes the script tag, the reader's scripture container and the note page's Scripture card both carry `blb-no-tag`, and the note body does not — which is the split the integration depends on. Worth a manual look in a browser.

### Phase 2 file map

```
study/study.go              package doc, UUID ids, UTC clock, IN-list helper
     notes.go               Note/NoteRef/NoteDraft, CRUD, NotesForVerse/Chapter
     tags.go                Tag CRUD, on-demand creation, case-folded identity
     xrefs.go               CrossRef CRUD, both-direction queries, Other()
     marks.go               ChapterMarks — per-verse indicator counts, one pass
     markdown.go            goldmark (safe mode) + [[G26]] expansion, Excerpt
     study_test.go          markdown, excerpt, tag names, range expansion
bible/ref.go                + ParseRefList / FormatRefList
     books.go               + MaxChapterVerses
web/handlers_notes.go       notes CRUD, anchor resolution, editor round-trip
   handlers_tags.go         tag index, topical page, describe, delete
   handlers_xrefs.go        xref create/delete, re-render-on-bad-reference
   support.go               studyUnavailable, safeReturn
   support_test.go          redirect guard, translation recovery
   ui/notes.go              list, detail, editor, shared note fragments
   ui/tags.go               tag index and topical study page
   ui/escape.go             the escaping rule for the whole ui package
   ui/verse.go              rewritten: notes, xrefs both ways, quick-add forms
   ui/reader.go             + indicator dots and chapter-level notes
assets/styles/app.styl      chips, note rows, editor fields, disclosures, xrefs
```

## Phase 3 outcome (implemented)

**Verified** against a live server holding all three translations (KJV 31,102 ·
WEB 31,095 · ASV 31,086 verses) and three seeded notes, two tags and one
cross-reference: `/search?q=grace` returns scripture, notes and topic groups in
one page; `?scope=scripture` and `?scope=notes` narrow it; a query that parses
as a reference still jumps to the verse hub (`John 3:16` → 302) or the reader
(`John 3` → 302) — except under `scope=notes`, where it lists the notes
anchored to that passage instead; a cross-reference found by its comment text
renders both ends as links; the verse hub shows John 3:16 in all three
translations; the reader's version switcher offers all three. Search timings on
this data: scripture scope 40–85 ms, study scope 0.6 ms. Hostile queries are
escaped in the `<title>`, in the form `value=`, and inside the `<mark>`
highlighting. `go build` / `go vet` / `go test` / `gofmt` all clean.

### Deviations from the plan above, and why

- **A reference query under `scope=notes` does not jump.** The plan had one
  fast-path rule; there are now two. Someone who has explicitly narrowed to
  their own writing and then types "John 3:16" is asking *what have I written
  about this verse*, not *take me there* — so the reference is resolved through
  `NotesForVerse` / `NotesForChapter` and the jump is offered as a link on the
  page. Under every other scope the redirect is unchanged.
- **Search covers tags and cross-reference comments, not just notes.** The
  comment on a correlation is often the only place a thought was written down
  ("this is the protoevangelium", typed once while linking Genesis 3:15 to
  Romans 16:20). Text that no query can reach is text that is lost, so
  `scope=notes` means "everything I wrote" — note bodies and titles, tag names
  and descriptions, and xref comments — rendered as three labelled groups.
- **Every result group states its cap.** `scriptureSearchLimit` is 200 and
  `study.DefaultSearchLimit` is 100; when a group fills its limit the page says
  so. A list that silently stops reads as "there is nothing more", which is a
  worse failure than showing fewer results.
- **A failing study query fails the search page** rather than degrading, which
  is the opposite of the reader's treatment of the same error. The difference is
  what the page is for: a reader that loses its indicator dots still shows
  scripture, but a search that quietly drops the notes group answers "where does
  this come up?" with a confident, wrong "nowhere".
- **Snippets are cut from the *stripped* text, not the raw Markdown.**
  `study.Snippet` strips markup and collapses whitespace first, then locates the
  match in the stripped copy — searching the raw source and slicing the stripped
  copy at that offset would land somewhere else entirely. Two cases fall back to
  a leading excerpt instead of guessing: a match that lived only inside markup
  the stripper removed (a link URL), and any input where lowercasing changes
  byte length (non-ASCII), where a wrong offset would cut a rune in half. The
  same guard already protects `ui.highlight`.
- **Scope semantics live in one place.** `ui.NormalizeScope` maps the URL value
  and `ui.Search.Wants` answers "does this group belong on the page" — the
  handler asks the same method before doing the work, so the page cannot query
  one thing and display another. An unknown scope widens to "all" rather than
  erroring: a scope is a filter, and the honest response to a filter nobody
  understands is to filter nothing.
- **Search joined the nav.** It has a header box on every page, but that box
  cannot express a scope or a version, and search is now a destination.
- **The tag page grew a passage list** (`study.RefsAcross`): the distinct
  anchors across a tag's notes, deduplicated and sorted canonically via a new
  `bible.Ref.Compare`. This is the topical-study payoff — the scripture a topic
  actually touches, gathered from notes that were never organised around it —
  and it is derived from the notes already loaded, so it cannot disagree with
  the list below it. Identical ranges collapse; overlapping ones do not, because
  "John 3:16" and "John 3:16-18" are two different citations of the same text.

### Bug found and fixed

- **`ParseRef` could not read `Lev 16:14` or `Rev 22:1`.** The parser walks
  backwards over the numeric tail and accepted `v` as a chapter/verse separator
  (the "John 3v16" form) *anywhere* — so "Lev 16:14" split as book `Le` plus
  locator `v 16:14` and failed outright. `v` is now a separator only when a
  digit sits immediately before it, which is the only position the "3v16" form
  ever puts it in. Found while seeding a note anchored to Leviticus 16:14, not
  by any existing test; `bible/ref_test.go` now covers Lev/Rev/REV.

### Measured, and what it means for Phase 4+

`EXPLAIN` on the scripture search confirms bytdb uses the primary key's leading
column: `Index Scan using verses_pkey` with `Index Cond: (translation = 'kjv')`
and the ILIKE as a `Filter`. So the scan is bounded to one translation's ~31k
rows and does *not* grow as more translations are downloaded. It costs 40–85 ms
— a full scan when few rows match, and *less* when many do, because the `LIMIT`
sits above the scan and stops it early. Still inside the plan's 100 ms target,
so no index was built; if it ever needs to be faster, the answer is a separate
term index, not a schema change to `verses`.

### Phase 3 file map

```
study/search.go             SearchNotes/SearchTags/SearchXrefs, NoteHits, Snippet
     search_test.go         snippet windowing, UTF-8 guard, LIKE escaping, RefsAcross
     notes.go               + RefsAcross (distinct anchors, canonical order)
bible/ref.go                + Ref.Compare; 'v' separator fix in ParseRef
web/handlers_search.go      rewritten: scopes, two fast-path rules, one render exit
   ui/search.go             rewritten: form, scope tabs, four result groups
   ui/search_test.go        scope normalization, URL escaping, highlight escaping
   ui/tags.go               + passage list on the topical page
   ui/layout.go             + Search in the nav
assets/styles/app.styl      search form, scope tabs, result groups, <mark>
```

## Phase 4 outcome (implemented)

**Verified** against a live server holding all three translations and the
seeded study data: a sermon is created from the index, its outline takes
headings, points, passages and notes, sections reorder with the arrows and
survive a stale page, `export.md` inlines John 3:16-18 and Romans 5:8 verbatim
from the KJV cache with a note's body and anchors underneath its title,
`export.html` is a self-contained document with no external references, the
dashboard counts sermons, and a hostile sermon title is escaped in the builder,
in the index, in the HTML export and in the download filename. With no API key
the drafting controls are absent and the stream endpoint answers 200 with a
`fail` event explaining why; with a key set, the request reaches
api.anthropic.com, is accepted as well-formed, and its rejection message is
surfaced to the browser verbatim. `go build` / `go vet` / `go test -race` /
`gofmt` all clean.

### Deviations from the plan above, and why

- **Drafting is POST-then-stream, and the generation starts in the stream.**
  The plan had `POST /sermons/:id/draft` start the work and
  `GET /sermons/:id/draft/stream` read it, which races: the generator produces
  events before the browser has connected, so they are either buffered
  indefinitely or lost. Instead the POST validates and redirects to
  `?draft=1`, and generation begins when the EventSource connects — one
  request, one goroutine, one channel, and no event that predates its reader.
- **Every stream rejection is a 200 carrying a `fail` event**, not an HTTP
  status. An EventSource cannot read a status line or a response body, so a 404
  from this endpoint reaches the browser as a bare connection error and shows
  the user nothing at all.
- **The SSE event for a failure is named `fail`, not `error`.** EventSource
  dispatches its own connection failures as an `error` event, so a server-sent
  one would arrive at the same handler and be indistinguishable from the socket
  dropping.
- **Draft fragments travel as JSON, not as raw text.** rweb writes an event as
  `event: <name>\ndata: <payload>\n\n`, and a sermon draft is mostly
  newlines — a raw paragraph break would terminate the frame mid-word and hand
  the browser a corrupt stream. `web/drafts_test.go` pins this.
- **Outline sections carry their own ids.** The plan described the outline as
  an ordered JSONB array, which invites addressing sections by index; a Move-up
  button rendered against position 3 would then move whatever *now* sits at
  position 3 if the outline changed in another tab. Sections are addressed by a
  UUID minted on append, so a stale click either finds its section wherever it
  drifted to or reports that it is gone.
- **No JavaScript reorder.** The plan allowed "minimal JS reorder"; the up/down
  buttons are plain single-button POST forms, which work with JavaScript
  disabled, are keyboard-accessible for free, and needed no new endpoint. The
  only JavaScript Phase 4 adds is the draft stream reader, which genuinely
  cannot be done in HTML.
- **A whole-chapter passage is capped at 60 inlined verses**, and the cap is
  stated in the document. A chapter section is legitimate — a preacher working
  through Romans 8 wants the chapter — but Psalm 119 is 176 verses, and pasting
  it into a two-page outline (and into an AI prompt) helps nobody.
- **Missing material is marked in the document, never fatal.** A deleted note
  or an uncached passage becomes an italic marker rather than failing the
  assembly, because the same document goes to a model that must not invent the
  missing text and to a preacher who needs to know what to go and fix.
- **`ai` errors carry two messages.** serr's user-message channel holds one
  sentence for the person waiting on a draft while `Error()` stays the
  developer's string, so the log gets the status, type and endpoint and the
  browser gets "credit balance is too low". Those two are not the same audience
  and a single message serves neither.
- **`bible.Ref` gained JSON tags.** A Ref is now persisted inside a sermon
  outline and will travel in the Phase 5 sync files, so its wire names are
  pinned in lower camel case rather than left to Go's field capitalisation.
  `Section.Ref` is tagged `omitzero`, not `omitempty` — the latter has no
  effect on a struct value and would write a zero Ref into every heading.
- **The `comingSoon` placeholder handler is gone.** Every nav destination now
  resolves to a real page.
- **The dashboard counts sermons**, on the same terms as notes and
  cross-references: only once at least one exists.

### The disconnect problem, and how it is solved

rweb's SSE loop detects a client disconnect and returns, but it exposes no
channel, context or callback to tell the producing goroutine — so a generator
that keeps sending would block forever on a channel nobody drains. The signal
used instead is the send itself: events go through a buffered channel
(`draftBuffer` = 256) with a timed send, and a send that cannot complete within
`Server.draftStall` (45 s) means the reader has gone. The generator then stops,
saves whatever was produced — those tokens were paid for either way — and
releases its claim on the sermon. The timeout fires at most once per draft, so
a departed reader costs one wait rather than one per event.

`Server.draftStall` is a per-server field rather than a package variable
specifically so a test can shorten its own server's timeout without racing
another server's in-flight goroutines; an earlier package-level `var` failed
`go test -race` for exactly that reason.

### Verified end to end without an API key

`web/draft_stream_test.go` runs the real server on a real socket with a local
stand-in for the Messages API (`Server.aiEndpoint`, the one test seam), and
covers what unit tests cannot: that rweb writes the frames this app expects,
that a newline-heavy draft survives the wire intact, that a second EventSource
is refused rather than starting a parallel generation, and that a reader
walking away mid-draft leaves the partial saved and the sermon claimable again.

### Phase 4 file map

```
study/sermons.go            Sermon/Section model, CRUD, outline mutation under tx
     outline.go             AssembleOutline, resolveOutline, pure formatOutline
     export.go              ExportHTML (self-contained), FileSlug
     outline_test.go        document format, decode tolerance, slug allow-list
     notes.go               + NotesByIDs
ai/draft.go                 Anthropic Messages streaming client (net/http only)
  draft_test.go             SSE parsing, request shape, every error path
web/handlers_sermons.go     CRUD, sections, exports, draft + stream
   drafts.go                draftJobs registry, runDraft, SSE framing
   drafts_test.go           one-line framing, stall detector, claim-once
   draft_stream_test.go     end-to-end over a socket against a stand-in API
   server.go                + sermon routes, drafts registry, draftStall
   ui/sermon.go             SermonsList, SermonBuilder, OutlineRow
   ui/sermon_test.go        escaping, AI-off rendering, stream panel contract
bible/ref.go                + JSON tags on Ref (sync-facing wire names)
assets/js/app.js            + EventSource draft reader
assets/styles/app.styl      + outline rows, add forms, draft panel
```

## Phase 5 outcome (implemented)

**Verified** with two data directories on one machine sharing one sync folder,
each running a real server: a note created on A (two anchors, two tags) reaches
B's notes list, verse hub and tag pages; an edit on B wins back on A *and*
removes the anchor B dropped; a delete on A propagates as a tombstone and does
not resurrect on any later pass; a settled pair reports "Everything was already
in step" and writes nothing. Automatic export fires ~2 s after a write with no
request made; a rename made under a second before `SIGTERM` is still on disk
afterwards. A snapshot taken from the settings page opens as `study.bytdb` in a
fresh data directory with its sermons, tags and cross-references intact and the
deleted note still deleted. No `.bytdb` ever appears in the sync folder outside
`backups/`, and no `.tmp` survives a pass. `go build` / `go vet` /
`go test -race` / `gofmt` all clean.

### Deviations from the plan above, and why

- **The merge is two passes, not one.** Import runs over the whole folder
  first, then export re-reads the database. A single interleaved pass would
  decide a row's fate against a snapshot taken before its own import changed
  it, and immediately write back what it had just taken in.
- **Tag identity forced a second clock comparison.** The syncer decides what to
  import by comparing a file against the local row *with the same id* — but two
  machines that both created "Grace" offline have two ids for it, and the unique
  index on `tags.name` makes inserting the second one impossible. `ApplyTag`
  therefore matches on the folded name and compares clocks itself, reporting
  `TagUnchanged` when the local tag is already at least as new. Without that
  second comparison the merge rewrites a timestamp on every run, the next run
  exports it, and two machines trade the same tag forever. The cost of keeping
  the local id is one redundant file per machine in `tags/`, which settles into
  a no-op. The live check showed exactly that: 2 tags, 4 files, "nothing to do".
- **The auto-export hook is one middleware, not a call in every handler.**
  Every mutation in this app is a POST that ends in a redirect or a re-render,
  so the rule lives where that rule lives. Failed requests are excluded, and
  `Touch` is non-blocking, so nothing is on the response's critical path.
- **`cfg.Load` refuses overlapping directories.** The plan warned never to let a
  sync daemon touch the live file; a warning is not a mechanism. Startup now
  fails if the sync dir is the data dir, or either contains the other.
- **Sermon `status` is normalized on import.** "drafting" describes a generator
  running in a process — possibly one that crashed on the other machine — so an
  incoming `drafting` becomes `drafted` if there is draft text and `outline` if
  there is not. This is the Phase 4 carried-forward item, closed.
- **A `pbstudy sync` subcommand was added.** The same engine the settings page
  drives, without starting the server, so a cron job or a git hook can run it.
  It cannot run while `serve` holds the data-directory lock, which is correct.
- **No download buttons on the settings page.** The route list sketched them;
  downloading 66 books over HTTP from a web handler needs its own progress
  stream and concurrency guard, and `pbstudy download` already does the job
  honestly. Settings gained the sync and backup buttons instead.
- **Records are their own types, not the domain types.** `Note`/`Tag`/`CrossRef`
  /`Sermon` are shaped for the pages that display them — tombstones filtered,
  tag links resolved, unexported direction fields. A record must carry
  tombstones, stay stable across versions, and be readable in a text editor. One
  type serving both would make every display change a change to the format of
  files already sitting in someone's iCloud folder.

### The clock, and the precision it is compared at

bytdb stores `TIMESTAMP` at **microsecond** precision — a value written with
nanoseconds reads back truncated (measured, not assumed). Two consequences,
both load-bearing:

- Every record is built from a database read, never from an in-memory value, so
  `RFC3339Nano` round-trips it exactly.
- `syncer.newer` truncates both sides to microseconds before comparing. Raw
  comparison would make a freshly imported row look permanently older than the
  file it came from, and every run would re-import it forever.

Ties lose. An equal clock is not evidence of a change.

### What the report is for

`syncer.Report` is a first-class result rather than a log line, because sync is
the one feature whose success is invisible — nothing on screen changes when it
works. A silent pass that failed to import the note written on the other machine
is indistinguishable from one that had nothing to do. So every pass counts what
moved in each direction, names every file it could not use (unreadable JSON,
wrong kind for the folder, a format version from a newer pbstudy), and says out
loud when a tag was merged by name. Problems are collected rather than returned,
so one broken file never stops the pass — and never vanishes either. The
`/sync/run` and `/backup` handlers hold the last outcome on the server and
redirect to `/settings`, so a refresh re-reads the report instead of running a
second sync.

### Phase 5 file map

```
syncer/syncer.go            Syncer, Run/ImportAll/ExportAll/Touch/Flush, merge rule
      files.go              atomic writes, folder scan, id-as-filename guard
      report.go             Report/EntityReport — what a pass did, per entity
      syncer_test.go        two simulated hosts: round trip, tag convergence,
                            tombstones, direction-only passes, debounce
      files_test.go         path-traversal refusal, ignored files, wire shape
study/sync.go               record types, whole-table export, idempotent apply
     sync_test.go           export→apply→export round trip, tombstone NULLs,
                            the three ApplyTag outcomes, status normalization
cfg/cfg.go                  + SyncBackupsDir, checkDirsDisjoint
   cfg_test.go              the overlap guard
web/handlers_sync.go        /sync/run, /backup, settings status, backup listing
   server.go                + syncer field, syncOnWrite middleware, sync routes
   ui/pages.go              + Settings.Sync card, report, snapshot list
main.go                     + sync subcommand, startup import, shutdown flush
assets/styles/app.styl      + sync report, problem lines, snapshot list
```

## Compaction (implemented, post-Phase 5)

Closes the "nothing prunes tombstones" item carried forward from Phase 5. A
tombstone is evidence a delete happened; once every machine has seen it, the row
and its file are carrying nothing but weight.

`pbstudy compact [days] [--dry-run]` — default 90 days, requires a sync
directory, refuses to run while `serve` holds the data-directory lock.

**The pass, in order.** Reconcile (import only, under the same hold of `runMu`)
→ per entity: list rows tombstoned strictly before the cutoff, hard-delete them
with their children, delete their files → then delete any *remaining* file whose
own record is an expired tombstone with no local row.

**Decisions worth keeping:**

- **Import first.** Compaction deletes a file, which is the only record of an
  event, so anything the folder is still holding must be applied first — an
  undelete from the other machine, or a delete this machine has not seen. Import
  is not destructive; it is what `serve` already does at startup. Both halves run
  under one hold of `runMu`, or a debounced export would rewrite files the pass
  had already decided to delete.
- **The retention window is the whole safety.** Nothing in the process can see
  how long a second machine has been away. 90 days is sized against a laptop that
  spent a season in a drawer, and the cost of waiting is a few hundred bytes per
  dead row.
- **`purgeRows` narrows the id list against `deleted_at` before deleting
  anything.** The list is assembled from a sync folder as well as from the local
  database, and a file claiming a row is dead is not the same as the row being
  dead here. Narrowing before the child deletes is what keeps a live note's
  anchors safe from a confused caller.
- **The row goes before its file.** A crash between the two leaves a file with no
  row, which the next pass re-imports as a tombstone and removes again. The other
  order leaves a row with no file, which exports itself straight back.
- **The orphan branch is what finally clears merged-tag files.** `ApplyNote`
  creates tags by name before their own files are read, so every machine mints
  its own id for a shared tag and the folder holds one redundant file per machine
  per name. Those ids have no local row and never will, so only compaction can
  remove them — and it does, once the record they carry is an expired tombstone.
- **A command, not a button.** Every other maintenance action in the app is
  additive or reversible. The settings page gained a sentence pointing at the
  command instead.
- **No database-only mode.** bytdb's file is append-only, so `DELETE` reclaims
  nothing on disk; compacting without a sync folder would spend a destructive
  pass to save nothing. `pbstudy backup` is what writes a fresh, smaller file.

**Still true afterwards:** compaction is per machine. One that has not compacted
re-exports its tombstone files, which the others import and remove again on the
next pass — bounded churn that converges the moment both compact, and what comes
back is a tombstone, invisible to every read in `study/`.

```
study/compact.go            Expired*/Purge* per entity, purgeRows narrowing
     compact_test.go        children removed, live rows refused, strict cutoff
     sync.go                + Syncable.SyncDeleted
syncer/compact.go           Compact, CompactReport, compactEntity, DefaultRetention
      compact_test.go       expired vs recent, dry run, orphaned tag files,
                            two-host convergence, no .bytdb / .tmp left behind
      syncer.go             run split into run/runLocked so Compact can reuse it
      files.go              + removeRecord; readRecords takes a problemSink
      report.go             + problemSink, kindLabel shared by both reports
main.go                     + compact subcommand
web/ui/pages.go             + the sentence on the sync card
```
