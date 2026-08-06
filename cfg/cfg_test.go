package cfg

import (
	"path/filepath"
	"testing"
)

// TestCheckDirsDisjoint guards the one misconfiguration that can destroy the
// data this app exists to protect: the live, unlocked, append-only databases
// ending up under a sync daemon's watch.
func TestCheckDirsDisjoint(t *testing.T) {
	cases := []struct {
		name       string
		data, sync string
		wantErr    bool
	}{
		{"separate trees", "/home/x/.pbstudy", "/home/x/iCloud/pbstudy", false},
		{"same dir", "/home/x/.pbstudy", "/home/x/.pbstudy", true},
		{"sync inside data", "/home/x/.pbstudy", "/home/x/.pbstudy/sync", true},
		{"data inside sync", "/home/x/iCloud/pb/data", "/home/x/iCloud/pb", true},
		// The check compares whole path segments, so a directory whose name
		// merely begins with the other's is not nested.
		{"shared prefix only", "/home/x/pbstudy", "/home/x/pbstudy-sync", false},
		{"trailing slash", "/home/x/.pbstudy/", "/home/x/sync/", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkDirsDisjoint(filepath.FromSlash(c.data), filepath.FromSlash(c.sync))
			if (err != nil) != c.wantErr {
				t.Errorf("checkDirsDisjoint(%q, %q) error = %v, wantErr %v",
					c.data, c.sync, err, c.wantErr)
			}
		})
	}
}

// TestSyncBackupsDir pins where snapshots land — the one place a database file
// is allowed inside the sync directory, because those are engine snapshots
// rather than copies of a file being appended to.
func TestSyncBackupsDir(t *testing.T) {
	if got := (Config{}).SyncBackupsDir(); got != "" {
		t.Errorf("with no sync dir, SyncBackupsDir() = %q, want empty", got)
	}
	want := filepath.Join("/tmp/sync", "backups")
	if got := (Config{SyncDir: "/tmp/sync"}).SyncBackupsDir(); got != want {
		t.Errorf("SyncBackupsDir() = %q, want %q", got, want)
	}
}
