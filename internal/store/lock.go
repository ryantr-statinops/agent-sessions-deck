package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// ErrLocked reports that another process (or another opener in this process)
// holds the state-home lock.
var ErrLocked = errors.New("state home lock is held by another writer")

// ErrLockReleased reports an operation bound to a lifetime lock that has
// already been released. Owner stores and migrations bound to the lock
// refuse every operation once it is released.
var ErrLockReleased = errors.New("state home lock has been released")

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

// WriteToken writes an owner instance fencing token into the locked file.
func (l *Lock) WriteToken(token string) error {
	if l == nil {
		return ErrLockReleased
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return ErrLockReleased
	}
	if err := l.file.Truncate(0); err != nil {
		return err
	}
	if _, err := l.file.Seek(0, 0); err != nil {
		return err
	}
	if _, err := l.file.WriteString(token); err != nil {
		return err
	}
	return l.file.Sync()
}

// ReadToken reads the owner instance fencing token from the locked file.
func (l *Lock) ReadToken() (string, error) {
	if l == nil {
		return "", ErrLockReleased
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return "", ErrLockReleased
	}
	data, err := os.ReadFile(l.path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// Release drops the lock and closes the descriptor. It serializes with
// every in-flight bound operation through the mutex, and clears the file
// handle so Owned reports false and later operations are refused. Release
// is idempotent.
func (l *Lock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return nil
}

// Owned reports whether the lock is still held.
func (l *Lock) Owned() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.file != nil
}

// ownedLocked reports liveness for callers that already hold the mutex.
func (l *Lock) ownedLocked() bool { return l != nil && l.file != nil }
