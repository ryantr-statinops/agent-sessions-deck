package session

import (
	"sort"
	"sync"
	"time"
)

// Engine applies events to a set of sessions with per-session serialization.
//
// It is the Stage 02 answer to the concurrency rule in ADR 0003: one lifecycle
// transaction per session at a time, so concurrent exit, stop and restart
// callbacks are ordered instead of racing, and cross-session work stays free to
// proceed in parallel. It owns no I/O: effects are returned to the caller, which
// performs them through the ProcessRuntime port and reports the results back as
// events.
//
// Engine is safe for concurrent use.
type Engine struct {
	mu       sync.Mutex
	order    []ID
	sessions map[ID]*engineSession
}

type engineSession struct {
	// mu serializes the lifecycle of this one session.
	mu      sync.Mutex
	session Session
}

// NewEngine builds an engine over the given sessions.
func NewEngine(sessions ...Session) (*Engine, error) {
	e := &Engine{sessions: make(map[ID]*engineSession, len(sessions))}
	for _, s := range sessions {
		if err := e.Add(s); err != nil {
			return nil, err
		}
	}
	return e, nil
}

// Add registers a new session record.
func (e *Engine) Add(s Session) error {
	if err := s.Validate(); err != nil {
		return WrapError(CodeInvalidConfiguration, string(s.ID), "session failed validation",
			"fix the session record before registering it", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, exists := e.sessions[s.ID]; exists {
		return NewError(CodeConflict, string(s.ID), "session id is already registered", "use a distinct session id")
	}
	e.sessions[s.ID] = &engineSession{session: s.Clone()}
	e.order = append(e.order, s.ID)
	sort.Slice(e.order, func(i, j int) bool { return e.order[i] < e.order[j] })
	return nil
}

// Apply reduces one event for one session and stores the result.
//
// A rejected or dropped transition is returned as a typed outcome and leaves the
// stored session untouched; only NOT_FOUND is reported as a Go error, because
// there is no session to report about.
func (e *Engine) Apply(id ID, event Event) (Outcome, error) {
	entry := e.lookup(id)
	if entry == nil {
		return Outcome{}, NewError(CodeNotFound, string(id), "session is not registered", "list the sessions to see valid ids")
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()

	outcome := Reduce(entry.session, event)
	if err := outcome.Validate(); err != nil {
		return Outcome{}, WrapError(CodeCorruptState, string(id), "the reducer produced an invalid session",
			"this is a contract bug; do not persist the state", err)
	}
	if outcome.Applied {
		entry.session = outcome.Session.Clone()
	}
	return outcome, nil
}

// Get returns a copy of a session's current state.
func (e *Engine) Get(id ID) (Session, bool) {
	entry := e.lookup(id)
	if entry == nil {
		return Session{}, false
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()
	return entry.session.Clone(), true
}

// Rename changes a session's user-visible label at the given instant.
//
// A rename is metadata, not lifecycle: it touches no axis, opens no attempt and
// keeps the session id and the attempt history exactly as they were, so a
// historical record stays editable offline and every id a user already knows
// keeps working. The label is validated by the session's own rule, which is the
// only rule that decides what a name may be, and the caller supplies the instant
// so the edit is reproducible rather than clock-dependent.
func (e *Engine) Rename(id ID, name string, at time.Time) (Session, error) {
	entry := e.lookup(id)
	if entry == nil {
		return Session{}, NewError(CodeNotFound, string(id), "session is not registered", "list the sessions to see valid ids")
	}
	if at.IsZero() {
		return Session{}, NewError(CodeInvalidConfiguration, string(id), "the rename has no timestamp",
			"stamp the rename with the time it happened")
	}
	entry.mu.Lock()
	defer entry.mu.Unlock()

	if err := validateName(name); err != nil {
		return Session{}, NewError(CodeInvalidConfiguration, string(id), err.Error(),
			"use a name of at most 128 bytes with no leading or trailing whitespace and no control characters")
	}
	updated := entry.session.Clone()
	updated.Name = name
	updated.UpdatedAt = at.UTC()
	if err := updated.Validate(); err != nil {
		return Session{}, WrapError(CodeInvalidConfiguration, string(id), "the renamed session failed validation",
			"use a different name; asd will not rewrite the rest of the record", err)
	}
	entry.session = updated
	return entry.session.Clone(), nil
}

// Sessions returns copies of every session, ordered by id.
func (e *Engine) Sessions() []Session {
	e.mu.Lock()
	entries := make([]*engineSession, 0, len(e.order))
	for _, id := range e.order {
		entries = append(entries, e.sessions[id])
	}
	e.mu.Unlock()

	out := make([]Session, 0, len(entries))
	for _, entry := range entries {
		entry.mu.Lock()
		out = append(out, entry.session.Clone())
		entry.mu.Unlock()
	}
	return out
}

// Delete removes a session's metadata, refusing while the session is active
// (transition T20). A refused delete leaves the record in place.
func (e *Engine) Delete(id ID) error {
	entry := e.lookup(id)
	if entry == nil {
		return NewError(CodeNotFound, string(id), "session is not registered", "list the sessions to see valid ids")
	}

	entry.mu.Lock()
	err := entry.session.EnsureDeletable()
	entry.mu.Unlock()
	if err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sessions[id] != entry {
		return NewError(CodeNotFound, string(id), "session is no longer registered", "list the sessions to see valid ids")
	}
	delete(e.sessions, id)
	for i, known := range e.order {
		if known == id {
			e.order = append(e.order[:i], e.order[i+1:]...)
			break
		}
	}
	return nil
}

// Snapshot projects the engine's state into a read model at one revision.
func (e *Engine) Snapshot(authority Authority, observedAt time.Time, revision uint64) (Snapshot, error) {
	return NewSnapshot(authority, observedAt, revision, e.Sessions())
}

func (e *Engine) lookup(id ID) *engineSession {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.sessions[id]
}
