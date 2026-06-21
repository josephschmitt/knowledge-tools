package vault

import (
	"errors"
	"os"
	"path/filepath"
)

// ErrLocked is returned by AcquireLock when another vault job already holds the lock. The bash
// scripts treated this as a clean exit 0 ("another job is running"), not a failure — callers
// should do the same.
var ErrLocked = errors.New("another vault job holds the lock")

// Lock is a held per-instance vault lock. Close releases it (closing the file drops the OS lock).
//
// This serializes compile / synthesize / resolve within one vault instance: they all edit wiki/
// and commit, so they must not run concurrently. The lock is keyed by the lock path (which the
// config derives from KNOWLEDGE_INSTANCE), so different instances use different files and DO run
// concurrently — exactly the multi-vault isolation vault-lib.sh provided.
//
// Unlike the bash version there is no flock-vs-mkdir split: flock(2) exists on both Linux and
// macOS, and Go reaches it the same way on each (see lock_unix.go), so the macOS mkdir fallback
// is gone.
type Lock struct {
	f *os.File
}

// AcquireLock takes the lock at path without blocking, or returns ErrLocked if it's held.
func AcquireLock(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := flockNB(f); err != nil {
		f.Close()
		if errors.Is(err, errWouldBlock) {
			return nil, ErrLocked
		}
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Release drops the lock.
func (l *Lock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	// Closing the descriptor releases the OS lock.
	return l.f.Close()
}
