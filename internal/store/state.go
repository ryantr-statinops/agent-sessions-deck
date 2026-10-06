package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
}

// NewStateStore binds the store to state.json and the state-home lock.
func NewStateStore(path, lockPath string) *StateStore {
	return &StateStore{path: path, lockPath: lockPath}
}

// Load returns the current state and revision; absent means empty state at
// revision 0.
func (s *StateStore) Load(ctx context.Context) (StatePayload, error) {
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
	var state StatePayload
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&state); err != nil {
		return StatePayload{}, &CorruptStateError{File: path, Err: err}
	}
	if state.SchemaVersion > StateSchemaVersion {
		return StatePayload{}, &FutureSchemaError{File: path, SchemaVersion: state.SchemaVersion, SupportedMax: StateSchemaVersion}
	}
	if state.SchemaVersion != StateSchemaVersion {
		return StatePayload{}, &CorruptStateError{File: path, Err: fmt.Errorf("unsupported schema_version %d", state.SchemaVersion)}
	}
	if state.RecentWorkspaces == nil {
		state.RecentWorkspaces = []RecentWorkspace{}
	}
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
	lock, err := AcquireLock(s.lockPath)
	if err != nil {
		return 0, err
	}
	defer lock.Release()
	current, err := s.Load(ctx)
	if err != nil {
		return 0, err
	}
	if current.Revision != expectedRevision {
		return 0, &RevisionConflictError{File: s.path, Expected: expectedRevision, Actual: current.Revision}
	}
	state.SchemaVersion = StateSchemaVersion
	state.Revision = expectedRevision + 1
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := writeFileAtomic(filepath.Dir(s.path), s.path, 0o600, out); err != nil {
		return 0, err
	}
	return state.Revision, nil
}

// AddRecent records a workspace as most-recently used: it moves it to the
// front, dedupes, bounds the list, and is the only recent API mutation.
func (s *StateStore) AddRecent(ctx context.Context, path string, at time.Time) (uint64, error) {
	lock, err := AcquireLock(s.lockPath)
	if err != nil {
		return 0, err
	}
	defer lock.Release()
	current, err := s.Load(ctx)
	if err != nil {
		return 0, err
	}
	next := []RecentWorkspace{{Path: path, LastUsed: at}}
	for _, recent := range current.RecentWorkspaces {
		if recent.Path == path {
			continue
		}
		next = append(next, recent)
	}
	if len(next) > maxRecentWorkspaces {
		next = next[:maxRecentWorkspaces]
	}
	current.RecentWorkspaces = next
	current.SchemaVersion = StateSchemaVersion
	current.Revision++
	out, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := writeFileAtomic(filepath.Dir(s.path), s.path, 0o600, out); err != nil {
		return 0, err
	}
	return current.Revision, nil
}
