# pbstudy — Phase 3: Search + topical study

- **Session ID**: `92864131-ec69-4a27-a3a2-f24a5a0b1d31`
- **Date**: 2026-08-05
- **Scope**: Implement Phase 3 of `PLAN.md` (combined search, notes search,
  topical study pages, WEB + ASV download)
- **Outcome**: Complete. `go build` / `go vet` / `go test` / `gofmt` all clean;
  every behaviour below checked against a live server.

---

## Starting point

Phase 2 left `bible.SearchText` (scripture ILIKE) and the reference fast-path
already wired to `/search` — Phase 1 had landed them early because the header
search box is on every page. So Phase 3 was the *other* half: searching the
study database, putting both halves on one page, and giving a tag page enough
structure to be called a topical study.

Only KJV was downloaded, so the parallel-translation verse hub had never
actually been seen with more than one column.

---

## What was built

### `study/search.go` — the study-side search layer

| Function | Purpose |
| --- | --- |
| `SearchNotes` | title + body ILIKE, recency order, refs and tags attached |
| `SearchTags` | name + descrip ILIKE, note counts from the same helper `ListTags` uses |
| `SearchXrefs` | cross-reference **comments** — text that was otherwise unreachable |
| `NoteHits` | wraps already-loaded notes as hits, so the ref fast-path renders identically |
| `Snippet` | plain-text window centred on the first match |

None of these join. `NotesForVerse` and friends need `DISTINCT` over a join and
therefore hit bytdb's "ORDER BY must appear in select list" rule (Phase 2, bug
#3); searching notes needs no join at all, so the trap is avoided by
construction rather than worked around. The carried-forward warning from the
Phase 2 session doc turned out not to bite.

### `/search` — rebuilt

`handlers_search.go` and `ui/search.go` were rewritten. The page carries its own
form (query + version), scope tabs as plain links, and four result groups:
Scripture, Notes, Topics, Cross-references. Scope semantics live in
`ui.NormalizeScope` and `ui.Search.Wants` — the handler asks the same method
before doing the work, so the page cannot query one thing and display another.

### Topical study

`study.RefsAcross(notes)` collects the distinct anchors across a set of notes,
deduplicated and sorted by a new `bible.Ref.Compare`. `/tags/:id` renders it as
a **Passages** row above the notes. Derived from the notes already loaded, so it
cannot disagree with the list under it.

### Scripture

`pbstudy download all` into the dev data dir: KJV 31,102 · WEB 31,095 · ASV
31,086 verses, no chapter-count mismatches. (WEB and ASV genuinely carry fewer
verses — they omit disputed passages the KJV includes.)

---

## Design decisions worth keeping

### Two fast-path rules, not one

A reference query jumps to the verse hub — *except* under `scope=notes`, where
it resolves through `NotesForVerse` / `NotesForChapter` instead and offers the
jump as a link. Someone who narrowed to their own writing and then typed
"John 3:16" is asking *what have I written about this*, not *take me there*.

### Search covers everything the user typed

Not just note bodies: tag names and descriptions, and cross-reference comments.
A comment like "the love of God demonstrated, not merely declared" is often the
only place a thought was written down. Text no query can reach is lost text.

### Every cap is visible

Scripture 200, study 100. When a group fills its limit the page says so. A list
that silently stops reads as "there is nothing more".

### A failing study query fails the page

The opposite of the reader, which degrades to no indicator dots and still shows
scripture. A search that quietly drops the notes group answers "where does this
come up?" with a confident, wrong "nowhere".

### Snippets are cut from stripped text

`Snippet` strips Markdown and collapses whitespace *first*, then locates the
match in the stripped copy — searching the raw source and slicing the stripped
copy at that offset would land somewhere else. Two fallbacks to a leading
excerpt rather than a guess: a match that lived only inside removed markup (a
link URL), and any input where lowercasing changes byte length, where a wrong
offset would split a rune. Same guard as `ui.highlight`.

---

## Bug found: `ParseRef` could not read `Lev 16:14` or `Rev 22:1`

Found while seeding a note anchored to Leviticus 16:14 — the create came back
`200` (editor re-render) instead of `303`:

```
Not saved. Could not read 1 reference: Lev 16:14.
```

The parser walks backwards over the numeric tail to split book from locator, and
accepted `v` as a chapter/verse separator (the `John 3v16` form) in **any**
position. So `Lev 16:14` split as book `Le` + locator `v 16:14`, and `Le` is not
a book. `Rev 22:1` failed the same way — two of the most-cited abbreviations in
the canon.

Fix: `v` is a separator only when a digit sits immediately before it, which is
the only position `3v16` ever puts it in. `bible/ref_test.go` now covers
`Lev`/`Rev`/`REV` alongside the existing `John 3v16`.

No existing test caught this; the Phase 1 suite tested `3v16` and the book
lookup separately, and the two only interact in the backwards scan.

---

## Measured

`EXPLAIN` on the scripture search, against the three-translation cache:

```
Limit
  ->  Index Scan using verses_pkey on verses
        Index Cond: (translation = 'kjv')
        Filter: (body ~~* '%Nicodemus%')
```

The primary key's leading column bounds the scan to one translation's ~31k rows,
so search does **not** slow down as more translations are downloaded.

| Query | Rows | Time |
| --- | --- | --- |
| `%Nicodemus%` | 5 | 85 ms |
| `%love%` | 200 (capped) | 46 ms |
| `%zzzz%` | 0 | 80 ms |

A common word is *faster* than a rare one: the `LIMIT` sits above the scan and
stops it early. Study-scope search is 0.6 ms. Both well inside the plan's 100 ms
target, so no index was built.

This also corrects a Phase 1 figure the README carried — "ILIKE scan of a
translation in 241 µs" is not what a full scan costs; the README now states the
measured range and the reason for the shape.

---

## End-to-end verification (live server, three translations, seeded study data)

| Check | Result |
| --- | --- |
| `/settings` lists KJV/WEB/ASV with counts | 31,102 / 31,095 / 31,086 |
| Verse hub John 3:16 in three translations | KJV, WEB, ASV rows |
| Reader translation switcher offers three | ✓ |
| `/read/web/43/3` renders WEB text | "his one and only Son" |
| `?scope=scripture&q=love` | 200 results + truncation notice |
| `?q=grace` (all scopes) | Scripture 160 · Notes 2 · Topics 1 |
| `?q=covenant&scope=notes` | 2 notes, `<mark>covenant</mark>` in both snippets |
| `?q=demonstrated&scope=notes` | xref comment hit, both ends linked |
| `?q=John+3:16` | 302 → `/verse/43/3/16?t=kjv` |
| `?q=John+3` | 302 → `/read/kjv/43/3` |
| `?q=John+3:16&scope=notes` | anchored notes, no redirect |
| `?q=John+3&scope=notes` | the chapter-level note |
| `?scope=bogus` | falls back to "all", 200 |
| `?q=zzzqqqxyz` | "Nothing matched" notice |
| Hostile query in `<title>` and `value=` | escaped |
| Hostile query in scope-tab hrefs | percent-encoded |
| Grace tag page | 4 passages, canonical order |
| `/css/app.css` | 200, 10,278 bytes, new rules present |
| Reader indicator dots | unchanged |
| Whole route sweep | no 5xx, no server-log errors |

---

## State at end of session

Phase 3 complete. `PLAN.md` carries a full "Phase 3 outcome" section; `README.md`
moved to Phase 3 of 5 and gained a "Searching" section.

**Phase 4** is the sermon builder: sermons CRUD, outline UI, `AssembleOutline`,
Markdown/HTML export, then streaming AI drafting via the Anthropic API.

### Carried forward

- The escaping rule in `web/ui/escape.go` still has no framework backstop.
  `ui/search_test.go` now pins the `highlight()` half of it.
- `bible.Ref.Compare` exists for the sermon builder's benefit as much as the tag
  page's — outline sections will want canonical ordering too.
- `study.NoteHits` is the seam for any future "notes that match X" view: build
  the `[]Note` however you like, render through the one search component.
- The dev data dir `/tmp/pbstudy-dev` now holds all three translations, so
  `PBSTUDY_DATA_DIR=/tmp/pbstudy-dev go run . serve` is a ready-made sandbox.
