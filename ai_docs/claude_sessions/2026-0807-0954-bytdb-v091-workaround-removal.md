# pbstudy — retiring the bytdb workarounds (v0.8.0 → v0.9.1)

- **Session ID**: `15c044e4-77ca-43f7-a102-d0c82c9648bd`
- **Date**: 2026-08-07
- **Scope**: Upgrade bytdb and remove the workarounds that two of its v0.8.0
  shortcomings had forced on this codebase.
- **Outcome**: Complete. Both workarounds gone. `gofmt` / `go build` / `go vet`
  / `go test -race` all clean; verified against a copy of a real pre-existing
  database driven by the actual binary.

---

## Starting point

The session opened as a plain dependency bump — v0.8.0 → v0.9.0, which built
and vetted clean with no code changes. The real work came from the follow-up
observation that some of this code was shaped *around* bytdb rather than by it,
and that those shapes might now be obsolete.

Nothing in the code said "workaround" — a grep for the word across the repo
returned only session docs. The two constraints were recorded in the Phase 1
and Phase 2 session docs and in comments that described them as live rules, so
the archaeology started there rather than in the code.

Two workarounds were found, and the honest answer turned out to be different
for each.

---

## Workaround 1: the unqualified `ORDER BY` under `SELECT DISTINCT` — removed

Phase 2 had found that bytdb v0.8.0 rejected `ORDER BY n.updated_at` under
`SELECT DISTINCT` even when `n.updated_at` was in the select list, because it
matched on the *output* column name. The three affected queries —
`NotesForVerse`, `NotesForChapter`, `NotesForTag` — worked around it by
ordering on a bare `updated_at`.

bytdb v0.9.0 fixed this (`sql/exec.go`, `resolveQualifiedOrder`), resolving a
qualified name against the select list the way Postgres does. All three queries
now qualify the key, which is what a two-table join wanted to say all along:
only the note's clock orders the result, not the join table's.

`study/search.go` carried a related comment explaining that its queries avoided
the trap "by construction" — that framing is now archaeology, so the comment was
rewritten to say the useful thing instead (the searchable text lives on the
notes row, so no join and no DISTINCT is needed).

---

## Workaround 2: the schema bootstrap probe — kept, then removed a version later

`store/schema.go` achieved idempotency by querying `information_schema.tables`
and `pg_class`, creating only the relations not already present, with an
"already exists" string match as a backstop.

**At v0.9.0 this had to stay.** The engine probe showed why: v0.9.0 added
`CREATE TABLE IF NOT EXISTS`, but `CREATE INDEX IF NOT EXISTS` was still a
syntax error, and this schema is roughly half indexes. Converting only the
tables would have split the bootstrap into two idioms and still needed the
catalog probe for the indexes. The comment was corrected to say so and the
mechanism was left alone.

**bytdb v0.9.1 closed the gap** (that release was prompted by the report from
the first half of this session), extending the guard clause to
`CREATE [UNIQUE] INDEX` and adding `DROP INDEX IF EXISTS`. The probe then had
nothing left to do:

| Removed | Why it is no longer needed |
| --- | --- |
| `existingRelations()` | the two catalog queries; the DDL now answers "does this exist" itself |
| `isAlreadyExists()` | the string-match backstop to a probe that no longer exists |
| `ddl{name, stmt}` struct | the `name` field existed so the probe could skip; now a plain `[]string` |

`store/schema.go` is 45 lines shorter and `applySchema` is a loop.

### Why the struct went rather than keeping `name` for error messages

The name's only remaining use was error context. Keeping it would have left a
field that no longer does anything load-bearing but still has to be maintained
in step with the statement beside it — the exact shape of metadata that rots
silently, since a mismatch would no longer be caught by anything. `firstLine()`
derives the identifying line from the statement that actually ran, so it cannot
disagree with it.

### What the guard does not do

`IF NOT EXISTS` is a **name check only** — bytdb never compares the requested
columns or uniqueness against the existing relation, exactly as Postgres does
not. Bootstrapping is therefore idempotent but is not a migration: changing a
column list still needs real migration work. This is written into the file's
header comment, because the mechanism now *looks* like it would reconcile a
schema change, and it does not.

---

## Not workarounds, deliberately left alone

- **`ALTER TABLE ADD PRIMARY KEY` is still unsupported**, and the v0.9.1 notes
  confirm it is a deliberate engine constraint rather than a gap. It stays
  documented in `store/schema.go` because it is why every primary key is
  declared inline and why the full study schema shipped in Phase 1.
- **TIMESTAMP is still microsecond precision.** Probed rather than assumed,
  because `syncer.newer` truncates both sides of its clock comparison to
  microseconds and would have been worth revisiting had this changed. It has
  not; the sync engine is untouched.
- **The composite natural PK on `verses`** is a design decision that the
  upgrade does not affect.

---

## Verifying against the engine, not the release notes

Every capability claim in this session was checked by running SQL against a
scratch `.bytdb` file before any code changed. This mattered twice:

- The v0.9.0 release notes could easily have been read as "IF NOT EXISTS is
  supported"; the probe is what established that indexes were still excluded,
  which is the fact that kept the bootstrap workaround alive for a version.
- For v0.9.1 the probe checked the two things a bootstrap actually depends on
  and that a "syntax accepted" result would not prove: that a guarded
  re-create leaves existing rows in place, and that the unique index on
  `tags.name` is still *enforced* afterwards rather than merely re-noticed.

---

## The retyping risk, and how it was handled

Rewriting `store/schema.go` meant retyping 17 DDL statements — the kind of edit
where a dropped column definition surfaces months later as a failing query. The
statements were extracted from both the old and new files, normalized for
whitespace with `IF NOT EXISTS` stripped, and compared: all 17 identical, and a
second pass confirmed none was left unguarded.

(An early version of that check reported a mismatch which turned out to be two
backtick fragments inside *comments*, and a "18 statements" line that was a
hardcoded literal in the print rather than a count. Both were run down rather
than waved through — a verification script that is wrong in a reassuring
direction is worse than none.)

---

## Tests added

Both workarounds sat in code with a coverage gap, which is how the original
DISTINCT bug reached a running server in Phase 2.

| File | Covers |
| --- | --- |
| `study/queries_test.go` | the three DISTINCT queries: overlapping-anchor collapse, containment at a mid-range verse, chapter-vs-verse anchor split, tag list with tombstone and untagged exclusions |
| `store/schema_test.go` | idempotency over three runs, data survival with the unique index still enforced, **bootstrap over a legacy pre-guard database**, every relation queryable, `firstLine`, and real DDL errors still stopping startup |

`store` had **no test files at all** before this session.

### The regression guard was proved, not assumed

A test that passes on the fixed version proves nothing about whether it would
have caught the bug. bytdb was temporarily downgraded to v0.8.0 and all three
new query tests failed with the original error —

```
for SELECT DISTINCT, ORDER BY expressions must appear in select list
```

— then v0.9.1 was restored and `go mod tidy` cleared the stale `go.sum` lines
the downgrade left behind.

### Ordering assertions tolerate ties

`assertNewestFirst` checks the sequence is non-increasing rather than pinning a
particular winner. Two notes written in the same microsecond are
indistinguishable to the storage, and a stricter assertion would fail on a fast
enough machine.

---

## End-to-end verification (real binary, real pre-existing database)

The strongest available test of the schema change is a database created by the
*old* bootstrap. The dev sandbox is exactly that, so it was **copied** to the
scratchpad and the server run against the copy — the original dev data was
never opened.

| Check | Result |
| --- | --- |
| Bootstrap over the legacy database | clean start, no error, no notice-level noise |
| Dashboard | 3 notes · 1 cross-reference · 1 sermon — all intact |
| `/tags/<Grace>` (`NotesForTag`) | both tagged notes, refs and chips rendered |
| `/verse/45/3/25` (`NotesForVerse`) | "Propitiation and the mercy seat" |
| `/verse/43/3/16` and `/verse/43/3/17` (`NotesForVerse`) | "The love of God" at both — containment across a 16-18 anchor |
| `/read/kjv/43/3` (`NotesForChapter`) | "Nicodemus comes by night", the chapter-level anchor |
| Route sweep (9 routes incl. two search scopes) | all 200, no 5xx |
| Server log | zero errors |

This is the check that matters most for this change: the Phase 2 failure mode
was *silent* — the queries errored, the lists rendered empty, and the reader's
indicator dots kept working, so it looked like a UI bug. Populated lists on a
real database are the evidence.

The server was stopped by resolving the PID on the port and confirming its
command line before signalling it, rather than by a pattern kill.

---

## State at end of session

All five phases plus compaction remain complete on `main`. bytdb is pinned at
**v0.9.1**. `PLAN.md`'s two risk entries now record the upstream fixes and what
replaced each workaround.

| File | Change |
| --- | --- |
| `go.mod` / `go.sum` | bytdb v0.8.0 → v0.9.1 |
| `store/schema.go` | probe-and-create → guarded DDL; −45 lines |
| `study/notes.go` | two queries qualify `ORDER BY`; comment rewritten |
| `study/tags.go` | `NotesForTag` qualifies `ORDER BY` |
| `study/search.go` | stale bytdb-rule comment rewritten |
| `store/schema_test.go` | new — first tests in the package |
| `study/queries_test.go` | new |
| `PLAN.md` | both risk entries updated |

### Carried forward

- **A successful live AI draft has still never been observed** (no
  `ANTHROPIC_API_KEY` in any session so far).
- **ScriptTagger hover popups in a real browser**, unverified since Phase 2 —
  the Chrome instance available here cannot reach this machine's localhost.
- **Live-tag duplicate files still accumulate**, one per machine per name.
- **Two machines editing the same note while both are offline still loses one
  side.** Accepted for a single user; the snapshots are the net.
- **Only the syncer path installs a signal handler.**
- **Compaction is per machine and has to be run on each.**
- **`DROP INDEX IF EXISTS` is now available** (v0.9.1) and unused here. Worth
  remembering if an index ever needs replacing — that is the one piece of
  migration this schema file could support without a rewrite.
- The dev sandbox is still `PBSTUDY_DATA_DIR=/tmp/pbstudy-dev go run . serve`,
  untouched by this session.
