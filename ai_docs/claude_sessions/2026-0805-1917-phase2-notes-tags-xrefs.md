# pbstudy — Phase 2: Notes, Tags, Cross-references

- **Session ID**: `101e1da0-d104-4eac-ad96-be776e678f6a`
- **Date**: 2026-08-05
- **Scope**: Implement Phase 2 of `PLAN.md` (study CRUD → annotated reader)
- **Outcome**: Complete. `go build` / `go vet` / `go test` / `gofmt` all clean.
  One plan item (ScriptTagger popups in a browser) could not be exercised — see
  "Not verified" below.

---

## Starting point

Phase 1 was done and committed: scripture cache, reader, verse hub, search, and
— usefully — the *full* study schema, which Phase 1 had shipped early on the
grounds that bytdb cannot add primary keys after `CREATE TABLE`. So Phase 2 was
purely CRUD and UI on top of tables that already existed.

`/notes`, `/tags` and `/sermons` were registered as honest "coming soon"
placeholders. Two of the three are now real.

---

## Probes run before writing code

Same discipline as Phase 1: assumptions get tested against the real libraries
before any of the design leans on them.

### 1. Does `element` escape anything?

```go
b.Div().R(b.T(`<script>alert(1)</script>`))
b.Input("type", "text", "value", `a" onfocus="x`)
```

Output:

```html
<div><script>alert(1)</script></div><input type="text" value="a" onfocus="x">
```

**No.** Neither `b.T()` nor attribute values are escaped. Confirmed by grepping
the package too — there is no `html.EscapeString` anywhere in element v0.6.0.

This directly contradicted a comment Phase 1 had left in `web/ui/search.go`
("both go through `b.T()`, which escapes"). Harmless while every rendered
string came from the compiled canon table or getbible.net. Not harmless the
moment notes became user input. This probe is the reason the escaping layer
became task #1 of the session rather than an afterthought.

### 2. What SQL does bytdb actually serve for the study tables?

Probed against a scratch `.bytdb`:

| Feature | Result |
| --- | --- |
| `deleted_at IS NULL` / `IS NOT NULL` | works |
| `ORDER BY … DESC`, `LIMIT $n` | works |
| `UPDATE … ` + `RowsAffected()` | works, accurate |
| `DELETE … WHERE` | works |
| Range predicate with a repeated `$3` | works |
| `INNER JOIN` and implicit join | works, correct results |
| `DISTINCT`, `IN (…)`, `GROUP BY` | works |
| Parenthesised `OR` inside `AND` | works |
| Unique-index violation | returns `unique index violation` |
| Nullable `TIMESTAMP` insert as Go `nil` | works; scans into `sql.NullTime` |

The join support was the consequential one — it meant `NotesForVerse`,
`NotesForTag` and `ChapterMarks` could filter tombstones in SQL instead of
intersecting id sets in Go.

### 3. Does rweb prefer a static route segment over a param?

`/notes/new` and `/notes/:id` conflict at the same depth. Probed with a live
server:

```
GET /notes            -> LIST
GET /notes/new        -> NEW          <- static wins
GET /notes/abc-123    -> DETAIL abc-123
GET /notes/abc-123/edit -> EDIT abc-123
POST /notes (form)    -> body and refs both read via FormValue
```

Static wins. No need to rename the route.

---

## What was built

### `study/` — the new package

| File | Contents |
| --- | --- |
| `study.go` | package doc, UUID ids, UTC clock, `placeholders()` for IN lists |
| `notes.go` | `Note` / `NoteRef` / `NoteDraft`, CRUD, `NotesForVerse` / `NotesForChapter` |
| `tags.go` | tag CRUD, on-demand creation, case-folded identity, `NotesForTag` |
| `xrefs.go` | `CrossRef` CRUD, both-direction queries, `Other()` |
| `marks.go` | `ChapterMarks` — every indicator dot for a chapter in two queries |
| `markdown.go` | goldmark in safe mode + `[[G26]]` expansion, `Excerpt` |
| `study_test.go` | markdown, excerpt, tag names, range expansion, title derivation |

### Web layer

`handlers_notes.go`, `handlers_tags.go`, `handlers_xrefs.go`, `support.go`
(+ `support_test.go`); `ui/notes.go`, `ui/tags.go`, `ui/escape.go`; `ui/verse.go`
rewritten; `ui/reader.go` extended with indicator dots and chapter-level notes.

`bible/ref.go` gained `ParseRefList` / `FormatRefList`; `bible/books.go` gained
`MaxChapterVerses`.

---

## Design decisions worth keeping

### One create path, two entrances

The verse hub's quick-add posts to the same `POST /notes` the full editor uses,
with the anchor and a `return` URL as hidden fields. A second "same thing but
from the hub" endpoint is how two paths drift apart.

### Anchors are typed, not picked

The References field takes `John 3:16-18; Rom 5:8` through the same parser as
the search box. `ParseRefList` returns what parsed *and* what did not, so three
good anchors and one typo saves the three and reports the one.

Semicolons take precedence over commas when both appear. A segment like
`Genesis 1:1, 2` is then rejected whole rather than silently truncated to
`Genesis 1:1` — storing an anchor the user did not ask for is worse than
telling them it did not parse. (A test asserted the truncating behaviour first;
the test was wrong, not the code.)

### Range containment, not equality

A note on `John 3:16-18` must surface while reading verse 17, so every lookup
is `verse_start <= $v AND verse_end >= $v`. A whole-chapter anchor is stored as
`verse_start = verse_end = 0` and deliberately does *not* match a specific
verse — `ChapterVerseKey` (= 0) is the map key for chapter-level marks, so the
same value flows from the schema through the map to the view unchanged.

### Tombstones, and one deliberate asymmetry

- `DeleteNote` tombstones the note and **keeps** its refs and tag links. Every
  read joins back to `notes` and filters `deleted_at IS NULL`, so the children
  are inert rather than stale, and an undelete stays one UPDATE away.
- `DeleteTag` tombstones the tag but **removes** `note_tags` rows, because a
  retired tag has to stop appearing on notes immediately.

### Tag identity is case-folded in Go, not SQL

The unique index on `tags.name` is case-sensitive, so "Grace" and "grace" could
both be inserted. `writeTagLinks` reads the (tens-of-rows) tag table once per
save and matches on a lowercased key. Also sidesteps escaping LIKE
metacharacters out of user-typed tag names.

### Markdown expands shortcodes *before* goldmark

`[[G26]]` becomes `[G26](https://…/lexicon/g26/kjv/tr/)` — ordinary Markdown
link syntax, not raw HTML. That is what lets goldmark stay in its default safe
mode (raw HTML dropped), which in turn is what makes it legitimate to write the
result with `b.WriteString`. Styled via `a[href*="/lexicon/"]` since the
renderer emits no class.

### Disclosures instead of `confirm()`

Delete, tag-describe and both quick-adds hide behind `<details>`: no script,
keyboard accessible for free, two deliberate actions instead of a modal
dismissed by reflex.

---

## Bugs found and fixed during verification

### 1. `element` escapes nothing (found by probe, confirmed by exploit)

Fixed by `web/ui/escape.go`, which states the rule for the package: *anything
that originated outside the binary goes through `esc()`*. Two live holes were
confirmed with real requests and then closed:

- A note titled `</title><script>alert(1)</script>` **broke out of the document
  `<title>`**. `<title>` is RCDATA so most markup inside it is inert — but a
  literal `</title>` still closes the element. Verified before:

  ```html
  <title></title><script>alert(1)</script> · pbstudy</title>
  ```

  and after:

  ```html
  <title>&lt;/title&gt;&lt;script&gt;alert(1)&lt;/script&gt; · pbstudy</title>
  ```

- The search page echoed its query unescaped via `%q`.

`highlight()` in `search.go` was reworked to escape *after* matching, not
before: escaping first would turn a searched-for `&` into `&amp;` and the index
arithmetic would slice through the middle of an entity.

Scripture bodies are escaped now too — they arrive over the network and were
never ours either.

### 2. Translations from the URL were never validated

`/read/:translation/…` passed the raw path segment into hrefs, a hidden form
field and the ScriptTagger config. rweb did not URL-decode the segment so it
was inert in practice, but the value was one decode away from an attribute
break. Now: `/read` 404s an unknown translation, `?t=` and `?translation=` fall
back to the default. Checking once at the boundary beats escaping correctly in
four places.

### 3. bytdb rejects a qualified `ORDER BY` under `SELECT DISTINCT`

```
for SELECT DISTINCT, ORDER BY expressions must appear in select list
```

`ORDER BY n.updated_at DESC` fails **even though `n.updated_at` is in the
select list** — bytdb matches on the *output* column name. The bare
`ORDER BY updated_at DESC` works. Isolated with a targeted probe that also
confirmed `DISTINCT` is genuinely load-bearing (one note carrying two anchors
over the same verse came back twice without it).

This silently emptied the verse hub's notes list — the reader dots worked, so
the failure looked like a UI bug rather than a query bug. Found via the error
in the server log, not by the compiler. Affects `NotesForVerse`,
`NotesForChapter`, `NotesForTag`.

### 4. Open redirect on every form's `return` field

`safeReturn` rejects empty, relative, protocol-relative (`//host/path`),
absolute, and backslash/CRLF targets. Local app or not, a request is a request.
Covered by `web/support_test.go`.

---

## End-to-end verification (live server, scratch data dir, 31,102 KJV verses)

| Check | Result |
| --- | --- |
| `POST /notes` with `refs=John 3:16-18; Rom 5:8` | 303 → permalink |
| Title derived from body first line, `#` stripped | "God so loved" |
| Both tags created, chips link to topical pages | ✓ |
| `[[G26]]` → `/lexicon/g26/kjv/tr/` | ✓ |
| `[[H430]]` → `/lexicon/h430/kjv/wlc/` (Hebrew corpus) | ✓ (test) |
| Raw `<script>` in body | dropped, `raw HTML omitted` |
| KJV text inlined for both anchors | ✓ |
| Verse hub John 3:16 **and** 3:17 show the note | ✓ (containment) |
| Reader dots on John 3:16, 17, 18 | `title="1 note"` |
| John 3:1 (no note) has no dot | ✓ |
| Xref John 3:16 → Rom 5:8, seen from John 3:16 | "From here" |
| …seen from Rom 5:8, never created there | "To here" |
| Combined dot on Rom 5:8 | `title="1 note, 1 cross-reference"` |
| Edit drops an anchor → hub entry and dot vanish | ✓ |
| Delete → 404 permalink, dots cleared, tag page empty | ✓ |
| Xref survives the note's deletion | ✓ |
| Whole-chapter note appears above the text, marks no verse | ✓ |
| Bad reference → editor re-renders with typing preserved | ✓ |
| Bad xref target → hub re-renders with the complaint | ✓ |
| Hostile title/tags escaped in text and `value=` | ✓ |
| `return=//evil.example/x` | ignored, redirects to the note |
| `/read/bogus/43/3` | 404 |
| `/css/app.css` | 200, 9,373 bytes |
| Phase 1 directory lock refused a second `serve` | ✓ (unplanned but welcome) |

### Not verified

**ScriptTagger hover popups in a real browser.** The Chrome instance available
in this environment cannot reach this machine's localhost —
`ERR_CONNECTION_REFUSED` on both `localhost` and `127.0.0.1`, while curl gets
200 on both, including after retrying outside the command sandbox. Stopped
after three attempts rather than rabbit-holing.

Confirmed from the served HTML instead: the `BLB.Tagger` config precedes the
script tag, `blb-no-tag` sits on the reader's scripture container and the note
page's Scripture card, and **not** on the note body — which is the split the
whole integration rests on. Still worth a manual look:

```sh
PBSTUDY_DATA_DIR=/tmp/pbstudy-dev go run . serve
```

---

## Dependencies added

- `github.com/google/uuid v1.6.0` — syncable row ids
- `github.com/yuin/goldmark v1.7.13` — note body rendering, default (safe) mode

---

## State at end of session

Phase 2 complete. `PLAN.md` carries a full "Phase 2 outcome" section with the
deviations and reasoning; `README.md` status moved to Phase 2 of 5, gained a
"Writing notes" section, and the roadmap now shows Phase 3 as next.

**Phase 3** is notes search, combined scope, and topical study pages — plus
downloading WEB and ASV to light up the parallel-translation verse hub. Note
that `bible.SearchText` and the reference fast-path already landed in Phase 1,
so Phase 3 is the notes half of search plus the combined view.

### Carried forward

- The escaping rule in `web/ui/escape.go` applies to every new view. There is
  no framework backstop.
- bytdb's `DISTINCT` + `ORDER BY` constraint will bite again the first time
  Phase 3 writes a deduplicating search query.
- The reader loads `ChapterMarks` on every chapter render. Fine at present
  scale; if it ever shows up, the fix is a per-chapter cache invalidated on
  study writes, not a schema change.
