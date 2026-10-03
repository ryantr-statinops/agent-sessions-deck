package events

import (
	"context"
	"testing"
	"time"
)

// guardTimeout bounds a helper that waits for another goroutine. It is a
// deadlock guard, never a synchronisation device: tests never sleep or race a
// timer against a success path.
const guardTimeout = 30 * time.Second

// blockDeadline is the short, deterministic deadline used where the assertion
// is that Recv keeps waiting while nothing is published. The only possible
// outcome is the deadline, so the assertion cannot flake.
const blockDeadline = 50 * time.Millisecond

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// waitFor fails the test when done is not closed in time, which turns a
// regression that deadlocks the event path into a readable failure instead of a
// package-level timeout.
func waitFor(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(guardTimeout):
		t.Fatalf("timed out after %s waiting for %s", guardTimeout, what)
	}
}

// recvAll drains count notifications and returns their revisions.
func recvAll(t *testing.T, sub *Subscription, count int) []Revision {
	t.Helper()
	revisions := make([]Revision, 0, count)
	for range count {
		revisions = append(revisions, recvWithin(t, sub).Revision())
	}
	return revisions
}

// publishCount publishes count minimal session events and fails on the first
// error, which keeps publisher tests focused on delivery rather than validation.
func publishCount(t *testing.T, publisher *Publisher, count int) []Event {
	t.Helper()
	events := make([]Event, 0, count)
	for i := range count {
		event, err := publisher.Publish(Spec{
			Type:    TypeSessionStarted,
			Session: SessionID("s1"),
			Attempt: AttemptGeneration(1),
		})
		if err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
		events = append(events, event)
	}
	return events
}

// mustSubscribe registers a subscription with an explicit buffer size.
func mustSubscribe(t *testing.T, publisher *Publisher, buffer int) *Subscription {
	t.Helper()
	sub, err := publisher.Subscribe(t.Context(), buffer)
	if err != nil {
		t.Fatalf("Subscribe(buffer=%d): %v", buffer, err)
	}
	return sub
}
