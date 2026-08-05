# pbstudy

A personal Bible study application that runs on your own machine.

Track notes and correlations between scriptures, study topics, and assemble
sermons and teachings from what you have collected. Scripture is cached
locally, so study works with the network off.

> **Status: Phase 1 of 5.** The scripture cache, chapter reader, verse hub and
> search are working. Notes, tags, cross-references, the sermon builder and
> file-sync are still to come — see [Roadmap](#roadmap) and [PLAN.md](PLAN.md).

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
- `/read/:translation/:book/:chapter` — the reader (← / → page through chapters)
- `/verse/:book/:chapter/:verse` — the verse hub: every translation at once,
  plus links into Blue Letter Bible's interlinear and lexicon
- `/search?q=` — press `/` from anywhere to jump to the search box

The search box does double duty. Type a phrase and it scans the scripture text;
type a reference and it takes you straight there. `John 3:16`, `1 Jn 2:1`,
`II Corinthians 5:17`, `Ps 23`, `Jude 3` and `Gen. 1:1` all parse.

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
a torn tail. Two consequences, both enforced rather than documented:

- **Never point a sync daemon at the data directory.** Use
  `pbstudy backup`, which takes an internal snapshot and writes a
  self-consistent file that is safe to drop in iCloud or Syncthing.
- **Only one pbstudy process per data directory.** Startup takes an advisory
  `flock`; a second process is refused and told which PID holds the directory.
  The kernel releases the lock on exit, including a crash, so there is no stale
  lock file to clean up.

### Scripture storage

Verses are keyed on `(translation, book_num, chapter, verse)` as a composite
primary key. bytdb stores rows in primary-key order in a single key space, so
reading a chapter is one bounded range scan that arrives already sorted by
verse — no secondary index, and no sort step.

Measured on this data: 33k-row bulk load in 75 ms, chapter read in 41 µs, a
full-text `ILIKE` scan of a translation in 241 µs. Fast enough that a search
index would be complexity in exchange for latency no one can perceive.

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
| 2 | Notes, tags, cross-references, Strong's shortcodes | next |
| 3 | Notes search, combined scope, topical study pages | planned |
| 4 | Sermon outline builder, Markdown/HTML export, AI drafting | planned |
| 5 | File-based sync across machines, backups, settings | planned |

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
[logger](https://github.com/rohanthewiz/logger)

Scripture text from [getbible.net](https://getbible.net).

---

## License

[MIT](LICENSE) © 2026 Rohan Allison

The cached scripture texts (KJV, WEB, ASV) are public domain.
