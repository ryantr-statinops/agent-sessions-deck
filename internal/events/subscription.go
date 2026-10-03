package events

import (
	"context"
	"fmt"
	"sync"
)

// Notification is one delivery to a subscriber: either a metadata event or a
// resync-required marker.
//
// A resync marker means the subscriber missed events and must reload state as of
// [Notification.ResyncRevision] before trusting its view again. Markers never
// carry an event.
type Notification struct {
	event    Event
	resync   bool
	resyncTo Revision
}

// IsResyncRequired reports whether the subscriber must resync to
// [Notification.ResyncRevision].
func (n Notification) IsResyncRequired() bool { return n.resync }

// ResyncRevision returns the newest revision the subscriber missed. It is only
// meaningful when [Notification.IsResyncRequired] reports true.
func (n Notification) ResyncRevision() Revision { return n.resyncTo }

// Event returns the delivered event and false for a resync marker or for the
// zero notification a failed receive returns.
func (n Notification) Event() (Event, bool) {
	if n.resync || n.event.rev == 0 {
		return Event{}, false
	}
	return n.event, true
}

// Revision returns the revision the notification refers to: the event revision
// for an event delivery, the missed revision for a resync marker.
func (n Notification) Revision() Revision {
	if n.resync {
		return n.resyncTo
	}
	return n.event.rev
}

// String renders the notification for logs and test failures without echoing
// metadata payloads.
func (n Notification) String() string {
	if n.resync {
		return fmt.Sprintf("notification{resync_required revision=%d}", n.resyncTo)
	}
	return n.event.String()
}

// Stats is a point-in-time view of a subscription's delivery state. A UI uses it
// to tell a lagging consumer apart from a quiet stream.
type Stats struct {
	// Buffer is the notification capacity.
	Buffer int
	// Pending is the number of buffered notifications.
	Pending int
	// Delivered counts notifications written to the buffer, resync markers
	// included.
	Delivered uint64
	// Dropped counts notifications the overflow policy discarded.
	Dropped uint64
	// ResyncRequired reports whether the subscription is coalescing dropped
	// notifications into a pending resync marker.
	ResyncRequired bool
	// ResyncRevision is the newest revision the subscriber missed; zero when no
	// resync is pending.
	ResyncRevision Revision
	// Closed reports whether the subscription is closed.
	Closed bool
}

// Subscription is a subscriber's bounded view of the event stream.
//
// Overflow policy: when the buffer is full the subscription is marked as
// requiring a revision resync, pending metadata notifications are coalesced or
// dropped, and once the consumer makes room it receives a resync-required
// notification carrying the newest missed revision. Nothing about that path
// blocks the publisher.
type Subscription struct {
	publisher *Publisher

	mu            sync.Mutex
	notifications chan Notification
	closed        bool
	resync        bool
	resyncTo      Revision
	delivered     uint64
	dropped       uint64
	doneCh        chan struct{}
}

func newSubscription(publisher *Publisher, buffer int) *Subscription {
	return &Subscription{
		publisher:     publisher,
		notifications: make(chan Notification, buffer),
		doneCh:        make(chan struct{}),
	}
}

// deliver offers an event to the subscription without blocking. It is called by
// the publisher and never by a subscriber.
func (s *Subscription) deliver(event Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.resync {
		// A resync marker is already pending: coalesce into it instead of
		// delivering events the consumer would have to discard anyway.
		s.dropped++
		if event.rev > s.resyncTo {
			s.resyncTo = event.rev
		}
		return
	}
	select {
	case s.notifications <- Notification{event: event}:
		s.delivered++
	default:
		// Buffer full: this is the only place a notification is dropped, and it
		// is what keeps a slow subscriber off the publisher's back.
		s.dropped++
		s.resync = true
		s.resyncTo = event.rev
	}
}

// Recv returns the next notification.
//
// It first hands a pending resync marker to the buffer when there is room for
// it, so a subscriber that resumes sees the marker before the events published
// after it. It then blocks until a notification is available, ctx is done
// (returning the context error), or the subscription is closed (returning
// [ErrSubscriptionClosed] once the buffered notifications are drained).
func (s *Subscription) Recv(ctx context.Context) (Notification, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	s.promoteResync()
	select {
	case notification, ok := <-s.notifications:
		if !ok {
			return Notification{}, ErrSubscriptionClosed
		}
		return notification, nil
	case <-ctx.Done():
		return Notification{}, ctx.Err()
	}
}

// Stats returns a snapshot of the delivery counters.
func (s *Subscription) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Stats{
		Buffer:         cap(s.notifications),
		Pending:        len(s.notifications),
		Delivered:      s.delivered,
		Dropped:        s.dropped,
		ResyncRequired: s.resync,
		ResyncRevision: s.resyncTo,
		Closed:         s.closed,
	}
}

// Buffer returns the notification capacity.
func (s *Subscription) Buffer() int { return cap(s.notifications) }

// Done returns a channel closed when the subscription is closed, so a caller
// can wait for shutdown without polling.
func (s *Subscription) Done() <-chan struct{} { return s.doneCh }

// Close stops delivery and deregisters the subscription. It is idempotent and
// race-safe: concurrent Close calls all return nil and the buffer is closed
// exactly once. Notifications already buffered stay readable, and Recv reports
// [ErrSubscriptionClosed] once they are drained.
func (s *Subscription) Close() error {
	// Deregister before flipping the closed flag: Done() closing then implies
	// the publisher no longer lists this subscription.
	if s.publisher != nil {
		s.publisher.remove(s)
	}
	s.markClosed()
	return nil
}

// Cancel is the cancel verb for callers that think in cancellation rather than
// closing; it is the same idempotent operation as [Subscription.Close].
func (s *Subscription) Cancel() error { return s.Close() }

// markClosed flips the closed flag and closes the notification buffer. It
// touches no publisher state, so the publisher can use it while holding no lock
// and while another goroutine is publishing.
func (s *Subscription) markClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.notifications)
	close(s.doneCh)
}

// promoteResync moves a pending resync marker into the buffer when space
// permits, and clears the pending state so later events flow normally again.
func (s *Subscription) promoteResync() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || !s.resync {
		return
	}
	select {
	case s.notifications <- Notification{resync: true, resyncTo: s.resyncTo}:
		s.resync = false
		s.resyncTo = 0
		s.delivered++
	default:
	}
}
