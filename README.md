# pbstudy

A personal Bible study application that runs on your own machine.

Track notes and correlations between scriptures, study topics, and assemble
sermons and teachings from what you have collected. Scripture is cached
locally, so study works with the network off.

> **Status: complete through Phase 5.** The scripture cache, chapter reader,
> verse hub, notes, tags, cross-references, search across all of them, the
> sermon builder with its exports and optional AI drafting, and file-based sync
> across machines are all working. See [Roadmap](#roadmap) and
> [PLAN.md](PLAN.md).

---

## Why it exists

Study tools tend to be either a website that owns your notes or a document
folder that knows nothing about scripture. pbstudy is neither: your notes live
in a database on your machine, they are anchored to verse ranges, and the
scripture they point at is right there offline.

Three design commitments follow from that:

- **Local first.** One binary, two database files, no server to run and no
  account to create. The only network calls are the one-time scripture
  download, the optional AI drafting, and links out to Blue Letter Bible.
- **Your data stays yours.** Notes are plain Markdown in a local database, and
  sync (when it lands) is JSON files in a directory you choose — point it at
  iCloud, Syncthing, or a git repo. There is no service in the middle.
- **Public-domain scripture.** KJV, WEB and ASV, so the text can simply ship.

---

## Install

Requires Go 1.26.1 or newer.

```sh
go install github.com/rohanthewiz/pbstudy@latest
```

Or from a clone:

```sh
git clone https://github.com/rohanthewiz/pbstudy.git
cd pbstudy
go build -o pbstudy .
```

## Quick start

```sh
pbstudy download kjv     # ~31,100 verses, about 30 seconds
pbstudy serve            # http://localhost:8000
```

`download all` fetches KJV, WEB and ASV, which enables the parallel-translation
view on the verse hub.

---

## Usage

```
pbstudy [serve]                      start the web app (default)
pbstudy download <kjv|web|asv|all>   fetch scripture into the local cache
pbstudy backup [dir]                 snapshot the study database
pbstudy sync                         reconcile with the sync directory
```

Downloads are idempotent — re-running replaces a translation in place rather
than duplicating it, so it is also how you pick up an upstream text correction.

### Configuration

Everything comes from the environment; there is no config file.

| Variable | Default | Purpose |
| --- | --- | --- |
| `PBSTUDY_DATA_DIR` | `~/.pbstudy` | where the databases live |
| `PBSTUDY_SYNC_DIR` | *(unset)* | sync and backup directory |
| `PBSTUDY_PORT` | `8000` | HTTP listen port |
| `ANTHROPIC_API_KEY` | *(unset)* | enables AI sermon drafting |

The API key is held in memory only. It is never written to a database, an
export, or a backup, and the drafting UI is hidden entirely when it is unset.

### Getting around

- `/` — the whole canon as a grid
- `/read/:translation/:book/:chapter` — the reader (← / → page through
  chapters). A verse with notes or cross-references carries a marker you can
  click straight through to.
- `/verse/:book/:chapter/:verse` — the verse hub: every translation at once,
  the notes and cross-references attached to that verse, forms to add more,
  plus links into Blue Letter Bible's interlinear and lexicon
- `/notes` — everything you have written, most recently edited first
- `/tags/:id` — a topic: every note carrying that tag, whatever passage each
  one is anchored to
- `/sermons/:id` — the outline builder, with Markdown and HTML exports
- `/search?q=` — press `/` from anywhere to jump to the search box
- `/settings` — resolved paths, cache state, and the sync and backup buttons

### Searching

One page answers "where does this come up?" across both halves of the app:

- **Scripture** — a case-insensitive scan of the translation you pick, with the
  match highlighted in place.
- **My notes** — note titles and bodies, tag names and descriptions, and the
  comments on your cross-references. A comment you typed once while linking two
  passages is findable text, not a dead end.

Scope tabs narrow it to either half, and every search is a plain URL you can
bookmark: `/search?q=grace&scope=notes&translation=web`.

Type a reference instead of a phrase and the search takes you straight there —
`John 3:16`, `1 Jn 2:1`, `II Corinthians 5:17`, `Ps 23`, `Jude 3`, `Lev 16:14`
and `Gen. 1:1` all parse. The one exception is the **My notes** scope, where a
reference lists what you have written *about* that passage instead of jumping
to it, since that is what you were asking for.

### Writing notes

A note is Markdown anchored to one or more passages. Anchors are typed, not
picked from menus — the References field takes the same references the search
box does, several at a time:

```
John 3:16-18; Rom 5:8; Ps 23
```

A range anchors to every verse in it, so a note on `John 3:16-18` surfaces
while you are reading verse 17. A chapter on its own (`John 3`) anchors to the
chapter and appears above the text rather than beside a verse.

Two more things happen inside a note body:

- `[[G26]]` and `[[H430]]` become Blue Letter Bible lexicon links — Greek and
  Hebrew corpora are chosen from the prefix letter.
- Scripture references you type get BLB's hover popups. Scripture *we* render
  does not, so the verses in the reader keep our own links instead of being
  double-linked.

Tags are created by typing them. There is no tag manager to visit first: put
`Grace, Covenant` in a note's tag field and both exist, and `/tags/…` becomes
a topical study: the passages that topic touches — gathered from every note
carrying the tag, deduplicated and put back into canonical order — above the
notes themselves.

Cross-references are drawn from the verse hub and shown from both ends. A link
you record from Romans while studying Paul is waiting for you in Genesis when
you get there.

Nothing is erased. Deleting a note or a tag retires it with a tombstone, which
is what lets the deletion travel to your other machines once sync lands.

### Building a sermon

A sermon is an ordered outline of four kinds of section:

| Kind | What it is |
| --- | --- |
| **Heading** | a movement of the sermon |
| **Passage** | a reference, looked up when the outline is assembled |
| **Note** | one of your notes, inlined whole |
| **Point** | something you want to say |

Add sections at the bottom of the builder, reorder them with the arrows, and
export whenever you like:

- `/sermons/:id/export.md` — Markdown, with the scripture inlined verbatim from
  your cache and each note's body and anchors underneath its title.
- `/sermons/:id/export.html` — the same document as a standalone page. Its
  styles travel inside it, so it reads and prints correctly from a downloads
  folder, an email, or a tablet on a lectern.

A passage section stores the *reference*, not the text. Re-download a
translation and every export picks up the new text; nothing goes stale. If a
note gets deleted or a passage is not cached, the export says so in place
rather than quietly leaving a hole.

### Drafting with AI (optional)

Set `ANTHROPIC_API_KEY` and the builder grows a **Draft with AI** button. Leave
it unset and the button is simply absent — everything else, exports included,
works the same.

The drafter is handed the assembled outline: the same Markdown the export
produces, no more. It is asked to follow the outline's order, to quote the
supplied scripture exactly, never to cite a verse the outline did not give it,
and to develop your notes rather than replace them. The draft streams into the
page as it is written and is saved when it finishes.

The key is read from the environment at startup and held in memory. It is never
written to either database, to an export, or to a backup.

### Syncing across machines

Point `PBSTUDY_SYNC_DIR` at a folder your sync service already keeps in step —
an iCloud Drive folder, a Syncthing share, a git checkout — and restart:

```sh
PBSTUDY_SYNC_DIR=~/Library/Mobile\ Documents/com~apple~CloudDocs/pbstudy pbstudy serve
```

Every note, tag, cross-reference and sermon is written there as its own small
JSON file, named for its id. Scripture never is: it is 28 MB of text you can
re-download in thirty seconds.

```
<sync>/notes/<uuid>.json      one file per note — body, anchors, tag names
      /tags/<uuid>.json
      /xrefs/<uuid>.json
      /sermons/<uuid>.json    outline and draft included
      /backups/study-<ts>.bytdb
```

Nothing has to be triggered. Edits are exported a couple of seconds after you
make them, whatever arrived while pbstudy was closed is imported at startup, and
anything still pending is flushed when the process is asked to stop. **Sync now**
and **Back up the study database** on the settings page are there for when you
want to force the issue, and `pbstudy sync` does the same thing from a script.

Conflicts are settled last-writer-wins, per row, on the `updatedAt` clock: the
newer of the file and the row overwrites the other, and neither side has to have
seen the other before. Deletes travel as tombstones rather than as absences —
a row that simply vanished would look identical to one the other machine has
not seen yet, and would come straight back on the next import.

Two things are worth knowing:

- **Editing the same note on two machines while both are offline loses one
  side.** That is the accepted trade for a single-user tool, and it is what the
  snapshots are for.
- **The sync folder must sit outside the data directory** (and vice versa).
  pbstudy refuses to start otherwise — see below.

Every run reports what it did, on the settings page or on the terminal:

```
Sync with ~/iCloud/pbstudy
  1 change in, 0 files out.
  Notes              1 in · 1 unchanged
  Tags               nothing to do (6 already in step)
  Cross-references   nothing to do (2 already in step)
  Sermons            nothing to do (2 already in step)
```

That report is the whole user interface for a feature whose success is
otherwise invisible. A file it cannot use — unreadable JSON, a record in the
wrong folder, a format written by a newer pbstudy — is named and left alone
rather than skipped in silence.

---

## How it works

### Two databases, deliberately

Storage is [bytdb](https://github.com/rohanthewiz/bytdb), an embedded SQL
database, in two files under the data directory:

| File | Contents | Policy |
| --- | --- | --- |
| `bible.bytdb` | scripture cache | rebuildable, never synced, relaxed durability |
| `study.bytdb` | notes, tags, cross-references, sermons | irreplaceable, full durability |

Splitting them is what makes a backup small enough to take often, and lets a
stale scripture cache be deleted without a second thought.

### Never copy a live database

bytdb does no file locking, so a copy taken while the process is appending has
a torn tail. Three consequences, all enforced rather than documented:

- **Never point a sync daemon at the data directory.** Use
  `pbstudy backup` (or the settings page's button), which takes an internal
  snapshot and writes a self-consistent file that is safe to drop in iCloud or
  Syncthing. This is enforced, not advised: startup fails if the sync directory
  is the data directory, or if either contains the other.
- **What the sync folder holds is JSON, not the database.** One small file per
  row, replaced by atomic rename, so a daemon copying the folder mid-write can
  at worst pick up a file a moment late — never half of one.
- **Only one pbstudy process per data directory.** Startup takes an advisory
  `flock`; a second process is refused and told which PID holds the directory.
  The kernel releases the lock on exit, including a crash, so there is no stale
  lock file to clean up.

### Scripture storage

Verses are keyed on `(translation, book_num, chapter, verse)` as a composite
primary key. bytdb stores rows in primary-key order in a single key space, so
reading a chapter is one bounded range scan that arrives already sorted by
verse — no secondary index, and no sort step.

Measured on this data: 33k-row bulk load in 75 ms, chapter read in 41 µs.

Search rides the same key. `EXPLAIN` shows an index scan with
`Index Cond: (translation = 'kjv')` and the `ILIKE` applied as a filter, so a
text search reads one translation's ~31k verses and does not slow down as more
translations are downloaded — 40–85 ms with all three cached, and *less* when
many verses match, because the result limit stops the scan early. Fast enough
that a search index would be complexity bought with latency no one waits on.

### Blue Letter Bible

[BLB](https://www.blueletterbible.org) supplies the original languages, and is
integrated two ways — deep links, and their official ScriptTagger script for
hover popups. Both degrade to nothing when offline; no iframes are involved.

The reader excludes its own scripture container from the tagger, so verses we
render keep our links, while references you type inside a note get popups. That
is the useful half of the behaviour without the double-linking.

---

## Roadmap

| Phase | Scope | Status |
| --- | --- | --- |
| 1 | Scripture cache, reader, verse hub, search | **done** |
| 2 | Notes, tags, cross-references, Strong's shortcodes | **done** |
| 3 | Notes search, combined scope, topical study pages | **done** |
| 4 | Sermon outline builder, Markdown/HTML export, AI drafting | **done** |
| 5 | File-based sync across machines, backups, settings | **done** |

[PLAN.md](PLAN.md) carries the full design, the schema, and the reasoning
behind each decision.

---

## Development

```sh
go build ./... && go vet ./... && go test ./...
```

The Blue Letter Bible slug check makes 66 live requests and is opt-in, so it
stays out of the normal test run:

```sh
PBSTUDY_LIVE_TESTS=1 go test ./blb/ -run BLBSlugs -v
```

Use a scratch data directory when experimenting, so your real notes are never
in the blast radius:

```sh
PBSTUDY_DATA_DIR=/tmp/pbstudy-dev go run . serve
```

### Built with

[rweb](https://github.com/rohanthewiz/rweb) ·
[element](https://github.com/rohanthewiz/element) ·
[go-styl](https://github.com/rohanthewiz/go-styl) ·
[bytdb](https://github.com/rohanthewiz/bytdb) ·
[serr](https://github.com/rohanthewiz/serr) ·
[logger](https://github.com/rohanthewiz/logger) ·
[goldmark](https://github.com/yuin/goldmark)

Scripture text from [getbible.net](https://getbible.net).

---

## License

[MIT](LICENSE) © 2026 Rohan Allison

The cached scripture texts (KJV, WEB, ASV) are public domain.
