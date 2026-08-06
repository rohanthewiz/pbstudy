// Package cfg resolves runtime configuration for pbstudy.
//
// Configuration is deliberately thin: this is a single-user, local-first app,
// so there is no config file to parse or reload. Everything comes from
// environment variables with sane defaults, which keeps startup total and
// makes the app trivially scriptable (PBSTUDY_DATA_DIR=/tmp/x pbstudy serve).
package cfg

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rohanthewiz/serr"
)

// Env var names. Collected here so the settings page can document them
// without duplicating string literals.
const (
	EnvDataDir      = "PBSTUDY_DATA_DIR"
	EnvSyncDir      = "PBSTUDY_SYNC_DIR"
	EnvPort         = "PBSTUDY_PORT"
	EnvAnthropicKey = "ANTHROPIC_API_KEY"
)

// Default listen port. 8000 matches the house convention for local Go apps.
const defaultPort = 8000

// Config is the resolved runtime configuration, built once at startup.
type Config struct {
	// DataDir holds the live database files. Never point this at a
	// sync-daemon-watched directory: bytdb does no file locking, and a
	// daemon copying a file mid-append yields a torn tail.
	DataDir string

	// SyncDir is where JSON entity exports and backup snapshots land. This
	// one IS meant to live in iCloud/Syncthing/a git checkout. Empty means
	// sync is disabled.
	SyncDir string

	// Port is the local HTTP listen port.
	Port int

	// AnthropicKey enables the optional "Draft with AI" feature. Held in
	// memory only — never written to a database, an export, or a backup.
	AnthropicKey string
}

// BibleDBPath is the scripture cache: large, rebuildable via
// `pbstudy download`, and never synced.
func (c Config) BibleDBPath() string { return filepath.Join(c.DataDir, "bible.bytdb") }

// StudyDBPath is the precious data — notes, tags, cross-references, sermons.
// Synced indirectly, via JSON exports and Backup() snapshots, never by
// copying this file while the process holds it open.
func (c Config) StudyDBPath() string { return filepath.Join(c.DataDir, "study.bytdb") }

// SyncBackupsDir is where snapshot files land inside the sync directory. A
// snapshot is a self-consistent copy taken by the engine, so unlike the live
// database it is safe for a sync daemon to pick up.
func (c Config) SyncBackupsDir() string {
	if c.SyncDir == "" {
		return ""
	}
	return filepath.Join(c.SyncDir, "backups")
}

// AIEnabled reports whether the optional AI draft feature should be offered.
// The UI hides the button entirely when this is false, rather than showing a
// control that fails on click.
func (c Config) AIEnabled() bool { return c.AnthropicKey != "" }

// SyncEnabled reports whether a sync directory has been configured.
func (c Config) SyncEnabled() bool { return c.SyncDir != "" }

// Load resolves configuration from the environment and ensures DataDir
// exists. It returns an error rather than falling back to a temp directory:
// silently writing the user's study notes somewhere unexpected is worse than
// refusing to start.
func Load() (Config, error) {
	c := Config{
		SyncDir:      os.Getenv(EnvSyncDir),
		AnthropicKey: os.Getenv(EnvAnthropicKey),
		Port:         defaultPort,
	}

	dataDir := os.Getenv(EnvDataDir)
	if dataDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return c, serr.Wrap(err, "cannot determine home directory; set "+EnvDataDir)
		}
		dataDir = filepath.Join(home, ".pbstudy")
	}
	// Absolute paths keep log lines and the settings page unambiguous
	// regardless of where the binary was launched from.
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return c, serr.Wrap(err, "cannot resolve data dir", "dir", dataDir)
	}
	c.DataDir = abs

	if err := os.MkdirAll(c.DataDir, 0o700); err != nil {
		return c, serr.Wrap(err, "cannot create data dir", "dir", c.DataDir)
	}

	if p := os.Getenv(EnvPort); p != "" {
		port, err := strconv.Atoi(p)
		if err != nil || port < 1 || port > 65535 {
			return c, serr.New("invalid port", EnvPort, p)
		}
		c.Port = port
	}

	if c.SyncDir != "" {
		abs, err := filepath.Abs(c.SyncDir)
		if err != nil {
			return c, serr.Wrap(err, "cannot resolve sync dir", "dir", c.SyncDir)
		}
		c.SyncDir = abs

		if err := checkDirsDisjoint(c.DataDir, c.SyncDir); err != nil {
			return c, err
		}
	}

	return c, nil
}

// checkDirsDisjoint refuses a configuration where the live databases would end
// up inside the synced directory (or the reverse).
//
// This is the one misconfiguration that can destroy the data this app exists to
// protect. bytdb does no file locking and its files are append-only, so a sync
// daemon that copies study.bytdb mid-append captures a torn tail — and the
// machine on the other end then imports a database that is subtly, silently
// wrong. Nesting either way puts the live files under a daemon's watch, so the
// check is symmetric and startup fails loudly rather than degrading.
//
// The safe arrangement is two separate trees:
//
//	~/.pbstudy/            live: bible.bytdb, study.bytdb   (never synced)
//	~/iCloud/pbstudy/      synced: notes/, tags/, backups/  (never opened)
func checkDirsDisjoint(dataDir, syncDir string) error {
	data := filepath.Clean(dataDir)
	sync := filepath.Clean(syncDir)

	if data == sync {
		return serr.New("sync dir must not be the data dir",
			EnvDataDir, data, EnvSyncDir, sync,
			"why", "a sync daemon copying a live bytdb file captures a torn tail")
	}
	// Compare with a trailing separator so /a/pbstudy-sync is not read as
	// living inside /a/pbstudy.
	if strings.HasPrefix(sync+string(filepath.Separator), data+string(filepath.Separator)) {
		return serr.New("sync dir must not be inside the data dir",
			EnvDataDir, data, EnvSyncDir, sync)
	}
	if strings.HasPrefix(data+string(filepath.Separator), sync+string(filepath.Separator)) {
		return serr.New("data dir must not be inside the sync dir",
			EnvDataDir, data, EnvSyncDir, sync,
			"why", "the live databases would be handed to the sync daemon")
	}
	return nil
}

// Address is the listen address in the ":port" form rweb expects. The empty
// host binds all interfaces, which is what makes the app reachable from a
// phone on the same LAN — a deliberate choice for a personal study tool.
func (c Config) Address() string { return ":" + strconv.Itoa(c.Port) }
