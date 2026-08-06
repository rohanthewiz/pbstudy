package ui

import (
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
// Read-only in Phase 1: configuration comes from environment variables, so
// the honest thing is to show what was resolved and name the variable that
// changes it, rather than offer a form that cannot persist anything.
type Settings struct {
	DataDir      string
	BibleDBPath  string
	StudyDBPath  string
	SyncDir      string
	Port         int
	AIEnabled    bool
	Translations []bible.Translation
	VerseCounts  map[string]int
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
