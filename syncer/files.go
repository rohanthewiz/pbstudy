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
//
// The extension alone does not make a file a record, though: a conflict copy
// usually keeps it. See strayCopy for the second half of the rule.
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
func readRecords[T study.Syncable](dir, kind string, rep problemSink) ([]T, error) {
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
		if msg := strayCopy(name, rec.SyncID()); msg != "" {
			rep.fail(msg)
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
		// Only the file named for the id speaks for the record. A conflict
		// copy carries the same id with some other stamp, and letting it into
		// this map would make the export compare the row against the wrong
		// file: a newer stray would hide a canonical file that needs
		// rewriting. Skipped silently here because the import pass (and
		// compaction) already name it through readRecords.
		if strayCopy(name, head.ID) != "" {
			continue
		}
		out[head.ID] = head.UpdatedAt
	}
	return out, nil
}

// strayCopy reports a .json file that is not the file of the record it
// carries, as the message to show the user; it returns "" for a file that is
// correctly named.
//
// # What this is for
//
// Every record lives in exactly one file, <id>.json. A sync daemon breaks that
// when the same record was changed on two machines before either saw the
// other's version: it keeps one side under the real name and parks the other
// beside it, still ending in .json and still carrying the same id.
//
//	notes/3f2a….json                                   the record
//	notes/3f2a….sync-conflict-20261118-120000-ABC.json  Syncthing's parked copy
//	notes/3f2a… 2.json                                  iCloud's parked copy
//
// # Why a stray is never imported, not even when it is newer
//
// Treating it as a second vote for the record looks harmless (last writer
// wins, so let the newest file win) and is not:
//
//   - It outlives the record. After the note is deleted and compaction has
//     removed the row and <id>.json, the stray is the only file left with
//     that id. Imported, a live stray brings a deleted note back, and a
//     tombstoned one is re-imported and re-purged on every compaction.
//   - The daemon already chose which side keeps the name. Whatever is in
//     <id>.json is what every other machine will converge on, so taking the
//     stray here would make this machine the odd one out.
//
// So the rule is the plain one: a file not named for its id is not a record.
// It is reported on every pass, which is deliberate. The message is the only
// sign that an edit lost a conflict, the file is the only place that text
// still exists, and the report should keep saying so until someone has looked
// at it and removed the file. Nothing here deletes it.
//
// An id that cannot be a file name at all (recordFileName returns "") is not
// this function's business; writeRecord and removeRecord refuse those.
func strayCopy(name, id string) string {
	canonical := recordFileName(id)
	if canonical == "" || name == canonical {
		return ""
	}
	return name + " is a second copy of " + canonical +
		" (a sync conflict?). It was not imported; compare it with the record, then delete it"
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

// removeRecord deletes one record's file, and reports whether there was one to
// delete.
//
// Only compaction calls this. It routes the id through recordFileName for the
// same reason writeRecord does — an id that arrived in a file is not
// necessarily an id — and it treats an already-absent file as success: another
// machine's compaction may have removed it through the same shared folder
// moments ago, and that is the outcome we wanted anyway.
func removeRecord(dir, id string) (bool, error) {
	name := recordFileName(id)
	if name == "" {
		return false, serr.New("record id is not usable as a file name", "id", id)
	}

	path := filepath.Join(dir, name)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, serr.Wrap(err, "cannot remove sync file", "path", path)
	}
	return true, nil
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
