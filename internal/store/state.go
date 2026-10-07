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
	"time"
)

// StateSchemaVersion is the durable envelope version for state.json.
const StateSchemaVersion = 1

// maxRecentWorkspaces bounds the retained recent-workspace list so the
// state file stays small.
const maxRecentWorkspaces = 32

// RecentWorkspace is one recent-workspace entry: the canonical path plus
// when ASD last used it. It is metadata, never a launch decision by itself.
type RecentWorkspace struct {
	Path     string    `json:"path"`
	LastUsed time.Time `json:"last_used"`
}

// StatePayload is the independent state.json document: its own schema
// version and revision, recent workspaces only. No transaction ever spans
// sessions.json and state.json (ADR 0003).
type StatePayload struct {
	SchemaVersion    int               `json:"schema_version"`
	Revision         uint64            `json:"revision"`
	RecentWorkspaces []RecentWorkspace `json:"recent_workspaces"`
}

// StateStore owns state.json with its own independent revision discipline:
// recent-workspace updates never touch sessions.json revision.
type StateStore struct {
	path     string
	lockPath string
	lock     *Lock
}

// NewStateStore binds the store to state.json and the state-home lock. It
// runs in offline mode: every operation takes the short state-home lock.
func NewStateStore(path, lockPath string) *StateStore {
	return &StateStore{path: path, lockPath: lockPath}
}

// NewOwnerStateStore binds the store to an already-held lifetime lock; its
// operations serialize through the lock mutex and reuse the held flock.
func NewOwnerStateStore(path string, lock *Lock) *StateStore {
	return &StateStore{path: path, lock: lock}
}

// withLock runs one owner-or-offline operation under the right mutual
// exclusion.
func (s *StateStore) withLock(fn func() error) error {
	if s.lock != nil {
		s.lock.mu.Lock()
		defer s.lock.mu.Unlock()
		return fn()
	}
	lock, err := AcquireLock(s.lockPath)
	if err != nil {
		return err
	}
	defer lock.Release()
	return fn()
}

// Load returns the current state and revision; absent means empty state at
// revision 0. Offline Load takes the short lock and is refused while an
// owner is live; the owner store reads under the lifetime lock.
func (s *StateStore) Load(ctx context.Context) (StatePayload, error) {
	var state StatePayload
	err := s.withLock(func() error {
		data, err := os.ReadFile(s.path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				state = StatePayload{SchemaVersion: StateSchemaVersion, Revision: 0, RecentWorkspaces: []RecentWorkspace{}}
				return nil
			}
			return &CorruptStateError{File: s.path, Err: err}
		}
		state, err = decodeState(s.path, data)
		return err
	})
	if err != nil {
		return StatePayload{}, err
	}
	return state, nil
}

func (s *StateStore) loadUnlocked(ctx context.Context) (StatePayload, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StatePayload{SchemaVersion: StateSchemaVersion, Revision: 0, RecentWorkspaces: []RecentWorkspace{}}, nil
		}
		return StatePayload{}, &CorruptStateError{File: s.path, Err: err}
	}
	return decodeState(s.path, data)
}

// decodeState strict-decodes the state document.
func decodeState(path string, data []byte) (StatePayload, error) {
	var wire struct {
		SchemaVersion    int             `json:"schema_version"`
		Revision         uint64          `json:"revision"`
		RecentWorkspaces json.RawMessage `json:"recent_workspaces"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&wire); err != nil {
		return StatePayload{}, &CorruptStateError{File: path, Err: err}
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return StatePayload{}, &CorruptStateError{File: path, Err: errors.New("trailing data after the state envelope")}
	}
	if wire.SchemaVersion > StateSchemaVersion {
		return StatePayload{}, &FutureSchemaError{File: path, SchemaVersion: wire.SchemaVersion, SupportedMax: StateSchemaVersion}
	}
	if wire.SchemaVersion != StateSchemaVersion {
		return StatePayload{}, &CorruptStateError{File: path, Err: fmt.Errorf("unsupported schema_version %d", wire.SchemaVersion)}
	}
	if len(wire.RecentWorkspaces) == 0 || bytes.Equal(bytes.TrimSpace(wire.RecentWorkspaces), []byte("null")) {
		return StatePayload{}, &CorruptStateError{File: path, Err: errors.New("recent_workspaces payload must be an array, not missing or null")}
	}
	var state StatePayload
	sub := json.NewDecoder(bytes.NewReader(wire.RecentWorkspaces))
	sub.DisallowUnknownFields()
	state.RecentWorkspaces = []RecentWorkspace{}
	if err := sub.Decode(&state.RecentWorkspaces); err != nil {
		return StatePayload{}, &CorruptStateError{File: path, Err: err}
	}
	if err := sub.Decode(&extra); err != io.EOF {
		return StatePayload{}, &CorruptStateError{File: path, Err: errors.New("trailing data inside recent_workspaces")}
	}
	state.SchemaVersion = wire.SchemaVersion
	state.Revision = wire.Revision
	for i := range state.RecentWorkspaces {
		if state.RecentWorkspaces[i].Path == "" {
			return StatePayload{}, &CorruptStateError{File: path, Err: fmt.Errorf("recent_workspaces[%d].path is empty", i)}
		}
	}
	return state, nil
}

// Commit atomically replaces the state document when the expected revision
// matches; otherwise RevisionConflictError. It takes the state-home lock
// briefly like the sessions store.
func (s *StateStore) Commit(ctx context.Context, state StatePayload, expectedRevision uint64) (uint64, error) {
	err := s.withLock(func() error {
		current, err := s.loadUnlocked(ctx)
		if err != nil {
			return err
		}
		if current.Revision != expectedRevision {
			return &RevisionConflictError{File: s.path, Expected: expectedRevision, Actual: current.Revision}
		}
		state.SchemaVersion = StateSchemaVersion
		state.Revision = expectedRevision + 1
		if state.RecentWorkspaces == nil {
			state.RecentWorkspaces = []RecentWorkspace{}
		}
		out, err := json.MarshalIndent(state, "", "  ")
		if err != nil {
			return err
		}
		return writeFileAtomic(filepath.Dir(s.path), s.path, 0o600, out)
	})
	if err != nil {
		return 0, err
	}
	return expectedRevision + 1, nil
}

// AddRecent records a workspace as most-recently used: it moves it to the
// front, dedupes, bounds the list, and is the only recent API mutation.
func (s *StateStore) AddRecent(ctx context.Context, path string, at time.Time) (uint64, error) {
	var next uint64
	err := s.withLock(func() error {
		if path == "" {
			return errors.New("recent workspace path must not be empty")
		}
		current, err := s.loadUnlocked(ctx)
		if err != nil {
			return err
		}
		next2 := []RecentWorkspace{{Path: path, LastUsed: at.UTC()}}
		for _, recent := range current.RecentWorkspaces {
			if recent.Path == path {
				continue
			}
			next2 = append(next2, recent)
		}
		if len(next2) > maxRecentWorkspaces {
			next2 = next2[:maxRecentWorkspaces]
		}
		current.RecentWorkspaces = next2
		current.SchemaVersion = StateSchemaVersion
		current.Revision++
		if current.RecentWorkspaces == nil {
			current.RecentWorkspaces = []RecentWorkspace{}
		}
		out, err := json.MarshalIndent(current, "", "  ")
		if err != nil {
			return err
		}
		if err := writeFileAtomic(filepath.Dir(s.path), s.path, 0o600, out); err != nil {
			return err
		}
		next = current.Revision
		return nil
	})
	if err != nil {
		return 0, err
	}
	return next, nil
}
