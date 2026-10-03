package events

import (
	"context"
	"errors"
	"testing"
)

func TestPublishAssignsMonotonicRevisions(t *testing.T) {
	publisher := newTestPublisher(t)
	if got := publisher.Revision(); got != 0 {
		t.Fatalf("Revision() before publishing = %d, want 0", got)
	}
	events := publishCount(t, publisher, 3)
	for i, event := range events {
		want := Revision(i + 1)
		if event.Revision() != want {
			t.Fatalf("event %d revision = %d, want %d", i, event.Revision(), want)
		}
	}
	if got := publisher.Revision(); got != 3 {
		t.Fatalf("Revision() = %d, want 3", got)
	}
}

func TestPublishRevisionedRejectsZeroAndRegressingRevisions(t *testing.T) {
	publisher := newTestPublisher(t)
	if _, err := publisher.PublishRevisioned(5, Spec{Type: TypeSessionStarted, Session: "s1"}); err != nil {
		t.Fatalf("PublishRevisioned(5): %v", err)
	}

	tests := []struct {
		name    string
		rev     Revision
		wantErr error
	}{
		{name: "zero", rev: 0, wantErr: ErrInvalidRevision},
		{name: "regressing", rev: 4, wantErr: ErrRevisionRegressed},
		{name: "duplicate of the last revision", rev: 5, wantErr: ErrRevisionRegressed},
		{name: "zero after a publish", rev: 0, wantErr: ErrInvalidRevision},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := publisher.PublishRevisioned(tt.rev, Spec{Type: TypeSessionStarted, Session: "s1"}); !errors.Is(err, tt.wantErr) {
				t.Fatalf("PublishRevisioned(%d) error = %v, want %v", tt.rev, err, tt.wantErr)
			}
			if got := publisher.Revision(); got != 5 {
				t.Fatalf("Revision() = %d after a rejected publish, want 5", got)
			}
		})
	}

	gapped, err := publisher.PublishRevisioned(9, Spec{Type: TypeSessionStarted, Session: "s1"})
	if err != nil {
		t.Fatalf("PublishRevisioned(9): %v", err)
	}
	if gapped.Revision() != 9 {
		t.Fatalf("gap revision = %d, want 9", gapped.Revision())
	}
	if got := publisher.Revision(); got != 9 {
		t.Fatalf("Revision() = %d, want 9", got)
	}
	next, err := publisher.Publish(Spec{Type: TypeSessionStarted, Session: "s1"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if next.Revision() != 10 {
		t.Fatalf("revision after a gap = %d, want 10", next.Revision())
	}
}

func TestRejectedSpecNeverReachesASubscriber(t *testing.T) {
	publisher := newTestPublisher(t)
	sub := mustSubscribe(t, publisher, 4)
	defer sub.Close()

	if _, err := publisher.Publish(Spec{Session: "s1"}); !errors.Is(err, ErrInvalidType) {
		t.Fatalf("Publish without a type error = %v, want %v", err, ErrInvalidType)
	}
	published := publishCount(t, publisher, 1)

	if got := recvWithin(t, sub).Revision(); got != published[0].Revision() {
		t.Fatalf("first delivered revision = %d, want %d", got, published[0].Revision())
	}
	cancelled, cancel := context.WithCancel(t.Context())
	defer cancel()
	cancel()
	if _, err := sub.Recv(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Recv after the only event = %v, want context.Canceled (no delivery for the rejected spec)", err)
	}
}

func TestSubscribeRejectsAnEmptyBuffer(t *testing.T) {
	publisher := newTestPublisher(t)
	for _, buffer := range []int{0, -1} {
		if _, err := publisher.Subscribe(t.Context(), buffer); !errors.Is(err, ErrInvalidBuffer) {
			t.Fatalf("Subscribe(buffer=%d) error = %v, want %v", buffer, err, ErrInvalidBuffer)
		}
	}
	if got := publisher.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() = %d, want 0", got)
	}
}

func TestSubscribeUsesTheRequestedBound(t *testing.T) {
	publisher := newTestPublisher(t)
	sub, err := publisher.Subscribe(t.Context(), DefaultBufferSize)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	if sub.Buffer() != DefaultBufferSize {
		t.Fatalf("Buffer() = %d, want %d", sub.Buffer(), DefaultBufferSize)
	}
	stats := sub.Stats()
	if stats.Buffer != DefaultBufferSize || stats.Pending != 0 {
		t.Fatalf("Stats() = %+v, want an empty buffer of %d", stats, DefaultBufferSize)
	}
}

func TestPublishRefusesAfterClose(t *testing.T) {
	publisher := newTestPublisher(t)
	publishCount(t, publisher, 1)

	if err := publisher.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := publisher.Close(); err != nil {
		t.Fatalf("second Close: %v, want idempotent nil", err)
	}
	if _, err := publisher.Publish(Spec{Type: TypeSessionCreated, Session: "s1"}); !errors.Is(err, ErrPublisherClosed) {
		t.Fatalf("Publish after Close error = %v, want %v", err, ErrPublisherClosed)
	}
	if _, err := publisher.PublishRevisioned(99, Spec{Type: TypeSessionCreated, Session: "s1"}); !errors.Is(err, ErrPublisherClosed) {
		t.Fatalf("PublishRevisioned after Close error = %v, want %v", err, ErrPublisherClosed)
	}
	if got := publisher.Revision(); got != 1 {
		t.Fatalf("Revision() = %d after Close, want the last published 1", got)
	}
}

func TestSubscribeAfterPublisherCloseFails(t *testing.T) {
	publisher := newTestPublisher(t)
	if err := publisher.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	sub, err := publisher.Subscribe(t.Context(), 2)
	if !errors.Is(err, ErrPublisherClosed) {
		t.Fatalf("Subscribe after Close error = %v, want %v", err, ErrPublisherClosed)
	}
	if sub != nil {
		t.Fatalf("Subscribe after Close returned %v, want nil", sub)
	}
}

func TestPublisherCloseClosesEverySubscription(t *testing.T) {
	publisher := newTestPublisher(t)
	first := mustSubscribe(t, publisher, 2)
	second := mustSubscribe(t, publisher, 2)
	if got := publisher.SubscriberCount(); got != 2 {
		t.Fatalf("SubscriberCount() = %d, want 2", got)
	}

	if err := publisher.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	for i, sub := range []*Subscription{first, second} {
		if _, err := sub.Recv(t.Context()); !errors.Is(err, ErrSubscriptionClosed) {
			t.Fatalf("subscription %d Recv after publisher Close = %v, want %v", i, err, ErrSubscriptionClosed)
		}
		if !sub.Stats().Closed {
			t.Fatalf("subscription %d is not reported closed", i)
		}
	}
	if got := publisher.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() after Close = %d, want 0", got)
	}
}

func TestNewPublisherToleratesNilOptions(t *testing.T) {
	publisher := NewPublisher(nil, WithClock(nil), WithIDMint(nil))
	event, err := publisher.Publish(Spec{Type: TypeAgentDetected})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if event.Revision() != 1 || event.ID() == "" {
		t.Fatalf("event = %s, want revision 1 and a minted ID", event)
	}
	if event.Timestamp().Time().IsZero() {
		t.Fatal("Publish did not fall back to the wall clock")
	}
}
