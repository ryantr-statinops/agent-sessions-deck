package events

import (
	"context"
	"errors"
	"testing"
)

func TestSubscriptionDeliversInRevisionOrder(t *testing.T) {
	publisher := newTestPublisher(t)
	sub := mustSubscribe(t, publisher, 8)
	defer sub.Close()

	events := publishCount(t, publisher, 5)
	got := recvAll(t, sub, 5)
	for i, revision := range got {
		if revision != events[i].Revision() {
			t.Fatalf("delivery %d revision = %d, want %d", i, revision, events[i].Revision())
		}
	}
	if stats := sub.Stats(); stats.Dropped != 0 || stats.ResyncRequired || stats.Delivered != 5 || stats.Pending != 0 {
		t.Fatalf("Stats() = %+v, want 5 delivered, 0 dropped, no resync", stats)
	}
	if sub.Buffer() != 8 {
		t.Fatalf("Buffer() = %d, want 8", sub.Buffer())
	}
}

func TestOverflowDropsAndMarksResyncRequired(t *testing.T) {
	publisher := newTestPublisher(t)
	slow := mustSubscribe(t, publisher, 2)
	defer slow.Close()

	publishCount(t, publisher, 5)

	stats := slow.Stats()
	if stats.Delivered != 2 {
		t.Fatalf("Delivered = %d, want 2", stats.Delivered)
	}
	if stats.Dropped != 3 {
		t.Fatalf("Dropped = %d, want 3", stats.Dropped)
	}
	if !stats.ResyncRequired {
		t.Fatal("ResyncRequired = false, want the subscription marked for a revision resync")
	}
	if stats.ResyncRevision != 5 {
		t.Fatalf("ResyncRevision = %d, want the newest revision 5", stats.ResyncRevision)
	}
	if stats.Pending != 2 {
		t.Fatalf("Pending = %d, want the bounded buffer to stay at 2", stats.Pending)
	}
	if stats.Buffer != 2 {
		t.Fatalf("Buffer = %d, want 2", stats.Buffer)
	}
}

func TestResyncMarkerArrivesWhenSpacePermitsAndDeliveryResumes(t *testing.T) {
	publisher := newTestPublisher(t)
	sub := mustSubscribe(t, publisher, 2)
	defer sub.Close()

	events := publishCount(t, publisher, 5)

	// The two buffered events drain first, in revision order.
	for _, want := range events[:2] {
		notification := recvWithin(t, sub)
		if notification.IsResyncRequired() {
			t.Fatalf("notification %s arrived before the buffered events", notification)
		}
		event, ok := notification.Event()
		if !ok || event.Revision() != want.Revision() {
			t.Fatalf("delivered %s, want revision %d", notification, want.Revision())
		}
	}

	marker := recvWithin(t, sub)
	if !marker.IsResyncRequired() {
		t.Fatalf("third notification = %s, want a resync-required marker", marker)
	}
	if marker.ResyncRevision() != 5 {
		t.Fatalf("marker revision = %d, want the newest missed revision 5", marker.ResyncRevision())
	}
	if _, ok := marker.Event(); ok {
		t.Fatal("a resync marker must not carry an event")
	}
	if got := marker.String(); got != "notification{resync_required revision=5}" {
		t.Fatalf("marker String() = %q", got)
	}
	if stats := sub.Stats(); stats.ResyncRequired || stats.ResyncRevision != 0 {
		t.Fatalf("Stats() = %+v, want the resync state cleared once delivered", stats)
	}

	// After the marker the subscription is healthy again.
	resumed, err := publisher.Publish(Spec{Type: TypeSessionStopped, Session: "s1"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	notification := recvWithin(t, sub)
	if notification.IsResyncRequired() {
		t.Fatalf("notification %s, want the resumed event", notification)
	}
	if notification.Revision() != resumed.Revision() {
		t.Fatalf("resumed revision = %d, want %d", notification.Revision(), resumed.Revision())
	}
	if notification.Revision() <= marker.ResyncRevision() {
		t.Fatalf("resumed revision %d must be greater than the marker revision %d", notification.Revision(), marker.ResyncRevision())
	}
}

func TestOverflowCoalescesIntoTheNewestMissedRevision(t *testing.T) {
	publisher := newTestPublisher(t)
	sub := mustSubscribe(t, publisher, 1)
	defer sub.Close()

	publishCount(t, publisher, 1)
	if got := recvWithin(t, sub).Revision(); got != 1 {
		t.Fatalf("first revision = %d, want 1", got)
	}

	publishCount(t, publisher, 40)
	stats := sub.Stats()
	if !stats.ResyncRequired || stats.ResyncRevision != 41 {
		t.Fatalf("Stats() = %+v, want a pending resync at revision 41", stats)
	}

	// The buffer still holds revision 2; the marker follows it.
	if got := recvWithin(t, sub).Revision(); got != 2 {
		t.Fatalf("buffered revision = %d, want 2", got)
	}
	marker := recvWithin(t, sub)
	if !marker.IsResyncRequired() || marker.ResyncRevision() != 41 {
		t.Fatalf("notification = %s, want a resync marker at revision 41", marker)
	}
	if stats := sub.Stats(); stats.Dropped != 39 || stats.Pending != 0 || stats.Delivered != 3 {
		t.Fatalf("Stats() = %+v, want 39 dropped, 3 delivered (2 events and the marker), no pending", stats)
	}
}

func TestRecvReportsContextCancellation(t *testing.T) {
	publisher := newTestPublisher(t)
	sub := mustSubscribe(t, publisher, 2)
	defer sub.Close()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := sub.Recv(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Recv(cancelled) error = %v, want context.Canceled", err)
	}

	timedOut, cancelTimeout := context.WithTimeout(t.Context(), blockDeadline)
	defer cancelTimeout()
	if _, err := sub.Recv(timedOut); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Recv(expired) error = %v, want context.DeadlineExceeded", err)
	}
}

func TestCloseIsIdempotentAndDeregisters(t *testing.T) {
	publisher := newTestPublisher(t)
	sub := mustSubscribe(t, publisher, 4)
	publishCount(t, publisher, 2)

	for _, closeCall := range []struct {
		name string
		call func() error
	}{
		{name: "Close", call: sub.Close},
		{name: "Cancel", call: sub.Cancel},
		{name: "Close again", call: sub.Close},
	} {
		if err := closeCall.call(); err != nil {
			t.Fatalf("%s: %v, want nil for an idempotent close", closeCall.name, err)
		}
	}
	if got := publisher.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() = %d after Close, want 0", got)
	}

	// Buffered notifications stay readable; the closed subscription reports
	// itself once they are drained.
	for want := Revision(1); want <= 2; want++ {
		if got := recvWithin(t, sub).Revision(); got != want {
			t.Fatalf("drained revision = %d, want %d", got, want)
		}
	}
	if _, err := sub.Recv(t.Context()); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("Recv on a closed subscription = %v, want %v", err, ErrSubscriptionClosed)
	}
	if _, err := sub.Recv(nil); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("Recv(nil ctx) on a closed subscription = %v, want %v", err, ErrSubscriptionClosed)
	}

	// A closed subscription ignores later publishes and never resurrects.
	publishCount(t, publisher, 1)
	if stats := sub.Stats(); !stats.Closed || stats.Pending != 0 {
		t.Fatalf("Stats() = %+v, want a closed, empty subscription", stats)
	}
	if got := publisher.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() = %d, want 0", got)
	}
	select {
	case <-sub.Done():
	default:
		t.Fatal("Done() is not closed after Close")
	}
}

func TestCancellingTheSubscribeContextClosesTheSubscription(t *testing.T) {
	publisher := newTestPublisher(t)
	ctx, cancel := context.WithCancel(t.Context())
	sub, err := publisher.Subscribe(ctx, 2)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	publishCount(t, publisher, 1)

	cancel()
	waitFor(t, sub.Done(), "the subscription to close after context cancellation")

	// The buffered event is still drained before the close is reported.
	if got := recvWithin(t, sub).Revision(); got != 1 {
		t.Fatalf("drained revision = %d, want 1", got)
	}
	if _, err := sub.Recv(t.Context()); !errors.Is(err, ErrSubscriptionClosed) {
		t.Fatalf("Recv after cancellation = %v, want %v", err, ErrSubscriptionClosed)
	}
	if got := publisher.SubscriberCount(); got != 0 {
		t.Fatalf("SubscriberCount() = %d after cancellation, want 0", got)
	}
}

func TestNotificationAccessors(t *testing.T) {
	publisher := newTestPublisher(t)
	sub := mustSubscribe(t, publisher, 2)
	defer sub.Close()

	event, err := publisher.Publish(Spec{Type: TypeSessionDetached, Session: "s1"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	notification := recvWithin(t, sub)
	if notification.IsResyncRequired() || notification.ResyncRevision() != 0 {
		t.Fatalf("notification = %s, want a plain event delivery", notification)
	}
	got, ok := notification.Event()
	if !ok || got.Revision() != event.Revision() {
		t.Fatalf("Event() = %s, %v, want revision %d", got, ok, event.Revision())
	}
	if notification.Revision() != event.Revision() {
		t.Fatalf("Revision() = %d, want %d", notification.Revision(), event.Revision())
	}
	if _, ok := (Notification{}).Event(); ok {
		t.Fatal("the zero notification reported an event")
	}
	if got := (Notification{}).Revision(); got != 0 {
		t.Fatalf("the zero notification revision = %d, want 0", got)
	}
}
