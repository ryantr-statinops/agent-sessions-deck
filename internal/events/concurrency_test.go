package events

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
)

// TestSlowSubscriberDoesNotBlockThePublisherOrOtherSubscribers is the overflow
// requirement in behavioural form: a subscriber that never reads must not stall
// publishing, must not stall another subscriber, and must not need the publisher
// to hold any lock while its buffer is full.
func TestSlowSubscriberDoesNotBlockThePublisherOrOtherSubscribers(t *testing.T) {
	publisher := newTestPublisher(t)
	const events = 1000

	slow := mustSubscribe(t, publisher, 1) // never read
	defer slow.Close()
	healthy := mustSubscribe(t, publisher, events+1)

	published := publishCount(t, publisher, events)

	got := recvAll(t, healthy, events)
	for i, revision := range got {
		if revision != published[i].Revision() {
			t.Fatalf("healthy delivery %d revision = %d, want %d", i, revision, published[i].Revision())
		}
	}
	healthyStats := healthy.Stats()
	if healthyStats.Dropped != 0 || healthyStats.ResyncRequired {
		t.Fatalf("healthy Stats() = %+v, want no drops while it keeps up", healthyStats)
	}

	slowStats := slow.Stats()
	if slowStats.Delivered != 1 || slowStats.Dropped != events-1 {
		t.Fatalf("slow Stats() = %+v, want 1 delivered and %d dropped", slowStats, events-1)
	}
	if !slowStats.ResyncRequired || slowStats.ResyncRevision != Revision(events) {
		t.Fatalf("slow Stats() = %+v, want a pending resync at revision %d", slowStats, events)
	}
	if slowStats.Pending != 1 {
		t.Fatalf("slow Pending = %d, want the bounded buffer to stay at 1", slowStats.Pending)
	}
}

// TestPublishNeverBlocksOnABlockedSubscriber fills a subscriber buffer to its
// bound while the consumer is parked in Recv. The publisher must still hand
// every event over and must hand them over in revision order.
func TestPublishNeverBlocksOnABlockedSubscriber(t *testing.T) {
	publisher := newTestPublisher(t)
	const events = 200

	// The buffer is exactly large enough, so publishing fills it completely
	// even if the consumer never runs.
	sub := mustSubscribe(t, publisher, events)
	defer sub.Close()

	received := make(chan Revision, events)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(received)
		for {
			notification, err := sub.Recv(t.Context())
			if err != nil {
				return
			}
			received <- notification.Revision()
		}
	}()

	publishCount(t, publisher, events)

	deadline, cancel := context.WithTimeout(t.Context(), guardTimeout)
	defer cancel()
	var seen []Revision
	for len(seen) < events {
		select {
		case revision, ok := <-received:
			if !ok {
				t.Fatalf("subscriber stopped after %d revisions, want %d", len(seen), events)
			}
			if len(seen) > 0 && revision <= seen[len(seen)-1] {
				t.Fatalf("revision %d arrived after %d: delivery must stay ordered", revision, seen[len(seen)-1])
			}
			seen = append(seen, revision)
		case <-deadline.Done():
			t.Fatalf("publisher stalled a blocked subscriber: %d of %d revisions arrived", len(seen), events)
		}
	}
	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitFor(t, done, "the consumer to stop after the subscription closed")

	if stats := sub.Stats(); stats.Dropped != 0 || stats.ResyncRequired {
		t.Fatalf("Stats() = %+v, want no drops and no resync for a buffer that holds every event", stats)
	}
}

func TestParallelPublishAssignsUniqueRevisions(t *testing.T) {
	publisher := newTestPublisher(t)
	const (
		publishers = 8
		perWriter  = 25
	)

	var (
		mu     sync.Mutex
		revs   = make([]Revision, 0, publishers*perWriter)
		wg     sync.WaitGroup
		failed = make(chan error, publishers)
	)
	for writer := range publishers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perWriter {
				event, err := publisher.Publish(Spec{
					Type:    TypeSessionStarted,
					Session: SessionID("s1"),
					Attempt: AttemptGeneration(writer + 1),
					ID:      ID("evt"),
				})
				if err != nil {
					failed <- err
					return
				}
				mu.Lock()
				revs = append(revs, event.Revision())
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(failed)
	for err := range failed {
		t.Fatalf("parallel Publish: %v", err)
	}

	if len(revs) != publishers*perWriter {
		t.Fatalf("published %d events, want %d", len(revs), publishers*perWriter)
	}
	if got := publisher.Revision(); got != publishers*perWriter {
		t.Fatalf("Revision() = %d, want %d", got, publishers*perWriter)
	}
	slices.Sort(revs)
	for i, revision := range revs {
		if want := Revision(i + 1); revision != want {
			t.Fatalf("sorted revision %d = %d, want %d: revisions must be unique and gapless", i, revision, want)
		}
	}
}

// TestConcurrentSubscribePublishAndClose exercises the publisher and the
// subscription locks against each other. Under -race it is the guard for the
// lock ordering the package documents.
func TestConcurrentSubscribePublishAndClose(t *testing.T) {
	publisher := newTestPublisher(t)
	const (
		readers       = 6
		publishers    = 4
		eventsPerWork = 50
	)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Each reader buffer is large enough for the whole run, so this test
	// measures lock behaviour rather than the overflow policy.
	for range readers {
		sub := mustSubscribe(t, publisher, publishers*eventsPerWork+1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer sub.Close()
			last := Revision(0)
			for {
				notification, err := sub.Recv(t.Context())
				if errors.Is(err, ErrSubscriptionClosed) {
					return
				}
				if err != nil {
					return
				}
				if notification.Revision() <= last {
					t.Errorf("revision %d arrived after %d", notification.Revision(), last)
					return
				}
				last = notification.Revision()
			}
		}()
	}

	// A churn of short-lived subscribers runs alongside the publishers.
	const churn = 50
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range churn {
			select {
			case <-stop:
				return
			default:
			}
			sub, err := publisher.Subscribe(t.Context(), 1)
			if errors.Is(err, ErrPublisherClosed) {
				return
			}
			if err != nil {
				t.Errorf("Subscribe: %v", err)
				return
			}
			if err := sub.Close(); err != nil {
				t.Errorf("Close: %v", err)
				return
			}
		}
	}()

	var failed sync.WaitGroup
	for range publishers {
		failed.Add(1)
		go func() {
			defer failed.Done()
			for range eventsPerWork {
				if _, err := publisher.Publish(Spec{Type: TypeSessionStarted, Session: "s1"}); err != nil {
					t.Errorf("Publish: %v", err)
					return
				}
			}
		}()
	}
	failed.Wait()
	close(stop)

	if err := publisher.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	waitFor(t, done, "every subscriber goroutine to exit after the publisher closed")

	if got := publisher.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() = %d, want 0", got)
	}
	if got := publisher.Revision(); got != publishers*eventsPerWork {
		t.Fatalf("Revision() = %d, want %d", got, publishers*eventsPerWork)
	}
}

func TestConcurrentCloseIsIdempotentAndRaceSafe(t *testing.T) {
	publisher := newTestPublisher(t)
	sub := mustSubscribe(t, publisher, 4)
	publishCount(t, publisher, 2)

	var (
		wg   sync.WaitGroup
		errs = make(chan error, 16)
	)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sub.Cancel(); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Cancel: %v, want nil", err)
	}

	if got := recvWithin(t, sub).Revision(); got != 1 {
		t.Fatalf("drained revision = %d, want 1", got)
	}
	if got := recvWithin(t, sub).Revision(); got != 2 {
		t.Fatalf("drained revision = %d, want 2", got)
	}
	if _, err := sub.Recv(t.Context()); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("Recv = %v, want %v", err, ErrSubscriptionClosed)
	}
	if got := publisher.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() = %d, want 0", got)
	}
}

func TestConcurrentPublishAndSubscribeCloseDoNotDuplicateDeliveries(t *testing.T) {
	publisher := newTestPublisher(t)
	const events = 300

	// The buffer is larger than the event count, so no delivery can be dropped
	// no matter how the consumer is scheduled.
	sub := mustSubscribe(t, publisher, events+1)

	consumer := make(chan Revision, events)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(consumer)
		for {
			notification, err := sub.Recv(t.Context())
			if err != nil {
				return
			}
			consumer <- notification.Revision()
		}
	}()

	publishCount(t, publisher, events)
	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitFor(t, done, "the consumer to drain the subscription")

	seen := make(map[Revision]bool, events)
	for revision := range consumer {
		if seen[revision] {
			t.Fatalf("revision %d was delivered twice", revision)
		}
		seen[revision] = true
	}
	if len(seen) != events {
		t.Fatalf("received %d unique revisions, want %d", len(seen), events)
	}
	if stats := sub.Stats(); stats.Dropped != 0 || stats.ResyncRequired {
		t.Fatalf("Stats() = %+v, want no drops for a draining consumer", stats)
	}
}
