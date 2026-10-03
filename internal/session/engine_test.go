package session

import (
	"context"
	"sync"
	"testing"
)

// TestEngineRefusesToDeleteAnActiveSession is T20 through the engine, and proves a
// refused delete does not lose the record.
func TestEngineRefusesToDeleteAnActiveSession(t *testing.T) {
	engine, err := NewEngine(runningSession(t))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	err = engine.Delete(testSessionID)
	if err == nil {
		t.Fatalf("a running session was deleted")
	}
	if got := CodeOf(err); got != CodeConflict {
		t.Fatalf("code = %s, want %s", got, CodeConflict)
	}
	if _, ok := engine.Get(testSessionID); !ok {
		t.Fatalf("a refused delete dropped the session")
	}

	if _, err := engine.Apply(ID("missing"), Event{Kind: EventStopRequested, Generation: 1, At: at(1)}); CodeOf(err) != CodeNotFound {
		t.Fatalf("unknown session code = %s, want %s", CodeOf(err), CodeNotFound)
	}

	if err := engine.Delete(ID("never-registered")); CodeOf(err) != CodeNotFound {
		t.Fatalf("deleting an unknown session code = %s, want %s", CodeOf(err), CodeNotFound)
	}

	if _, err := engine.Apply(testSessionID, Event{
		Kind:       EventKillRequested,
		Generation: 1,
		At:         at(10),
		Reason:     Killed("SIGKILL"),
	}); err != nil {
		t.Fatalf("Apply kill: %v", err)
	}
	if err := engine.Delete(testSessionID); err != nil {
		t.Fatalf("an exited session was not deletable: %v", err)
	}
}

// TestEngineSerializesPerSessionLifecycle runs concurrent callbacks for one
// session and asserts that exactly one of them wins and that the stored session
// stays valid, which is the Stage 02 part of the concurrency contract.
func TestEngineSerializesPerSessionLifecycle(t *testing.T) {
	engine, err := NewEngine(runningSession(t))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	const workers = 32
	var wg sync.WaitGroup
	applied := make([]bool, workers)
	for i := range workers {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			var event Event
			switch worker % 4 {
			case 0:
				event = Event{Kind: EventStopRequested, Generation: 1, At: at(2000 + worker)}
			case 1:
				event = Event{Kind: EventChildExited, Generation: 1, At: at(2000 + worker), Reason: Stopped(0)}
			case 2:
				event = Event{Kind: EventKillRequested, Generation: 1, At: at(2000 + worker)}
			default:
				event = Event{Kind: EventStopTimeout, Generation: 1, At: at(2000 + worker)}
			}
			outcome, applyErr := engine.Apply(testSessionID, event)
			if applyErr != nil {
				t.Errorf("apply %s: %v", event.Kind, applyErr)
				return
			}
			applied[worker] = outcome.Applied
		}(i)
	}
	wg.Wait()

	// Exactly one of the terminal observations may be recorded.
	stored, ok := engine.Get(testSessionID)
	if !ok {
		t.Fatalf("session disappeared")
	}
	if err := stored.Validate(); err != nil {
		t.Fatalf("concurrent callbacks produced an invalid session: %v", err)
	}
	terminal := 0
	for _, attempt := range stored.Attempts {
		if attempt.Terminal() {
			terminal++
		}
	}
	if terminal != 1 {
		t.Fatalf("%d terminal observations recorded, want exactly 1", terminal)
	}
	if stored.Generation != 1 || stored.AttemptCount() != 1 {
		t.Fatalf("concurrent callbacks created attempts: generation %d, %d attempts", stored.Generation, stored.AttemptCount())
	}
}

// TestLeaseBrokerRefusesASecondWriter covers the one-lease-per-session rule the
// domain owns, including detach after a lease loss.
func TestLeaseBrokerRefusesASecondWriter(t *testing.T) {
	broker := newFakeLeaseBroker()
	first, err := broker.Acquire(context.Background(), Lease{
		SessionID:  testSessionID,
		Generation: 1,
		Holder:     "client-a",
		At:         at(900),
	})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := broker.Acquire(context.Background(), Lease{
		SessionID:  testSessionID,
		Generation: 1,
		Holder:     "client-b",
		At:         at(901),
	}); CodeOf(err) != CodeConflict {
		t.Fatalf("second lease code = %s, want %s", CodeOf(err), CodeConflict)
	}
	if err := broker.Release(context.Background(), first); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := broker.Acquire(context.Background(), Lease{
		SessionID:  testSessionID,
		Generation: 2,
		Holder:     "client-b",
		At:         at(902),
	}); err != nil {
		t.Fatalf("a lease for a new attempt was refused after release: %v", err)
	}

	if err := (Lease{SessionID: testSessionID, Holder: "x", At: at(1)}).Validate(); CodeOf(err) != CodeStaleAttempt {
		t.Fatalf("lease without a generation code = %s, want %s", CodeOf(err), CodeStaleAttempt)
	}
}

// fakeLeaseBroker is an in-memory LeaseBroker.
type fakeLeaseBroker struct {
	mu     sync.Mutex
	leases map[ID]InteractiveLease
}

func newFakeLeaseBroker() *fakeLeaseBroker {
	return &fakeLeaseBroker{leases: map[ID]InteractiveLease{}}
}

func (b *fakeLeaseBroker) Acquire(_ context.Context, req Lease) (InteractiveLease, error) {
	if err := req.Validate(); err != nil {
		return InteractiveLease{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if held, ok := b.leases[req.SessionID]; ok {
		return InteractiveLease{}, NewError(CodeConflict, string(req.SessionID),
			"interactive lease is held by "+held.Holder, "detach the other client first")
	}
	lease := InteractiveLease{
		SessionID:  req.SessionID,
		Generation: req.Generation,
		Holder:     req.Holder,
		AcquiredAt: req.At,
	}
	b.leases[req.SessionID] = lease
	return lease, nil
}

func (b *fakeLeaseBroker) Release(_ context.Context, lease InteractiveLease) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	held, ok := b.leases[lease.SessionID]
	if !ok || held != lease {
		return NewError(CodeConflict, string(lease.SessionID), "lease is not held by this client", "")
	}
	delete(b.leases, lease.SessionID)
	return nil
}

func mustGet(t *testing.T, engine *Engine, id ID) Session {
	t.Helper()
	s, ok := engine.Get(id)
	if !ok {
		t.Fatalf("session %s is not registered", id)
	}
	return s
}
