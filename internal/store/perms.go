// Package store owns the persistent JSON state of Agent Session Deck:
// sessions.json and state.json, their envelopes, revisions, atomic writes,
// the state-home lock and legacy migration. It imports the domain's durable
// wire form (session.EncodeSessions/DecodeSessions) and never defines a
// second serialization.
package store

import (
	"errors"
	"fmt"
	"os"
)

// EnsurePrivateDir creates the directory tree with mode 0700 and only ever
// chmods a directory it created itself. An existing directory keeps its
// mode: the user may have their own reasoning, and the contract only
// promises "new state/config directories are private".
func EnsurePrivateDir(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", path)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	// MkdirAll applies the mode only to the pieces it created; make the final
	// directory private even under a permissive umask.
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return nil
}

// EnsurePrivateFile creates an empty file with mode 0600. An existing file
// keeps its mode — never chmod a user file destructively without notice —
// and an existing directory path fails loudly.
func EnsurePrivateFile(path string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	return f.Close()
}

// CreatePrivateFile creates a new file with mode 0600, failing if it exists.
func CreatePrivateFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

// MkdirTempRaw writes data to a temp file inside a 0700 directory; it is
// the first step of an atomic replace and keeps intermediate bytes private.
func MkdirTempFile(dir string) (*os.File, error) {
	if err := EnsurePrivateDir(dir); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, ".asd-tmp-*")
}
