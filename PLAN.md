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
3. **Search + topical study**: scripture ILIKE search, notes search, combined page, ref fast-path; download WEB+ASV, parallel-translation verse hub.
4. **Sermon builder + AI**: sermons CRUD, outline UI (form round-trips + minimal JS reorder), AssembleOutline, exports, then streaming AI draft.
5. **Sync**: syncer package, backup route, settings; test with two DataDirs on one machine simulating two hosts.

Then `git init` + initial commit (user said data should sync; repo hosting is his call later).

## Verification

- Every phase: `go build ./...` && `go vet ./...`, run server, check logs.
- P1: `SELECT COUNT(*) FROM verses` ≈ 31,102 after KJV download; `curl localhost:8000/read/kjv/43/3 | grep Nicodemus`; `/css/app.css` → 200+ETag, then 304.
- P2: curl-create a note anchored to John 3:16 → appears on verse hub + reader dot; xref both directions; browser hover-popup on a reference inside a note.
- P3: `/search?q=love&scope=scripture` returns hits <100ms; `/search?q=John+3:16` → verse hub.
- P4: export.md contains verse text verbatim; with key: watch SSE stream, kill mid-stream (no goroutine leak); without key: button absent.
- P5: two-instance round-trip — create on A → appears on B; edit on B wins on A; tombstone propagates; live `.bytdb` never appears in sync dir; a backup snapshot opens in a fresh DataDir.

## Risks / verify-at-implementation

- ~~bytdb `INT` col type & `CREATE TABLE IF NOT EXISTS` support~~ — **resolved.** `INT`/`SERIAL`/`TEXT`/`JSONB`/`TEXT[]`/`TIMESTAMP`/`BOOLEAN` all work. `CREATE TABLE IF NOT EXISTS` does **not** parse (only `CREATE SEQUENCE` has it). Bootstrap probes `information_schema.tables` (tables) and `pg_class` (indexes) and creates only the difference; `store/schema.go` keeps an "already exists" string match as a backstop only.
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
- **bytdb rejects a qualified ORDER BY under SELECT DISTINCT** — `ORDER BY n.updated_at` fails with "ORDER BY expressions must appear in select list" even when `n.updated_at` is selected; it matches on the *output* column name. The three DISTINCT queries (`NotesForVerse`, `NotesForChapter`, `NotesForTag`) order by the bare `updated_at`. DISTINCT itself is load-bearing: one note can carry several anchors covering the same verse. Found by the live smoke test, not by the compiler.
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
