//go:build linux

package terminal

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/pty"
	domain "github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

const MaxResizeBatch = 32

var ErrSessionActive = errors.New("terminal session still has an open PTY reader")

// Options bounds terminal scrollback and subscription buffers.
type Options struct {
	ScrollbackLines   int
	MaxInputBytes     int
	MaxRawOutputBytes int
	ResizeDebounce    time.Duration
}

// DefaultOptions matches the product's configured limits for scrollback and input.
func DefaultOptions() Options {
	return Options{
		ScrollbackLines:   MaxScrollbackLines,
		MaxInputBytes:     65536,
		MaxRawOutputBytes: 65536,
		ResizeDebounce:    16 * time.Millisecond,
	}
}

// Validate rejects options that could disable an output bound or allocate an
// unbounded terminal emulator.
func (o Options) Validate() error {
	switch {
	case o.ScrollbackLines < 1 || o.ScrollbackLines > MaxScrollbackLines:
		return fmt.Errorf("scrollback lines must be between 1 and %d", MaxScrollbackLines)
	case o.MaxInputBytes < 1:
		return errors.New("maximum input bytes must be positive")
	case o.MaxRawOutputBytes < 1:
		return errors.New("maximum raw output bytes must be positive")
	case o.ResizeDebounce < 0 || o.ResizeDebounce > 250*time.Millisecond:
		return errors.New("resize debounce must be between 0 and 250ms")
	default:
		return nil
	}
}

// Manager indexes the PTY-backed terminal sessions owned by one runtime.
type Manager struct {
	mu       sync.RWMutex
	options  Options
	sessions map[domain.ID]*Session
}

// NewManager constructs a terminal source with explicit resource bounds.
func NewManager(options Options) (*Manager, error) {
	if options == (Options{}) {
		options = DefaultOptions()
	}
	if err := options.Validate(); err != nil {
		return nil, err
	}
	return &Manager{options: options, sessions: make(map[domain.ID]*Session)}, nil
}

// Register binds one attempt to its PTY, starts its background drain and starts
// the emulator reply reader. The process runtime retains the returned session.
func (m *Manager) Register(id domain.ID, generation domain.Generation, child *pty.Child, size pty.Size) (*Session, error) {
	if m == nil {
		return nil, errors.New("terminal manager is nil")
	}
	if !id.Valid() || generation == 0 {
		return nil, errors.New("terminal session needs a valid id and nonzero generation")
	}
	if child == nil || child.PID() <= 0 {
		return nil, errors.New("terminal session needs a started PTY child")
	}
	if err := size.Validate(); err != nil {
		return nil, err
	}
	if err := ValidateSize(size.Columns, size.Rows); err != nil {
		return nil, err
	}

	m.mu.Lock()
	if _, exists := m.sessions[id]; exists {
		m.mu.Unlock()
		return nil, domain.NewError(domain.CodeConflict, string(id), "terminal session already has a registered attempt", "finish or remove the previous attempt before registering another")
	}
	s, err := newSession(id, generation, child, size, m.options)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	m.sessions[id] = s
	m.mu.Unlock()
	s.start()
	return s, nil
}

// Subscribe returns the streams for the lease's exact attempt. A client context
// controls acquisition only; cancellation after return cannot stop the child.
func (m *Manager) Subscribe(ctx context.Context, lease app.InteractiveLease) (app.TerminalSubscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	s := m.sessions[lease.SessionID]
	m.mu.RUnlock()
	if s == nil {
		return nil, domain.NewError(domain.CodeSessionIOFailed, string(lease.SessionID), "terminal session is unavailable", "re-read the session and attach to its current attempt")
	}
	return s.Subscribe(ctx, lease)
}

// Remove releases terminal state only after the process runtime has reaped the
// child, closed its PTY master and the background drain has completed.
func (m *Manager) Remove(id domain.ID, generation domain.Generation) error {
	if m == nil {
		return errors.New("terminal manager is nil")
	}
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return nil
	}
	if s.Generation() != generation {
		m.mu.Unlock()
		return domain.NewError(domain.CodeStaleAttempt, string(id), "terminal removal names a stale attempt", "re-read the session before removing its terminal")
	}
	if !s.OutputFinished() {
		m.mu.Unlock()
		return ErrSessionActive
	}
	delete(m.sessions, id)
	m.mu.Unlock()
	return s.Dispose()
}

// compile-time proof that the manager supplies the application terminal port.
var _ app.TerminalSource = (*Manager)(nil)
