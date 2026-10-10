package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// SessionsSchemaVersion is the durable envelope version for sessions.json.
const SessionsSchemaVersion = 1

// sessionsEnvelope wraps the Stage 02 full-snapshot payload with its own
// versioned revision (ADR 0003: one aggregate, one revision, no cross-file
// transactions).
type sessionsEnvelope struct {
	SchemaVersion int             `json:"schema_version"`
	Revision      uint64          `json:"revision"`
	Sessions      json.RawMessage `json:"sessions"`
}

// CorruptStateError preserves a corrupt store file: it is never reset to
// empty, never deleted, and the caller gets file path plus recovery guidance.
type CorruptStateError struct {
	File string
	Err  error
}

func (e *CorruptStateError) Error() string {
	return fmt.Sprintf("%s: the stored state is corrupt or unreadable (%v); the file is preserved as-is, repair it by hand or remove it and re-migrate; it was not reset", e.File, e.Err)
}

func (e *CorruptStateError) Unwrap() error { return e.Err }

// FutureSchemaError refuses an envelope written by a newer ASD and fails
// safe: the data stays untouched, the error names a concrete next step.
type FutureSchemaError struct {
	File          string
	SchemaVersion int
	SupportedMax  int
}

func (e *FutureSchemaError) Error() string {
	return fmt.Sprintf("%s: schema_version %d is newer than this build supports (max %d); upgrade ASD or restore a backup; the file was left untouched", e.File, e.SchemaVersion, e.SupportedMax)
}

// RevisionConflictError refuses a commit whose expected revision is stale.
type RevisionConflictError struct {
	File     string
	Expected uint64
	Actual   uint64
}

func (e *RevisionConflictError) Error() string {
	return fmt.Sprintf("%s: revision conflict: expected %d, current %d; re-read the store and retry", e.File, e.Expected, e.Actual)
}

// SessionsStore implements session.Store over sessions.json. One call
// atomically replaces the whole snapshot and its revision; Delete enforces
// the T20 active-session refusal via the session domain.
type SessionsStore struct {
	path     string
	lockPath string
	lock     *Lock
}

// NewSessionsStore binds the store to sessions.json and the state-home lock.
// It runs in offline mode: every operation takes the short state-home lock,
// so it is refused while an owner holds the lock.
func NewSessionsStore(path, lockPath string) *SessionsStore {
	return &SessionsStore{path: path, lockPath: lockPath}
}

// NewOwnerSessionsStore binds the store to an already-held lifetime lock.
// Owner operations reuse the held lock (re-flocking it would fail against
// itself) and are serialized through the lock's mutex. Load/Commit/Delete
// must work while the owner holds it.
func NewOwnerSessionsStore(path string, lock *Lock) *SessionsStore {
	return &SessionsStore{path: path, lock: lock}
}

// withLock runs one owner-or-offline operation under the right mutual
// exclusion: the shared lifetime-lock mutex while an owner store is bound,
// or a short flock acquisition otherwise.
func (s *SessionsStore) withLock(fn func() error) error {
	if s.lock != nil {
		s.lock.mu.Lock()
		defer s.lock.mu.Unlock()
		if !s.lock.ownedLocked() {
			return ErrLockReleased
		}
		return fn()
	}
	lock, err := AcquireLock(s.lockPath)
	if err != nil {
		return err
	}
	defer lock.Release()
	return fn()
}

// Load returns the stored sessions and revision. Absent file means an empty
// snapshot at revision 0. Corrupt or future-schema images fail safe. An
// offline Load takes the short lock and is refused while an owner is live;
// the owner store reads under the lifetime lock.
func (s *SessionsStore) Load(ctx context.Context) ([]session.Session, uint64, error) {
	var sessions []session.Session
	var revision uint64
	err := s.withLock(func() error {
		data, err := os.ReadFile(s.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				sessions = []session.Session{}
				revision = 0
				return nil
			}
			return &CorruptStateError{File: s.path, Err: err}
		}
		sessions, revision, err = decodeSessionsEnvelope(s.path, data)
		return err
	})
	if err != nil {
		return nil, 0, err
	}
	return sessions, revision, nil
}

// decodeSessionsEnvelope strict-decodes the envelope and the payload.
func decodeSessionsEnvelope(path string, data []byte) ([]session.Session, uint64, error) {
	var env sessionsEnvelope
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		return nil, 0, &CorruptStateError{File: path, Err: err}
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, 0, &CorruptStateError{File: path, Err: errors.New("trailing data after the sessions envelope")}
	}
	if env.SchemaVersion != SessionsSchemaVersion {
		if env.SchemaVersion > SessionsSchemaVersion {
			return nil, 0, &FutureSchemaError{File: path, SchemaVersion: env.SchemaVersion, SupportedMax: SessionsSchemaVersion}
		}
		return nil, 0, &CorruptStateError{File: path, Err: fmt.Errorf("unsupported schema_version %d", env.SchemaVersion)}
	}
	if len(env.Sessions) == 0 || bytes.Equal(bytes.TrimSpace(env.Sessions), []byte("null")) {
		return nil, 0, &CorruptStateError{File: path, Err: errors.New("sessions payload must be an array, not missing or null")}
	}
	sessions, err := session.DecodeSessions(bytes.NewReader(env.Sessions))
	if err != nil {
		return nil, 0, &CorruptStateError{File: path, Err: err}
	}
	return sessions, env.Revision, nil
}

// Commit writes the full snapshot atomically with the next revision, taking
// the state-home lock briefly around check-and-write so a concurrent writer
// is refused (offline mutation rule).
func (s *SessionsStore) Commit(ctx context.Context, sessions []session.Session, expectedRevision uint64) (uint64, error) {
	err := s.withLock(func() error {
		_, actualRevision, err := s.loadUnlocked(ctx)
		if err != nil {
			return err
		}
		if actualRevision != expectedRevision {
			return &RevisionConflictError{File: s.path, Expected: expectedRevision, Actual: actualRevision}
		}
		_, err = s.writeRevision(sessions, expectedRevision+1)
		return err
	})
	if err != nil {
		return 0, err
	}
	return expectedRevision + 1, nil
}

func (s *SessionsStore) loadUnlocked(ctx context.Context) ([]session.Session, uint64, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []session.Session{}, 0, nil
		}
		return nil, 0, &CorruptStateError{File: s.path, Err: err}
	}
	return decodeSessionsEnvelope(s.path, data)
}

// Delete removes one session, refusing while the session is active (T20) and
// refusing stale revisions. It is one atomic replace of the remaining
// snapshot.
func (s *SessionsStore) Delete(ctx context.Context, id session.ID, expectedRevision uint64) (uint64, error) {
	var next uint64
	err := s.withLock(func() error {
		sessions, actualRevision, err := s.loadUnlocked(ctx)
		if err != nil {
			return err
		}
		if actualRevision != expectedRevision {
			return &RevisionConflictError{File: s.path, Expected: expectedRevision, Actual: actualRevision}
		}
		idx := -1
		for i := range sessions {
			if sessions[i].ID == id {
				idx = i
				break
			}
		}
		if idx < 0 {
			return session.NewError(session.CodeNotFound, string(id), "session is not in the store", "list the sessions to see valid ids")
		}
		if err := sessions[idx].EnsureDeletable(); err != nil {
			return err
		}
		sessions = append(sessions[:idx], sessions[idx+1:]...)
		next, err = s.writeRevision(sessions, expectedRevision+1)
		return err
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}

// writeRevision serializes the snapshot into a new full envelope and
// atomically replaces the file.
func (s *SessionsStore) writeRevision(sessions []session.Session, revision uint64) (uint64, error) {
	payload := &bytes.Buffer{}
	if err := session.EncodeSessions(payload, sessions); err != nil {
		return 0, err
	}
	env := sessionsEnvelope{SchemaVersion: SessionsSchemaVersion, Revision: revision, Sessions: payload.Bytes()}
	out, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := writeFileAtomic(filepath.Dir(s.path), s.path, 0o600, out); err != nil {
		return 0, err
	}
	return revision, nil
}
