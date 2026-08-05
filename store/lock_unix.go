//go:build unix

package store

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/rohanthewiz/serr"
)

// lockFileName is the advisory lock guarding the whole data directory.
const lockFileName = "pbstudy.lock"

// dirLock is a held advisory lock on the data directory.
type dirLock struct {
	f *os.File
}

// acquireDirLock takes an exclusive advisory lock on dataDir.
//
// # Why this exists
//
// bytdb does no file locking of its own, and btypedb assumes it owns the
// database file. Two pbstudy processes on one data directory — a running
// `serve` plus a `download` or `backup` in another terminal — would each hold
// their own engine over the same bytes and interleave appends. The scripture
// cache could be rebuilt from that; study.bytdb could not.
//
// flock is used rather than a PID file because the kernel releases it when
// the process exits for any reason, including a crash or a kill -9. A stale
// PID file would need manual cleanup and would eventually train the user to
// delete it reflexively — exactly when it was telling the truth.
//
// The lock is advisory and per-directory, which is the right granularity: it
// is the pair of databases that must not be shared, not either file alone.
func acquireDirLock(dataDir string) (*dirLock, error) {
	path := filepath.Join(dataDir, lockFileName)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, serr.Wrap(err, "cannot open lock file", "path", path)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder := readLockHolder(f)
		_ = f.Close()
		return nil, serr.New(
			"another pbstudy process is using this data directory",
			"dataDir", dataDir, "heldBy", holder,
			"hint", "stop the running pbstudy, or use a different "+
				"PBSTUDY_DATA_DIR for this command")
	}

	// Record our PID so the next process can name who is holding the lock.
	// Best-effort: failing to write it costs a helpful message, nothing more.
	_ = f.Truncate(0)
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		_ = err // not worth failing startup over
	}

	return &dirLock{f: f}, nil
}

// release drops the lock. The file is left in place — an empty lock file is
// harmless, and removing it would race another process that has already
// opened it but not yet flocked it.
func (l *dirLock) release() error {
	if l == nil || l.f == nil {
		return nil
	}
	// Closing the descriptor releases the flock; do it explicitly first so
	// the intent is clear at the call site.
	if err := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN); err != nil {
		_ = l.f.Close()
		return serr.Wrap(err, "cannot release data directory lock")
	}
	if err := l.f.Close(); err != nil {
		return serr.Wrap(err, "cannot close lock file")
	}
	l.f = nil
	return nil
}

// readLockHolder reads the PID the lock holder recorded, for the error
// message. Returns "unknown" when the file is empty or unreadable.
func readLockHolder(f *os.File) string {
	buf := make([]byte, 32)
	n, _ := f.ReadAt(buf, 0)
	if n == 0 {
		return "unknown"
	}
	pid := string(buf[:n])
	for i, c := range pid {
		if c == '\n' || c == '\r' {
			pid = pid[:i]
			break
		}
	}
	if pid == "" {
		return "unknown"
	}
	return "pid " + pid
}
