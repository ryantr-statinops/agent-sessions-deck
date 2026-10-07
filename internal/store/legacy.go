package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	if present, err := destinationPresent(destPath); err != nil {
		return false, err
	} else if present {
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
	if present, err := destinationPresent(destPath); err != nil {
		return false, err
	} else if present {
		return false, nil
	}
	return migrateLocked(sessions, legacyPath, destPath)
}

// MigrateLegacySessionsWithLock imports the legacy store into destPath using
// an already-held owner lock instead of acquiring a short one; the caller
// keeps ownership of the lock and its mutex. Destination and backup rules
// are identical to MigrateLegacySessions.
func MigrateLegacySessionsWithLock(ctx context.Context, legacyPath, destPath string, lock *Lock) (bool, error) {
	if present, err := destinationPresent(destPath); err != nil {
		return false, err
	} else if present {
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
	lock.mu.Lock()
	defer lock.mu.Unlock()
	if !lock.ownedLocked() {
		return false, ErrLockReleased
	}
	if present, err := destinationPresent(destPath); err != nil {
		return false, err
	} else if present {
		return false, nil
	}
	return migrateLocked(sessions, legacyPath, destPath)
}

func migrateLocked(sessions []session.Session, legacyPath, destPath string) (bool, error) {
	if _, err := backupLegacy(legacyPath); err != nil {
		return false, fmt.Errorf("legacy backup: %w", err)
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

// destinationPresent reports whether a non-importable destination exists.
// Lstat — not Stat — decides: a dangling symlink counts as present so the
// destination is never silently replaced. Other stat errors propagate.
func destinationPresent(destPath string) (bool, error) {
	_, err := os.Lstat(destPath)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("cannot stat destination %s: %w", destPath, err)
}

// backupLegacy preserves the source bytes in a private 0600 backup next to
// the source. An existing backup is reused only when it is byte-identical;
// a different existing backup is never overwritten, so a new backup lands
// at the first free ".backup.N" name.
func backupLegacy(legacyPath string) (string, error) {
	src, err := os.ReadFile(legacyPath)
	if err != nil {
		return "", err
	}
	for i := 0; ; i++ {
		candidate := legacyPath + ".backup"
		if i > 0 {
			candidate = fmt.Sprintf("%s.backup.%d", legacyPath, i)
		}
		if _, err := os.Lstat(candidate); err == nil {
			existing, err := os.ReadFile(candidate)
			if err != nil {
				return "", err
			}
			if bytes.Equal(existing, src) {
				return candidate, nil
			}
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err := writeFileAtomic(filepath.Dir(candidate), candidate, 0o600, src); err != nil {
			return "", err
		}
		return candidate, nil
	}
}
