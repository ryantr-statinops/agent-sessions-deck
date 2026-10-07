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
	"path/filepath"
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
	if err := mkdirPrivateRecursive(path); err != nil {
		return err
	}
	return nil
}

// mkdirPrivateRecursive creates one missing directory at a time with mode
// 0700. A directory is chmodded only when this call created it, so a
// directory that another process created — including one that appears after
// the caller's initial stat — is never chmodded by us. An existing directory
// keeps its mode: the user may have their own reasoning, and the contract
// only promises "new state/config directories are private".
func mkdirPrivateRecursive(path string) error {
	err := os.Mkdir(path, 0o700)
	if err == nil {
		// We created it: honor the promised mode even under an unusual umask.
		return os.Chmod(path, 0o700)
	}
	if errors.Is(err, os.ErrExist) {
		info, serr := os.Lstat(path)
		if serr != nil {
			return serr
		}
		if !info.IsDir() {
			return fmt.Errorf("%s exists and is not a directory", path)
		}
		return nil
	}
	if errors.Is(err, os.ErrNotExist) {
		parent := filepath.Dir(path)
		if parent == path {
			return err
		}
		if err := mkdirPrivateRecursive(parent); err != nil {
			return err
		}
		return mkdirPrivateRecursive(path)
	}
	return err
}
