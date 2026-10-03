package events

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// DefaultBufferSize is the subscription buffer size a caller gets when it does
// not care about the exact bound.
const DefaultBufferSize = 64

// defaultIDMint derives a stable event ID from the revision, so a publisher
// without injected dependencies still produces deterministic IDs.
func defaultIDMint(rev Revision) ID {
	return ID(fmt.Sprintf("evt-%06d", rev))
}

// Publisher assigns revisions to metadata events and fans them out to
// subscriptions.
//
// A publisher is safe for concurrent use. Publishing is never blocked by a
// subscriber: revision assignment and fan-out are serialised by publishMu, the
// state lock only guards the revision counter and the subscription list, and
// each subscription writes to its own bounded buffer with a nonblocking send.
// No publisher lock is ever held while a subscription lock is taken, so a slow
// subscriber can hold neither.
type Publisher struct {
	// publishMu serialises revision assignment and fan-out, so concurrent
	// producers cannot deliver their events to a subscriber out of revision
	// order.
	publishMu sync.Mutex

	mu       sync.RWMutex
	closed   bool
	revision Revision
	subs     []*Subscription

	now    func() time.Time
	mintID func(Revision) ID
}

// Option configures a [Publisher].
type Option func(*Publisher)

// WithClock replaces the timestamp source. Tests use it to make event
// timestamps deterministic; the returned time is normalised to UTC.
func WithClock(now func() time.Time) Option {
	return func(p *Publisher) { p.now = now }
}

// WithIDMint replaces event ID minting. Tests use it for deterministic IDs.
func WithIDMint(mint func(Revision) ID) Option {
	return func(p *Publisher) { p.mintID = mint }
}

// NewPublisher returns a publisher that starts at revision zero, so the first
// published event carries revision 1.
func NewPublisher(opts ...Option) *Publisher {
	p := &Publisher{
		now:    time.Now,
		mintID: defaultIDMint,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	if p.now == nil {
		p.now = time.Now
	}
	if p.mintID == nil {
		p.mintID = defaultIDMint
	}
	return p
}

// Revision returns the highest revision published so far, zero when nothing has
// been published.
func (p *Publisher) Revision() Revision {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.revision
}

// SubscriberCount returns the number of live subscriptions.
func (p *Publisher) SubscriberCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.subs)
}

// Publish validates spec, stamps the next revision, the event ID (when the spec
// omits it) and the timestamp (when the spec omits it), then delivers the event
// to every subscription without blocking.
//
// It returns [ErrPublisherClosed] after [Publisher.Close], and the spec errors
// for an empty type or an identifier that is not printable UTF-8. An invalid
// spec never consumes a revision.
func (p *Publisher) Publish(spec Spec) (Event, error) {
	return p.publish(spec, 0, true)
}

// PublishRevisioned publishes spec with a revision that an authoritative source
// already assigned, which is how a store transaction hands its revision to the
// event stream. The revision must be greater than zero and strictly greater
// than the last revision this publisher published; zero returns
// [ErrInvalidRevision] and a duplicate or regressing revision returns
// [ErrRevisionRegressed]. Gaps are allowed, because a store-wide revision also
// advances for mutations that are not events.
func (p *Publisher) PublishRevisioned(rev Revision, spec Spec) (Event, error) {
	if rev == 0 {
		return Event{}, ErrInvalidRevision
	}
	return p.publish(spec, rev, false)
}

// publish stamps and fans out an event. With assignNext set the publisher owns
// the revision and takes the next one under its lock, so concurrent publishers
// never collide; otherwise rev is authoritative and is validated against the
// last published revision.
func (p *Publisher) publish(spec Spec, rev Revision, assignNext bool) (Event, error) {
	// Validate before touching the revision counter: a rejected event must not
	// burn a revision.
	if err := spec.validate(); err != nil {
		return Event{}, err
	}

	// Revision assignment and fan-out run as one step, so every subscriber
	// observes revisions in publication order even when producers publish
	// concurrently. The work under this lock is bounded and never waits for a
	// subscriber.
	p.publishMu.Lock()
	defer p.publishMu.Unlock()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return Event{}, ErrPublisherClosed
	}
	switch {
	case assignNext:
		rev = p.revision + 1
	case rev == 0:
		p.mu.Unlock()
		return Event{}, ErrInvalidRevision
	case rev <= p.revision:
		last := p.revision
		p.mu.Unlock()
		return Event{}, fmt.Errorf("%w: revision %d is not greater than last revision %d", ErrRevisionRegressed, rev, last)
	}
	p.revision = rev
	id := spec.ID
	if id == "" {
		id = p.mintID(rev)
	}
	at := NewTimestamp(spec.Time.Time())
	if time.Time(at).IsZero() {
		at = NewTimestamp(p.now())
	}
	p.mu.Unlock()

	event := newEvent(id, spec.Type, spec.Session, spec.Attempt, rev, at, spec.Metadata)
	// Fan out after the state lock is released: p.mu is never held while a
	// subscription lock is taken, and the fan-out itself never blocks.
	for _, sub := range p.snapshot() {
		sub.deliver(event)
	}
	return event, nil
}

// Subscribe registers a subscription with a bounded buffer of at least one
// notification. The subscription receives events published after this call and
// observes them in increasing revision order.
//
// When ctx is non-nil, cancelling it closes the subscription, which is the
// cancel path for a subscriber that goes away; [Subscription.Close] is the
// explicit equivalent and is idempotent.
func (p *Publisher) Subscribe(ctx context.Context, buffer int) (*Subscription, error) {
	if buffer < 1 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidBuffer, buffer)
	}
	sub := newSubscription(p, buffer)

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		sub.markClosed()
		return nil, ErrPublisherClosed
	}
	p.subs = append(p.subs, sub)
	p.mu.Unlock()

	if ctx != nil {
		go func() {
			select {
			case <-ctx.Done():
				_ = sub.Close()
			case <-sub.Done():
			}
		}()
	}
	return sub, nil
}

// Close closes every subscription and refuses later publishes. It is
// idempotent and never waits for a subscriber to drain.
func (p *Publisher) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	subs := p.subs
	p.subs = nil
	p.mu.Unlock()

	for _, sub := range subs {
		sub.markClosed()
	}
	return nil
}

// snapshot copies the subscription list under the publisher lock so fan-out
// runs lock-free.
func (p *Publisher) snapshot() []*Subscription {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return slices.Clone(p.subs)
}

// remove drops a closed subscription from the fan-out list.
func (p *Publisher) remove(target *Subscription) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if index := slices.Index(p.subs, target); index >= 0 {
		p.subs = slices.Delete(p.subs, index, index+1)
	}
}
