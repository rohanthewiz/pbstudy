// Package web wires the HTTP surface: routes, handlers, and the rendering
// helpers that turn a ui component into a response.
package web

import (
	"io/fs"
	"net/http"
	"path"
	"time"

	"github.com/rohanthewiz/element"
	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"
	"github.com/rohanthewiz/rweb/middleware/stylus"
	"github.com/rohanthewiz/serr"

	"github.com/rohanthewiz/go-styl/stylserve"

	"github.com/rohanthewiz/pbstudy/assets"
	"github.com/rohanthewiz/pbstudy/cfg"
	"github.com/rohanthewiz/pbstudy/store"
	"github.com/rohanthewiz/pbstudy/web/ui"
)

// Server holds what every handler needs. Handlers are methods on it rather
// than closures over globals, so the whole app is constructible in a test
// with a temporary data directory.
type Server struct {
	cfg   cfg.Config
	store *store.Store
	rweb  *rweb.Server
	// drafts is the only mutable server-wide state: which sermons are being
	// drafted right now, so a reconnecting EventSource cannot start a second
	// generation for the same one. See drafts.go.
	drafts *draftJobs
	// aiEndpoint overrides the Anthropic API URL. Empty everywhere but the
	// end-to-end drafting test; see Server.draftClient.
	aiEndpoint string
	// draftStall is how long a draft event waits for the browser before the
	// generator concludes it has gone. Per-server rather than a package
	// variable so a test can shorten its own server's without racing another
	// server's in-flight goroutines. See defaultDraftStall.
	draftStall time.Duration
}

// New builds the server and registers all routes.
func New(conf cfg.Config, st *store.Store) (*Server, error) {
	s := &Server{
		cfg:   conf,
		store: st,
		rweb: rweb.NewServer(rweb.ServerOptions{
			Address: conf.Address(),
			Verbose: true,
		}),
		drafts:     newDraftJobs(),
		draftStall: defaultDraftStall,
	}

	s.rweb.Use(rweb.RequestInfo)

	if err := s.registerRoutes(); err != nil {
		return nil, err
	}
	return s, nil
}

// Run starts listening. It blocks until the server stops.
func (s *Server) Run() error {
	logger.Info("pbstudy listening", "address", s.cfg.Address(),
		"dataDir", s.cfg.DataDir)
	if err := s.rweb.Run(); err != nil {
		return serr.Wrap(err, "server stopped")
	}
	return nil
}

func (s *Server) registerRoutes() error {
	// --- static assets ----------------------------------------------------
	// Stylus sources compile on first request and are then served from an
	// ETag-validated cache, so the second load of a page is a 304.
	styles, err := assets.Styles()
	if err != nil {
		return err
	}
	s.rweb.Get("/css/*path", stylus.Handler(stylserve.Options{FS: styles}))

	js, err := assets.JS()
	if err != nil {
		return err
	}
	s.rweb.Get("/js/*path", s.serveJS(js))

	// --- pages ------------------------------------------------------------
	s.rweb.Get("/", s.handleDashboard)

	// /read with no location lands on the default; /read/go is where the
	// book/chapter picker posts. Both redirect to a canonical
	// /read/:translation/:book/:chapter URL so every reader state is
	// bookmarkable and shareable.
	s.rweb.Get("/read", s.handleReadDefault)
	s.rweb.Get("/read/go", s.handleReadGo)
	s.rweb.Get("/read/:translation/:book/:chapter", s.handleRead)

	s.rweb.Get("/verse/:book/:chapter/:verse", s.handleVerse)

	s.rweb.Get("/search", s.handleSearch)
	s.rweb.Get("/settings", s.handleSettings)

	// --- notes ------------------------------------------------------------
	// Every mutation is a POST that ends in a redirect (303), so a refresh
	// after saving re-renders the note rather than re-submitting the form.
	// /notes/new is registered before /notes/:id; rweb resolves the static
	// segment first, so "new" is never read as an id.
	s.rweb.Get("/notes", s.handleNotes)
	s.rweb.Get("/notes/new", s.handleNoteNew)
	s.rweb.Post("/notes", s.handleNoteCreate)
	s.rweb.Get("/notes/:id", s.handleNoteShow)
	s.rweb.Get("/notes/:id/edit", s.handleNoteEdit)
	s.rweb.Post("/notes/:id", s.handleNoteUpdate)
	s.rweb.Post("/notes/:id/delete", s.handleNoteDelete)

	// --- tags -------------------------------------------------------------
	// No create route: tags come into being by being typed into a note.
	s.rweb.Get("/tags", s.handleTags)
	s.rweb.Get("/tags/:id", s.handleTagShow)
	s.rweb.Post("/tags/:id/descrip", s.handleTagDescrip)
	s.rweb.Post("/tags/:id/delete", s.handleTagDelete)

	// --- cross-references -------------------------------------------------
	// No index page: a cross-reference is only meaningful at one of its ends,
	// so it is created and removed from the verse hub it belongs to.
	s.rweb.Post("/xrefs", s.handleXrefCreate)
	s.rweb.Post("/xrefs/:id/delete", s.handleXrefDelete)

	// --- sermons ----------------------------------------------------------
	// Outline sections are addressed by their own id rather than their
	// position, so a button clicked on a page that has since been reordered
	// still moves the row it was rendered against — see study.Section.ID.
	s.rweb.Get("/sermons", s.handleSermons)
	s.rweb.Post("/sermons", s.handleSermonCreate)
	s.rweb.Get("/sermons/:id", s.handleSermonShow)
	s.rweb.Post("/sermons/:id", s.handleSermonRename)
	s.rweb.Post("/sermons/:id/delete", s.handleSermonDelete)

	s.rweb.Post("/sermons/:id/sections", s.handleSectionAdd)
	s.rweb.Post("/sermons/:id/sections/:sectionId/move", s.handleSectionMove)
	s.rweb.Post("/sermons/:id/sections/:sectionId/delete", s.handleSectionDelete)

	s.rweb.Get("/sermons/:id/export.md", s.handleSermonExportMD)
	s.rweb.Get("/sermons/:id/export.html", s.handleSermonExportHTML)

	// Drafting is two requests on purpose: the POST validates and redirects,
	// and the generation starts when the browser opens the stream. One
	// request, one goroutine, one channel — so no event is produced before
	// there is a reader for it. See handleSermonDraftStream.
	s.rweb.Post("/sermons/:id/draft", s.handleSermonDraft)
	s.rweb.Get("/sermons/:id/draft/stream", s.handleSermonDraftStream)

	return nil
}

// serveJS serves the embedded scripts.
//
// Hand-rolled rather than routed through a static-file middleware: there is
// exactly one script, it lives in the binary, and this way the path handling
// is explicit — the wildcard is cleaned and forced relative, so no request
// can escape the embedded root.
func (s *Server) serveJS(fsys fs.FS) rweb.Handler {
	return func(ctx rweb.Context) error {
		name := path.Clean("/" + ctx.Request().PathParam("path"))[1:]
		if name == "" || name == "." {
			return s.notFound(ctx, "no script named")
		}

		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return s.notFound(ctx, "no such script: "+name)
		}

		res := ctx.Response()
		res.SetHeader("Content-Type", "application/javascript; charset=utf-8")
		// The script is compiled into the binary, so its content cannot
		// change without a restart — but a long max-age would outlive the
		// binary across upgrades. Revalidation keeps it honest and costs
		// one conditional request.
		res.SetHeader("Cache-Control", "no-cache")
		return ctx.Bytes(body)
	}
}

// --- rendering helpers ----------------------------------------------------

// render writes a full page. The builder comes from element's pool because
// every page render allocates one and they are short-lived.
func (s *Server) render(ctx rweb.Context, page ui.Page) error {
	b := element.AcquireBuilder()
	defer element.ReleaseBuilder(b)

	page.Render(b)
	return ctx.WriteHTMLBytes(b.Bytes())
}

// renderError writes an error page at the given status. The error is logged
// with its full serr context; the page shows only the human-readable detail,
// since this is a local app but stack detail still belongs in the log.
func (s *Server) renderError(ctx rweb.Context, status int, heading, detail string, err error) error {
	if err != nil {
		logger.LogErr(err, "request failed", "status", status, "path", ctx.Request().Path())
	}

	ctx.SetStatus(status)
	return s.render(ctx, ui.Page{
		Title: heading,
		Body: ui.ErrorPage{
			Status:  status,
			Heading: heading,
			Detail:  detail,
		},
	})
}

// notFound is the common 404 path.
func (s *Server) notFound(ctx rweb.Context, detail string) error {
	return s.renderError(ctx, http.StatusNotFound, "Not found", detail, nil)
}

// The comingSoon placeholder handler that stood in for /sermons is gone: every
// nav destination now resolves to a real page.
