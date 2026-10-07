package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// ErrLocked reports that another process (or another opener in this process)
// holds the state-home lock.
var ErrLocked = errors.New("state home lock is held by another writer")

// Lock is an flock-based exclusive lock on the state home. The owner keeps
// it for its whole lifetime; offline mutations (the CLI contract's permitted
// writes, Stage 06) take it briefly around a read/write. A second holder —
// online or offline — is refused.
//
// mu serializes every operation bound to this lifetime lock, so concurrent
// owner goroutines cannot interleave a read-modify-write across the two
// stores even though each is a separate file.
type Lock struct {
	file *os.File
	path string
	mu   sync.Mutex
}

// AcquireLock takes the exclusive lock on path, creating the file when
// necessary with mode 0600. It never waits: the caller decides whether to
// retry or surface the typed refusal, so a CLI never hangs on a live owner.
//
// The state-home directory is created privately first, so a first write into
// a missing nested XDG state home never fails on a vanished parent.
//
// flock semantics give per-open-description exclusion, which means even two
// openers in the same process conflict — exactly what the concurrent-writer
// acceptance tests need.
func AcquireLock(path string) (*Lock, error) {
	if err := EnsurePrivateDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("flock %s: %w", path, err)
	}
	return &Lock{file: f, path: path}, nil
}

// Release drops the lock and closes the descriptor.
func (l *Lock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}

// Owned reports whether the lock is still held.
func (l *Lock) Owned() bool { return l != nil && l.file != nil }
