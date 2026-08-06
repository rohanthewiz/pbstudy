package web

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/rweb"

	"github.com/rohanthewiz/pbstudy/syncer"
	"github.com/rohanthewiz/pbstudy/web/ui"
)

// handleSyncRun reconciles the study database with the sync directory and
// leaves the report where the settings page will find it.
//
// The work happens inline rather than in a goroutine. A pass over a personal
// study database is milliseconds, and a button that returns before it knows the
// answer would have to grow a progress mechanism to say anything useful — which
// is the one thing this page has to do (see syncer.Report).
func (s *Server) handleSyncRun(ctx rweb.Context) error {
	if s.sync == nil {
		return s.notFound(ctx, "Sync is off; no sync directory is configured.")
	}

	rep, err := s.sync.Run()
	outcome := syncOutcome{At: time.Now(), Report: rep}
	if err != nil {
		logger.LogErr(err, "sync run failed", "dir", s.sync.Dir())
		outcome.Problem = "The sync directory could not be reconciled: " + err.Error()
	}
	s.setLastSync(outcome)

	return ctx.Redirect(http.StatusSeeOther, "/settings")
}

// handleBackup writes a snapshot of the study database into the sync
// directory's backups folder.
//
// This is the only safe way to put the database itself somewhere a sync daemon
// can see it: Engine.Backup writes a self-consistent file, where copying the
// live one captures a torn tail (see store.BackupStudy).
func (s *Server) handleBackup(ctx rweb.Context) error {
	dest := s.cfg.SyncBackupsDir()
	if dest == "" {
		return s.notFound(ctx, "There is nowhere to write a backup; no sync directory is configured.")
	}

	outcome := syncOutcome{At: time.Now()}
	path, err := s.store.BackupStudy(dest)
	if err != nil {
		logger.LogErr(err, "backup failed", "dest", dest)
		outcome.Problem = "The snapshot could not be written: " + err.Error()
	} else {
		outcome.Backup = path
	}
	s.setLastSync(outcome)

	return ctx.Redirect(http.StatusSeeOther, "/settings")
}

// --- shared plumbing -------------------------------------------------------

func (s *Server) setLastSync(o syncOutcome) {
	s.lastSyncMu.Lock()
	defer s.lastSyncMu.Unlock()
	s.lastSync = o
}

func (s *Server) lastSyncOutcome() syncOutcome {
	s.lastSyncMu.RLock()
	defer s.lastSyncMu.RUnlock()
	return s.lastSync
}

// syncStatus assembles what the settings page shows about sync.
func (s *Server) syncStatus() ui.SyncStatus {
	st := ui.SyncStatus{
		Enabled: s.sync != nil,
		Dir:     s.cfg.SyncDir,
	}
	if !st.Enabled {
		return st
	}

	st.Backups = recentBackups(s.cfg.SyncBackupsDir(), 5)

	out := s.lastSyncOutcome()
	if out.At.IsZero() {
		return st
	}
	st.LastAt = out.At
	st.LastProblem = out.Problem
	st.LastBackup = out.Backup
	if len(out.Report.Entities) > 0 {
		st.LastHeadline = out.Report.Headline()
		st.LastLines = syncReportLines(out.Report)
	}
	return st
}

// recentBackups lists the newest snapshot files, most recent first.
//
// Named by timestamp (store.BackupStudy), so a lexical sort is a chronological
// one and no stat call is needed. Shown at all because a backup the user cannot
// see is one they will not trust — and restoring one is a manual, deliberate
// act, so the page's job is to say what exists and where.
func recentBackups(dir string, limit int) []ui.BackupFile {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // no backups taken yet is the usual reason
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".bytdb" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	if len(names) > limit {
		names = names[:limit]
	}

	out := make([]ui.BackupFile, 0, len(names))
	for _, name := range names {
		f := ui.BackupFile{Name: name}
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil {
			f.Size = info.Size()
		}
		out = append(out, f)
	}
	return out
}

// syncReportLines adapts a report for the view.
//
// The ui package renders; it does not reconcile. Flattening the report here
// keeps syncer out of the view layer entirely, which is what lets the report
// grow fields without every page that shows one having to care.
func syncReportLines(rep syncer.Report) []ui.SyncEntityLine {
	out := make([]ui.SyncEntityLine, 0, len(rep.Entities))
	for _, e := range rep.Entities {
		out = append(out, ui.SyncEntityLine{
			Label:    e.Label(),
			Summary:  e.Summary(),
			Problems: e.Problems,
			Notes:    e.Notes,
		})
	}
	return out
}
