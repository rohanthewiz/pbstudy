package syncer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/rohanthewiz/serr"

	"github.com/rohanthewiz/pbstudy/study"
)

// entityFolders are the subdirectories of the sync dir, one per entity, plus
// the backups folder store.BackupStudy writes into. Created up front so the
// directory is self-describing the moment it is configured — an empty tree
// tells the user where things will land, and a sync daemon has something to
// pick up before there is any data.
var entityFolders = []string{"notes", "tags", "xrefs", "sermons", "backups"}

// fileExt is the only extension the importer reads. Anything else in a folder
// (a daemon's conflict copy, a partially written .tmp, a stray README) is left
// alone rather than guessed at.
const fileExt = ".json"

// tmpExt marks a file being written. Writes are never made in place: a daemon
// watching the folder must never see a half-written record, so the content goes
// to <id>.json.tmp and is renamed over the real name, which is atomic within a
// filesystem.
const tmpExt = ".tmp"

func (s *Syncer) ensureDirs() error {
	for _, folder := range entityFolders {
		dir := filepath.Join(s.dir, folder)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return serr.Wrap(err, "cannot create sync folder", "dir", dir)
		}
	}
	return nil
}

func (s *Syncer) entityDir(folder string) string { return filepath.Join(s.dir, folder) }

// readRecords decodes every record in a folder.
//
// Three kinds of bad file are counted and reported rather than thrown:
//
//   - unreadable JSON — the daemon may still be delivering it, or a hand edit
//     broke it; either way the other three thousand files should still import.
//   - the wrong kind — a note file in the tags folder is a mistake worth
//     naming, not a record to force through the tag importer.
//   - a newer format version — see study.SyncVersion. Skipping is the only safe
//     answer: importing what we can parse would drop the fields we cannot, and
//     the export half of the run would then write that loss back to the file.
func readRecords[T study.Syncable](dir, kind string, rep *EntityReport) ([]T, error) {
	names, err := recordFiles(dir)
	if err != nil {
		return nil, err
	}

	out := make([]T, 0, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)

		body, err := os.ReadFile(path)
		if err != nil {
			rep.fail("cannot read " + name + ": " + err.Error())
			continue
		}

		var rec T
		if err := json.Unmarshal(body, &rec); err != nil {
			rep.fail(name + " is not readable as " + kind + " JSON")
			continue
		}
		if rec.SyncID() == "" {
			rep.fail(name + " has no id")
			continue
		}
		if k := rec.SyncKind(); k != "" && k != kind {
			rep.fail(name + " says it is a " + k + ", not a " + kind)
			continue
		}
		if v := rec.SyncVersion(); v > study.SyncVersion {
			rep.fail(name + " was written by a newer pbstudy (format " +
				strconv.Itoa(v) + "); leaving it alone")
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// readStamps reads just the id and updated_at of every record in a folder.
//
// The export pass only needs to know whether the file it would write is already
// current, so it decodes the two fields it compares rather than the whole
// record. Unreadable files are simply absent from the map, which makes the
// export overwrite them — a file that cannot be parsed is not one worth
// preserving, and the row it shadows is still here.
func readStamps(dir string) (map[string]time.Time, error) {
	names, err := recordFiles(dir)
	if err != nil {
		return nil, err
	}

	out := make(map[string]time.Time, len(names))
	for _, name := range names {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var head struct {
			ID        string    `json:"id"`
			UpdatedAt time.Time `json:"updatedAt"`
		}
		if err := json.Unmarshal(body, &head); err != nil || head.ID == "" {
			continue
		}
		out[head.ID] = head.UpdatedAt
	}
	return out, nil
}

// recordFiles lists the .json files in a folder, ignoring everything else
// (in-progress .tmp writes, backups, editor droppings) and any subdirectory.
func recordFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // a folder nobody has written to yet
		}
		return nil, serr.Wrap(err, "cannot list sync folder", "dir", dir)
	}

	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), fileExt) {
			continue
		}
		out = append(out, e.Name())
	}
	return out, nil
}

// writeRecord writes one record atomically, named for its id.
//
// Indented JSON with a trailing newline, deliberately: these files land in
// iCloud folders and git repositories, where a human reads them and a diff has
// to be legible. The size cost of the whitespace is irrelevant next to the
// scripture cache these files exist to stay out of.
func writeRecord(dir string, rec study.Syncable) error {
	body, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return serr.Wrap(err, "cannot encode record", "id", rec.SyncID())
	}
	body = append(body, '\n')

	name := recordFileName(rec.SyncID())
	if name == "" {
		return serr.New("record id is not usable as a file name", "id", rec.SyncID())
	}

	final := filepath.Join(dir, name)
	tmp := final + tmpExt

	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return serr.Wrap(err, "cannot write sync file", "path", tmp)
	}
	if err := os.Rename(tmp, final); err != nil {
		// Leaving a .tmp behind would be picked up by nothing (the importer
		// only reads .json) but would accumulate, so clean up after ourselves.
		_ = os.Remove(tmp)
		return serr.Wrap(err, "cannot replace sync file", "path", final)
	}
	return nil
}

// recordFileName builds the file name for an id, refusing anything that is not
// a plain identifier.
//
// Ids are UUIDs minted by this app, so this never fires in practice — but the
// id also arrives from files written elsewhere, and an id of "../../.ssh/id_rsa"
// must not become a path. Rejecting outright beats sanitizing: a record whose
// id is not an id is not a record.
func recordFileName(id string) string {
	if id == "" || len(id) > 128 {
		return ""
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_':
		default:
			return ""
		}
	}
	return id + fileExt
}
