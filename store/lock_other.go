//go:build !unix

package store

// Fallback for platforms without flock (Windows, plan9, js). The databases are
// still opened normally; only the "one process per data directory" guarantee
// is missing, so running `serve` and `download` at the same time there is on
// the user.
//
// Kept as a compiling no-op rather than a build failure: pbstudy is a personal
// tool that should still build anywhere Go does.

type dirLock struct{}

func acquireDirLock(string) (*dirLock, error) { return &dirLock{}, nil }

func (l *dirLock) release() error { return nil }
