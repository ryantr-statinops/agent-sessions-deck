package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// MigrateLegacySessions imports the pre-XDG session store at legacyPath
// into the versioned XDG store at destPath, but only when destPath does
// not exist. The legacy source is preserved, a same-directory backup is
// written, and a corrupt source fails safe — it is never reset and the
// valid destination is never merged over or replaced.
//
// The legacy format is the Stage 02 wire: one JSON array of sessions,
// strict-decoded with session.DecodeSessions so an unknown field or a
// half-record fails loudly instead of importing half a store.
func MigrateLegacySessions(ctx context.Context, legacyPath, destPath, lockPath string) (bool, error) {
	if _, err := os.Stat(destPath); err == nil {
		// The valid XDG destination is authoritative; never merge or overwrite.
		return false, nil
	}
	data, err := os.ReadFile(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("cannot read legacy sessions %s: %w", legacyPath, err)
	}
	sessions, err := session.DecodeSessions(bytes.NewReader(data))
	if err != nil {
		return false, &CorruptStateError{File: legacyPath, Err: err}
	}
	lock, err := AcquireLock(lockPath)
	if err != nil {
		return false, err
	}
	defer lock.Release()
	// Re-check the destination under the lock: a concurrent first start must
	// not double-import.
	if _, err := os.Stat(destPath); err == nil {
		return false, nil
	}
	backup := legacyPath + ".backup"
	if err := copyFile(legacyPath, backup); err != nil {
		return false, fmt.Errorf("legacy backup %s: %w", backup, err)
	}
	payload := &bytes.Buffer{}
	if err := session.EncodeSessions(payload, sessions); err != nil {
		return false, err
	}
	env := sessionsEnvelope{SchemaVersion: SessionsSchemaVersion, Revision: 1, Sessions: payload.Bytes()}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return false, err
	}
	if err := writeFileAtomic(filepath.Dir(destPath), destPath, 0o600, out); err != nil {
		return false, err
	}
	return true, nil
}

// copyFile preserves the byte content and the permission bits of src.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm())
}
