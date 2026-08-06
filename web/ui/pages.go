package ui

import (
	"time"

	"github.com/rohanthewiz/element"

	"github.com/rohanthewiz/pbstudy/bible"
)

// Dashboard is the landing page: a whole-canon index plus the state of the
// scripture cache.
//
// A book grid rather than a "continue reading" card, deliberately — study
// starts from a passage far more often than it resumes where it left off,
// and the grid doubles as the app's site map.
type Dashboard struct {
	Translation  string
	Translations []bible.Translation
	VerseCount   int
	// NoteCount, XrefCount and SermonCount summarise the study database — the
	// half of the storage that cannot be re-downloaded, so it is worth seeing
	// on arrival.
	NoteCount   int
	XrefCount   int
	SermonCount int
}

func (d Dashboard) Render(b *element.Builder) (x any) {
	b.H1Class("page-title").T("Study")

	if len(d.Translations) == 0 {
		b.DivClass("notice").R(
			b.Strong().T("No scripture downloaded yet. "),
			b.T("Run "),
			b.Code().T("pbstudy download kjv"),
			b.T(" (or "),
			b.Code().T("pbstudy download all"),
			b.T(" for KJV, WEB and ASV) to fill the local cache. "),
			b.T("Everything after that works offline."),
		)
	} else {
		b.PClass("page-sub").R(
			b.F("%s · %d verses cached", upper(d.Translation), d.VerseCount),
			b.Wrap(func() {
				// Only mention the study database once there is something in
				// it. "0 notes" on a fresh install is a reproach, not a
				// status line.
				if d.NoteCount == 0 && d.XrefCount == 0 && d.SermonCount == 0 {
					return
				}
				b.T(" · ")
				b.A("href", "/notes").T(Plural(d.NoteCount, "note"))
				b.T(" · ")
				b.T(Plural(d.XrefCount, "cross-reference"))
				b.Wrap(func() {
					// Sermons join the line only once one exists, for the same
					// reason the whole line is conditional.
					if d.SermonCount == 0 {
						return
					}
					b.T(" · ")
					b.A("href", "/sermons").T(Plural(d.SermonCount, "sermon"))
				})
			}),
		)
	}

	d.renderBooks(b, bible.OldTestament, "Old Testament")
	d.renderBooks(b, bible.NewTestament, "New Testament")
	return
}

func (d Dashboard) renderBooks(b *element.Builder, testament, label string) (x any) {
	b.DivClass("testament-label").T(label)
	b.DivClass("book-grid").R(
		element.ForEach(bible.Books, func(bk bible.Book) {
			if bk.Testament != testament {
				return
			}
			b.A("href", ReadURL(d.Translation, bk.Num, 1),
				"title", bk.Name+" — "+itoa(bk.ChapterCount)+" chapters").
				T(bk.Name)
		}),
	)
	return
}

// Settings shows resolved configuration and the state of the caches.
//
// Configuration itself is read-only — it comes from environment variables, so
// the honest thing is to show what was resolved and name the variable that
// changes it, rather than offer a form that cannot persist anything. The two
// buttons on this page are not configuration: they are actions against the
// state it describes.
type Settings struct {
	DataDir      string
	BibleDBPath  string
	StudyDBPath  string
	SyncDir      string
	Port         int
	AIEnabled    bool
	Translations []bible.Translation
	VerseCounts  map[string]int
	Sync         SyncStatus
}

// SyncStatus is everything this page says about sync: where it points, what
// the last run did, and what snapshots exist.
type SyncStatus struct {
	Enabled bool
	Dir     string

	// LastAt is when the last manual run or backup finished; zero means
	// neither has happened in this process.
	LastAt       time.Time
	LastHeadline string
	LastLines    []SyncEntityLine
	LastProblem  string
	LastBackup   string

	Backups []BackupFile
}

// SyncEntityLine is one entity's row in the report.
type SyncEntityLine struct {
	Label    string
	Summary  string
	Problems []string
	Notes    []string
}

// BackupFile is one snapshot in the backups folder.
type BackupFile struct {
	Name string
	Size int64
}

func (s Settings) Render(b *element.Builder) (x any) {
	b.H1Class("page-title").T("Settings")
	b.PClass("page-sub").T("Configuration is read from the environment at startup.")

	b.DivClass("card").R(
		b.H2().T("Paths"),
		b.Dl("class", "kv").R(
			kv(b, "Data directory", s.DataDir),
			kv(b, "Scripture cache", s.BibleDBPath),
			kv(b, "Study database", s.StudyDBPath),
			kv(b, "Sync directory", orNone(s.SyncDir)),
			kv(b, "Listen port", itoa(s.Port)),
		),
	)

	b.DivClass("card").R(
		b.H2().T("Scripture cache"),
		b.Wrap(func() {
			if len(s.Translations) == 0 {
				b.PClass("empty").T("Nothing downloaded yet.")
				return
			}
			b.Dl("class", "kv").R(
				element.ForEach(s.Translations, func(t bible.Translation) {
					kv(b, upper(t.Abbrev)+" — "+t.Name, itoa(s.VerseCounts[t.Abbrev])+" verses")
				}),
			)
		}),
		b.PClass("page-sub").R(
			b.T("Refresh with "),
			b.Code().T("pbstudy download <kjv|web|asv|all>"),
			b.T(". The scripture cache is rebuildable and is never synced."),
		),
	)

	s.renderSync(b)

	b.DivClass("card").R(
		b.H2().T("AI drafting"),
		b.Wrap(func() {
			if s.AIEnabled {
				b.P().R(
					b.T("Enabled — "),
					b.Code().T("ANTHROPIC_API_KEY"),
					b.T(" is set. The key is held in memory only; it is never written to a "),
					b.T("database, an export, or a backup."),
				)
			} else {
				b.P().R(
					b.T("Disabled. Set "),
					b.Code().T("ANTHROPIC_API_KEY"),
					b.T(" to enable sermon drafting. Everything else works without it."),
				)
			}
		}),
	)
	return
}

// renderSync is the sync card: where files go, what the last run did, and the
// two buttons that act on it.
//
// Both buttons are plain POST forms with no JavaScript. Sync and backup are
// exactly the kind of operation someone reaches for when something has already
// gone wrong, and the last thing that should stand between a worried user and
// their data is a script that has to load first.
func (s Settings) renderSync(b *element.Builder) (x any) {
	b.DivClass("card").R(
		b.H2().T("Sync"),

		b.Wrap(func() {
			if !s.Sync.Enabled {
				b.P().R(
					b.T("Off. Set "),
					b.Code().T("PBSTUDY_SYNC_DIR"),
					b.T(" to a folder your sync service watches (iCloud, Syncthing, a git "),
					b.T("checkout) and restart. Notes, tags, cross-references and sermons are "),
					b.T("written there as one JSON file each; scripture never is."),
				)
				b.PClass("page-sub").R(
					b.T("The sync folder must sit outside the data directory. The live "),
					b.T("databases are append-only and unlocked, so a sync daemon that copies "),
					b.T("one mid-write captures a torn file — pbstudy refuses to start if the "),
					b.T("two overlap."),
				)
				return
			}

			b.Dl("class", "kv").R(
				kv(b, "Sync directory", s.Sync.Dir),
				kv(b, "Written there", "notes · tags · xrefs · sermons · backups"),
			)

			b.DivClass("link-row").R(
				b.FormClass("inline-form", "method", "post", "action", "/sync/run").R(
					b.Button("class", "btn", "type", "submit").T("Sync now"),
				),
				b.FormClass("inline-form", "method", "post", "action", "/backup").R(
					b.Button("class", "btn", "type", "submit").T("Back up the study database"),
				),
			)

			b.PClass("page-sub").T(
				"A sync reads what arrived and writes what changed here; the newer of the " +
					"two wins, row by row. Edits are also exported automatically a couple of " +
					"seconds after you make them.")

			// Deliberately a sentence rather than a third button. Compaction
			// throws away the evidence of a delete and cannot be undone by
			// running it again, so it belongs behind a command that has to be
			// typed — with a retention window the user chose, on a machine they
			// know has not been away.
			b.PClass("page-sub").R(
				b.T("Deleted rows are kept as tombstones so the deletion can reach your "),
				b.T("other machines. To clear out ones old enough that every machine has "),
				b.T("seen them, run "),
				b.Code().T("pbstudy compact --dry-run"),
				b.T(" to see what would go, then "),
				b.Code().T("pbstudy compact"),
				b.T(" — with pbstudy stopped, on each machine."),
			)

			s.renderSyncResult(b)
			s.renderBackups(b)
		}),
	)
	return
}

// renderSyncResult shows the outcome of the last run or backup.
func (s Settings) renderSyncResult(b *element.Builder) (x any) {
	if s.Sync.LastAt.IsZero() {
		return
	}

	b.DivClass("sync-report").R(
		b.PClass("sync-when").T("Last action at "+s.Sync.LastAt.Format("15:04:05")),

		b.Wrap(func() {
			if s.Sync.LastProblem != "" {
				// The message is built from an error string, which may quote a
				// path or a file name that came off the disk — escaped like
				// everything else that did not originate in this binary.
				b.PClass("problem").T(esc(s.Sync.LastProblem))
			}
			if s.Sync.LastBackup != "" {
				b.P().R(
					b.T("Snapshot written to "),
					b.Code().T(esc(s.Sync.LastBackup)),
				)
			}
			if s.Sync.LastHeadline != "" {
				b.PClass("sync-headline").T(s.Sync.LastHeadline)
			}
		}),

		b.Wrap(func() {
			if len(s.Sync.LastLines) == 0 {
				return
			}
			b.UlClass("sync-lines").R(
				element.ForEach(s.Sync.LastLines, func(line SyncEntityLine) {
					b.Li().R(
						b.Strong().T(line.Label),
						b.T(" — "),
						b.T(line.Summary),
						b.Wrap(func() {
							if len(line.Notes) == 0 && len(line.Problems) == 0 {
								return
							}
							b.Ul().R(
								element.ForEach(line.Notes, func(n string) {
									b.Li().T(esc(n))
								}),
								// Problems name files from the sync folder,
								// which is to say names this app did not
								// choose. They are escaped for the same reason
								// note titles are.
								element.ForEach(line.Problems, func(p string) {
									b.LiClass("problem").T(esc(p))
								}),
							)
						}),
					)
				}),
			)
		}),
	)
	return
}

// renderBackups lists the most recent snapshots.
func (s Settings) renderBackups(b *element.Builder) (x any) {
	if len(s.Sync.Backups) == 0 {
		return
	}

	b.DivClass("sync-backups").R(
		b.H3().T("Recent snapshots"),
		b.UlClass("plain-list").R(
			element.ForEach(s.Sync.Backups, func(f BackupFile) {
				b.Li().R(
					b.Code().T(esc(f.Name)),
					b.SpanClass("muted").T(" · "+humanSize(f.Size)),
				)
			}),
		),
		b.PClass("page-sub").R(
			b.T("A snapshot is a whole copy of the study database, safe to hand to a sync "),
			b.T("service. To restore one, stop pbstudy, put the file in the data directory "),
			b.T("as "),
			b.Code().T("study.bytdb"),
			b.T(", and start it again."),
		),
	)
	return
}

// humanSize renders a byte count at the granularity a person reading a file
// list actually wants.
func humanSize(n int64) string {
	switch {
	case n >= 1<<20:
		return itoa(int(n/(1<<20))) + " MB"
	case n >= 1<<10:
		return itoa(int(n/(1<<10))) + " KB"
	default:
		return itoa(int(n)) + " bytes"
	}
}

func kv(b *element.Builder, key, value string) (x any) {
	b.Dt().T(key)
	b.Dd().T(value)
	return
}

func orNone(s string) string {
	if s == "" {
		return "(not configured)"
	}
	return s
}

// ErrorPage is the fallback rendering for 4xx/5xx responses. It stays inside
// the normal shell so the nav is still available — a dead end with no way
// back is the worst part of most error pages.
type ErrorPage struct {
	Status  int
	Heading string
	Detail  string
}

func (e ErrorPage) Render(b *element.Builder) (x any) {
	b.DivClass("error-page").R(
		b.H1Class("page-title").F("%d — %s", e.Status, e.Heading),
		b.Wrap(func() {
			if e.Detail != "" {
				b.PClass("page-sub").T(e.Detail)
			}
		}),
		b.PClass("link-row").R(
			b.A("class", "btn", "href", "/").T("Back to study"),
		),
	)
	return
}
