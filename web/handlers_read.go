package web

import (
	"net/http"
	"strconv"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"

	"github.com/rohanthewiz/pbstudy/bible"
	"github.com/rohanthewiz/pbstudy/study"
	"github.com/rohanthewiz/pbstudy/web/ui"
)

// defaultTranslation picks the translation to use when a request does not
// name one: the first downloaded translation in preference order, falling
// back to KJV so the UI still renders (with a "nothing downloaded" notice)
// on a fresh install.
func (s *Server) defaultTranslation() string {
	downloaded, err := bible.Downloaded(s.store.Bible)
	if err != nil || len(downloaded) == 0 {
		return bible.DefaultTranslation
	}
	return downloaded[0].Abbrev
}

// handleDashboard renders the canon index and cache status.
func (s *Server) handleDashboard(ctx rweb.Context) error {
	translation := s.defaultTranslation()

	downloaded, err := bible.Downloaded(s.store.Bible)
	if err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot read scripture cache", "The scripture database could not be queried.", err)
	}

	count := 0
	if len(downloaded) > 0 {
		// A failed count is not worth failing the page over — show zero.
		count, _ = bible.CountVerses(s.store.Bible, translation)
	}

	// Same reasoning for the study counts: they are a status line, not the
	// page's reason for existing.
	noteCount, _ := study.CountNotes(s.store.Study)
	xrefCount, _ := study.CountXrefs(s.store.Study)
	sermonCount, _ := study.CountSermons(s.store.Study)

	return s.render(ctx, ui.Page{
		Title:       "",
		Active:      ui.NavRead,
		Translation: translation,
		Body: ui.Dashboard{
			Translation:  translation,
			Translations: downloaded,
			VerseCount:   count,
			NoteCount:    noteCount,
			XrefCount:    xrefCount,
			SermonCount:  sermonCount,
		},
	})
}

// handleReadDefault sends a bare /read to a canonical chapter URL. John 1 is
// the landing point rather than Genesis 1 — this is a study tool, and the
// Gospels are where study most often starts.
func (s *Server) handleReadDefault(ctx rweb.Context) error {
	return ctx.Redirect(http.StatusFound, ui.ReadURL(s.defaultTranslation(), 43, 1))
}

// handleReadGo receives the book/chapter picker's GET submission and
// redirects to the canonical reader URL. The picker posts here rather than
// building the URL client-side so it works with JavaScript disabled.
func (s *Server) handleReadGo(ctx rweb.Context) error {
	req := ctx.Request()

	bookNum, err := strconv.Atoi(req.QueryParam("book"))
	if err != nil {
		bookNum = 43
	}
	chapter, err := strconv.Atoi(req.QueryParam("chapter"))
	if err != nil || chapter < 1 {
		chapter = 1
	}
	translation := req.QueryParam("translation")
	if translation == "" {
		translation = s.defaultTranslation()
	}

	// Clamp rather than error: the picker's own options are always valid,
	// so an out-of-range value came from a hand-edited URL and landing on
	// the nearest real chapter beats an error page.
	if bk, err := bible.ByNum(bookNum); err == nil && chapter > bk.ChapterCount {
		chapter = bk.ChapterCount
	}

	return ctx.Redirect(http.StatusFound, ui.ReadURL(translation, bookNum, chapter))
}

// handleRead renders one chapter.
func (s *Server) handleRead(ctx rweb.Context) error {
	req := ctx.Request()

	// Validate the translation against the known set before it goes anywhere.
	// It arrives as a URL path segment and then flows into hrefs, a hidden
	// form field and the ScriptTagger config; checking it once here is worth
	// more than escaping it correctly in four places.
	translation := req.Param("translation")
	if !bible.IsKnownTranslation(translation) {
		return s.notFound(ctx, "There is no translation called "+
			strconv.Quote(translation)+". Try kjv, web or asv.")
	}

	bookNum, err := strconv.Atoi(req.Param("book"))
	if err != nil {
		return s.notFound(ctx, "Book must be a number from 1 to 66.")
	}
	bk, err := bible.ByNum(bookNum)
	if err != nil {
		return s.notFound(ctx, "There is no book number "+req.Param("book")+".")
	}

	chapter, err := strconv.Atoi(req.Param("chapter"))
	if err != nil || chapter < 1 || chapter > bk.ChapterCount {
		return s.notFound(ctx, bk.Name+" has "+strconv.Itoa(bk.ChapterCount)+" chapters.")
	}

	verses, err := bible.Chapter(s.store.Bible, translation, bookNum, chapter)
	if err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot read chapter", "The scripture database could not be queried.", err)
	}

	downloaded, err := bible.Downloaded(s.store.Bible)
	if err != nil {
		downloaded = nil // the switcher simply hides; the chapter still renders
	}

	// The user's own annotations. A failure here is degraded, not fatal: the
	// chapter is still readable without its indicator dots, and refusing to
	// show scripture because the notes database hiccuped would be the wrong
	// trade in a reading app.
	marks, err := study.ChapterMarks(s.store.Study, bookNum, chapter)
	if err != nil {
		logger.LogErr(err, "cannot load chapter marks",
			"book", bk.Name, "chapter", strconv.Itoa(chapter))
		marks = nil
	}
	chapterNotes, err := study.NotesForChapter(s.store.Study, bookNum, chapter)
	if err != nil {
		chapterNotes = nil
	}

	return s.render(ctx, ui.Page{
		Title:       bk.Name + " " + strconv.Itoa(chapter),
		Active:      ui.NavRead,
		Translation: translation,
		Body: ui.Reader{
			Translation:  translation,
			Translations: downloaded,
			Book:         bk,
			Chapter:      chapter,
			Verses:       verses,
			Prev:         s.prevChapter(translation, bk, chapter),
			Next:         s.nextChapter(translation, bk, chapter),
			Marks:        marks,
			ChapterNotes: chapterNotes,
		},
	})
}

// prevChapter resolves the previous chapter, crossing the book boundary
// backwards into the last chapter of the preceding book. Genesis 1 has no
// predecessor and yields a zero link, which the view renders as no button.
func (s *Server) prevChapter(translation string, bk *bible.Book, chapter int) ui.ChapterLink {
	if chapter > 1 {
		return ui.ChapterLink{
			URL:   ui.ReadURL(translation, bk.Num, chapter-1),
			Label: bk.Name + " " + strconv.Itoa(chapter-1),
		}
	}
	prev, err := bible.ByNum(bk.Num - 1)
	if err != nil {
		return ui.ChapterLink{}
	}
	return ui.ChapterLink{
		URL:   ui.ReadURL(translation, prev.Num, prev.ChapterCount),
		Label: prev.Name + " " + strconv.Itoa(prev.ChapterCount),
	}
}

// nextChapter is prevChapter's mirror, crossing forward into chapter 1 of the
// following book. Revelation 22 yields a zero link.
func (s *Server) nextChapter(translation string, bk *bible.Book, chapter int) ui.ChapterLink {
	if chapter < bk.ChapterCount {
		return ui.ChapterLink{
			URL:   ui.ReadURL(translation, bk.Num, chapter+1),
			Label: bk.Name + " " + strconv.Itoa(chapter+1),
		}
	}
	next, err := bible.ByNum(bk.Num + 1)
	if err != nil {
		return ui.ChapterLink{}
	}
	return ui.ChapterLink{
		URL:   ui.ReadURL(translation, next.Num, 1),
		Label: next.Name + " 1",
	}
}

// handleVerse renders the verse hub: one verse across every downloaded
// translation, plus links out to Blue Letter Bible.
func (s *Server) handleVerse(ctx rweb.Context) error {
	req := ctx.Request()

	bookNum, err := strconv.Atoi(req.Param("book"))
	if err != nil {
		return s.notFound(ctx, "Book must be a number from 1 to 66.")
	}
	bk, err := bible.ByNum(bookNum)
	if err != nil {
		return s.notFound(ctx, "There is no book number "+req.Param("book")+".")
	}

	chapter, err := strconv.Atoi(req.Param("chapter"))
	if err != nil || chapter < 1 || chapter > bk.ChapterCount {
		return s.notFound(ctx, bk.Name+" has "+strconv.Itoa(bk.ChapterCount)+" chapters.")
	}

	verse, err := strconv.Atoi(req.Param("verse"))
	if err != nil || verse < 1 {
		return s.notFound(ctx, "Verse must be a positive number.")
	}

	// The translation to return to. Honour ?t= when the reader linked here,
	// otherwise fall back to whatever is downloaded. An unrecognized value
	// falls back rather than 404s: the hub shows every translation anyway, so
	// a bad ?t= costs only the "back to chapter" link's version.
	readTranslation := req.QueryParam("t")
	if !bible.IsKnownTranslation(readTranslation) {
		readTranslation = s.defaultTranslation()
	}

	return s.renderVerseHub(ctx, bk, chapter, verse, readTranslation, "")
}

// renderVerseHub assembles and renders the verse hub.
//
// Split out from handleVerse because two paths land on this page: a plain GET,
// and a rejected quick-add that has to re-render the page with a complaint
// instead of redirecting (see verseHubWithProblem). Sharing the assembly is
// what keeps the second path from becoming a thinner, subtly different hub.
func (s *Server) renderVerseHub(ctx rweb.Context, bk *bible.Book,
	chapter, verse int, readTranslation, problem string) error {

	renderings, err := bible.AllTranslationsOf(s.store.Bible, bk.Num, chapter, verse)
	if err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot read verse", "The scripture database could not be queried.", err)
	}

	// Everything below is the user's own data. Each read degrades on its own
	// rather than taking the page down — the verse itself is the reason to be
	// here, and it has already loaded.
	notes, err := study.NotesForVerse(s.store.Study, bk.Num, chapter, verse)
	if err != nil {
		logger.LogErr(err, "cannot load notes for verse", "book", bk.Name,
			"chapter", strconv.Itoa(chapter), "verse", strconv.Itoa(verse))
		notes = nil
	}
	xrefsOut, err := study.XrefsFrom(s.store.Study, bk.Num, chapter, verse)
	if err != nil {
		xrefsOut = nil
	}
	xrefsIn, err := study.XrefsTo(s.store.Study, bk.Num, chapter, verse)
	if err != nil {
		xrefsIn = nil
	}
	knownTags, _ := study.ListTags(s.store.Study)

	ref := bible.Ref{BookNum: bk.Num, Chapter: chapter, VerseStart: verse, VerseEnd: verse}

	return s.render(ctx, ui.Page{
		Title:       ref.String(),
		Active:      ui.NavRead,
		Translation: readTranslation,
		Body: ui.VerseHub{
			Book:            bk,
			Chapter:         chapter,
			Verse:           verse,
			Renderings:      renderings,
			ReadTranslation: readTranslation,
			Notes:           notes,
			XrefsOut:        xrefsOut,
			XrefsIn:         xrefsIn,
			KnownTags:       knownTags,
			Problem:         problem,
		},
	})
}

// handleSettings shows resolved configuration and cache state.
func (s *Server) handleSettings(ctx rweb.Context) error {
	downloaded, err := bible.Downloaded(s.store.Bible)
	if err != nil {
		return s.renderError(ctx, http.StatusInternalServerError,
			"Cannot read scripture cache", "The scripture database could not be queried.", err)
	}

	counts := make(map[string]int, len(downloaded))
	for _, t := range downloaded {
		n, err := bible.CountVerses(s.store.Bible, t.Abbrev)
		if err == nil {
			counts[t.Abbrev] = n
		}
	}

	return s.render(ctx, ui.Page{
		Title:       "Settings",
		Active:      ui.NavSettings,
		Translation: s.defaultTranslation(),
		Body: ui.Settings{
			DataDir:      s.cfg.DataDir,
			BibleDBPath:  s.cfg.BibleDBPath(),
			StudyDBPath:  s.cfg.StudyDBPath(),
			SyncDir:      s.cfg.SyncDir,
			Port:         s.cfg.Port,
			AIEnabled:    s.cfg.AIEnabled(),
			Translations: downloaded,
			VerseCounts:  counts,
			Sync:         s.syncStatus(),
		},
	})
}
