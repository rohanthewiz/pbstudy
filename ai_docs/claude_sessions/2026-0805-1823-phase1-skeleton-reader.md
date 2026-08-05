# pbstudy — Phase 1: Skeleton + Reader

- **Session ID**: `c28c0247-3cc5-4d69-b601-76830fb86488`
- **Date**: 2026-08-05
- **Scope**: Implement Phase 1 of `PLAN.md` (greenfield → browsable reader)
- **Outcome**: Complete and verified. `go build` / `go vet` / `go test` / `gofmt` all clean.

---

## Starting point

`~/projs/go/pbstudy` contained only `PLAN.md`. Not a Go module, not a git repo.
The plan defined five phases; this session delivered Phase 1 and closed four of
the five "verify-at-implementation" risks.

---

## Probes run before writing code

Rather than trusting the plan's assumptions, each was probed against the real
libraries first. This changed two design decisions.

### bytdb v0.8.0 capability probe

Ran a throwaway program against a scratch `.bytdb` file:

| Feature | Result |
| --- | --- |
| `INT`, `SERIAL`, `TEXT`, `JSONB`, `TEXT[]`, `TIMESTAMP`, `BOOLEAN` | all work |
| `CREATE TABLE IF NOT EXISTS` | **syntax error** — only `CREATE SEQUENCE` has it |
| Duplicate `CREATE TABLE` / `CREATE INDEX` | errors: `table already exists` / `index already exists` |
| `SELECT table_name FROM information_schema.tables` | works — lists tables |
| `SELECT relname FROM pg_class` | works — lists tables, `*_pkey`, secondary indexes |
| `SELECT tablename FROM pg_tables` | **no such table** |
| `$1` params, `ILIKE`, nullable scans, jsonb round-trip | all work |
| `stdlib.Engine(ctx, db)` → `Engine.Backup(path)` | works |

**Consequence**: schema bootstrap probes the catalog and creates only the
difference, instead of a swallow-the-error retry. Genuine DDL errors (a typo, a
bad type) stay loud. The `already exists` string match survives only as a
backstop.

### Composite-PK probe (changed the plan's schema)

Second probe loaded 33,000 rows and ran `EXPLAIN`:

```
inserted 33000 rows in 75.3ms      (single transaction, prepared stmt)
EXPLAIN: Index Scan using verses_pkey on verses
EXPLAIN:   Index Cond: ((translation = 'kjv') AND (book_num = 43) AND (chapter = 3))
chapter read: 20 verses in 41.2µs
ILIKE scan:  100 hits  in 241.4µs
DELETE by translation: 33000 rows in 27.2ms
```

Confirmed the planner pushes a PK-prefix predicate down to a bounded key scan.

### getbible.net v2 shape

`GET https://api.getbible.net/v2/kjv/43.json` verified live:

```
top keys: translation, abbreviation, lang, language, direction, encoding, nr, name, chapters
chapters: LIST (len 21 for John), each {chapter, name, verses}
verses:   LIST, each {chapter, verse, name, text}
```

Arrays, not maps — modelling this as objects keyed by number would silently
yield zero verses.

### rweb / element / go-styl API checks

- `Param` and `PathParam` are **identical aliases** over the same params slice
  (`request.go:122` and `:132`) — the plan's open question was moot.
- `rweb/middleware/stylus` exists only in `v0.1.27-0.20260707123520-9c2144b2ed7c`,
  **not** in the `v0.1.26` tag that `@latest` resolves to. Pinned the pseudo-version.
- element v0.6.0: `ForEach` takes no builder (breaking change from older docs);
  there is no `Mark()` element method, so `b.Ele("mark")` is used;
  `AcquireBuilder`/`ReleaseBuilder` are pool-safe because `b.Ele` closes over
  `b.s`, which `Reset()` does not replace.
- go-styl accepts both the indented and brace/semicolon dialects.

---

## What was built

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

Deps pinned: bytdb v0.8.0, element v0.6.0, go-styl v0.2.0, logger v1.3.0,
rweb v0.1.27-0.2026…, serr v1.4.0. Module `go 1.26.1` (bytdb requires ≥1.26.1).

---

## Deviations from PLAN.md, and why

### 1. `verses` uses a composite natural primary key

Plan said `verses(id SERIAL PK, …)` + an index on
`(translation, book_num, chapter, verse)`. Implemented as a composite PK on
those four columns and **no** surrogate key.

bytdb stores rows in PK order in one key space. The `EXPLAIN` above proves a
chapter read becomes one ordered range scan already sorted by verse — the
`ORDER BY verse` is free. A `SERIAL` PK would have put rows in insert order and
required a parallel index maintained on every one of the ~31k inserts per
translation: twice the write work for a strictly worse read path. The natural
key also makes re-download idempotent via a single bounded
`DELETE WHERE translation = $1 AND book_num = $2`.

A secondary index on `(book_num, chapter, verse)` remains, serving the reverse
"which translations have this verse" lookup for the parallel-translation hub.

### 2. Full study schema ships in Phase 1

Plan put `study.bytdb` in Phase 2. Implemented now because the schema is
declarative, `store.Open` opens both databases anyway, and **bytdb cannot add a
primary key after `CREATE TABLE`** — getting the tables right up front is the
safer order. Phase 2 becomes purely CRUD + UI.

### 3. Scripture search landed early

Plan put search in Phase 3. The header search box is on every page and would
otherwise 404, so the reference fast-path and `bible.SearchText` (ILIKE) are
wired to `/search` now. Notes search and the combined-scope page remain Phase 3.

### 4. Added a data-directory lock (not in the plan)

The plan warns that a sync daemon must never touch a live `.bytdb` (torn tail,
no file locking). The same hazard applies to two pbstudy processes — and it was
reproduced: a `pbstudy backup` ran happily against a live `pbstudy serve`,
opening the same `study.bytdb` from a second process.

`store/lock_unix.go` takes an exclusive `flock` on `<DataDir>/pbstudy.lock`
before either database is opened. flock rather than a PID file because the
kernel releases it on any exit, including `kill -9` — a stale PID file would
need manual cleanup and would train the user to delete it reflexively, exactly
when it was telling the truth. `lock_other.go` is a documented no-op for
non-unix builds.

Verified refusal message:

```
another pbstudy process is using this data directory
  dataDir=…/data  heldBy="pid 47400"
  hint="stop the running pbstudy, or use a different PBSTUDY_DATA_DIR"
```

### 5. Placeholder routes instead of 404s

`/notes`, `/tags`, `/sermons` are in the nav but land in later phases. They are
registered as honest "arrives in the next phase" pages — a nav link that
dead-ends on a generic error page reads as a bug.

---

## Bugs found and fixed during verification

- **`serr.Wrap(nil, …)` is not a no-op.** It prints
  `SErr: Not wrapping a nil error` to stderr. The
  `return serr.Wrap(tx.Commit(), "…")` idiom logged noise on every successful
  download. Commits are now checked explicitly. Three sites fixed
  (`download.go` ×2, `server.go` ×1).
- **Reference parser swallowed a trailing period.** `"Gen. 1:1"` failed: the
  backwards numeric-tail scan accepts `.` as a separator, so the abbreviation's
  period landed at the head of the locator, producing `":1:1"`. Fixed by
  `strings.TrimLeft(s[idx:], " .:-")`. Caught by the new test table, not by
  inspection.
- **Verse links lost the translation.** `/verse/43/3/16` had no `?t=`, so the
  hub fell back to whichever translation sorted first. Harmless with only KJV
  cached, wrong as soon as WEB/ASV land. Reader now emits `?t=<translation>`.
- **`highlight()` offset assumption.** Matching happens on lowercased copies and
  slices the original body to preserve casing — only valid while lowercasing
  preserves byte offsets. Added a length guard that falls back to unhighlighted
  text rather than slicing at a wrong offset.

---

## Verification results

| Check | Result |
| --- | --- |
| `pbstudy download kjv` | **31,102 verses** (exact KJV count) |
| Re-download idempotency | still 31,102, no duplicates |
| Chapter-count cross-check | 0 mismatches across all 66 books |
| Canon total (unit test) | 1,189 chapters, 66 books |
| `/read/kjv/43/3` | 200; 36 verses; contains "Nicodemus" and v16 |
| `/css/app.css` | 200 + `ETag: "004e69966af7e4a3"` |
| …with `If-None-Match` | **304 Not Modified** |
| ScriptTagger config placement | precedes the script tag in `<head>` |
| Scripture container | carries `blb-no-tag` (no double-linking) |
| All 66 BLB slugs | HTTP 200 (live, 14.6s) |
| Chapter boundaries | John 1 ← Luke 24; Gen 1 no prev; Rev 22 no next |
| 404s | `/read/kjv/43/99`, `/read/kjv/99/1`, `/nope` |
| Search ref fast-path | `?q=John+3:16` → 302 → `/verse/43/3/16?t=kjv` |
| Search text | `?q=lilies+of+the+field` → `<mark>`-highlighted Mat 6:28 |
| `backup` | snapshot written; live `.bytdb` never enters the sync dir |
| Concurrent-process lock | second process refused, names holding PID |
| build / vet / test / gofmt | all clean |

Route smoke test — `/` 200, `/read` 302, `/read/kjv/43/3` 200, `/read/go?…` 302,
`/verse/43/3/16` 200, `/search?q=grace` 200, `/search?q=John+3` 302,
`/settings` 200, `/notes` 200, `/tags` 200, `/sermons` 200, `/css/app.css` 200,
`/js/app.js` 200, `/nope` 404.

---

## Gotchas worth remembering

- **zsh `path` is tied to `PATH`.** `for path in …` inside a test loop wiped
  `PATH` and produced `command not found: curl` for every iteration. Use any
  other variable name.
- **`@latest` is not the newest code.** `rweb@latest` → `v0.1.26`, which has no
  `middleware/` directory at all. The stylus adapter only exists in the newer
  pseudo-version.
- **Go toolchain auto-switch**: bytdb v0.8.0 requires go ≥ 1.26.1; the local
  toolchain is 1.25.4, so Go silently switched to 1.26.5 for the build.

---

## State at end of session

Phase 1 complete. `PLAN.md` updated: Phase 1 struck through, four risks marked
resolved with their findings, and a "Phase 1 outcome" section added recording
the deviations, verification numbers, and file map.

### Next up — Phase 2 (notes / tags / xrefs)

Study CRUD, note editor + goldmark + `[[G26]]` Strong's links, verse-hub forms,
reader indicator dots, tags browser, and a browser check of ScriptTagger hover
popups inside note bodies. The study schema is already created, so this is
CRUD + UI only.

### Open item for Phase 4

`PLAN.md` specifies `model: claude-sonnet-5` for the AI draft. Confirm the exact
model ID against current API docs at implementation time rather than trusting
the string in the plan.
