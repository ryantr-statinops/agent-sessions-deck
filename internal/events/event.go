package events

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// ID identifies one published event. IDs are opaque to consumers; the publisher
// mints one when a [Spec] leaves it empty.
type ID string

// Type is the event kind, for example [TypeSessionStarted]. The set below
// mirrors PRODUCT.md §21 and is deliberately open: an unknown type is a valid
// event so later stages can add kinds without changing this contract.
type Type string

// Well-known event types (PRODUCT.md §21). Membership is never enforced.
const (
	TypeAgentDetected    Type = "agent.detected"
	TypeSessionCreated   Type = "session.created"
	TypeSessionStarted   Type = "session.started"
	TypeSessionOpened    Type = "session.opened"
	TypeSessionDetached  Type = "session.detached"
	TypeSessionStopped   Type = "session.stopped"
	TypeSessionKilled    Type = "session.killed"
	TypeSessionDead      Type = "session.dead"
	TypeSessionRestarted Type = "session.restarted"
	TypeWorkspaceChanged Type = "workspace.changed"
)

// SessionID is the logical session that owns an event. The zero value means the
// event is not scoped to a single session, as for [TypeWorkspaceChanged].
//
// The events package keeps its own identifier type on purpose: it must not
// import the session package, so the session owner can convert through the
// underlying string when the shared contracts are frozen.
type SessionID string

// Revision orders events published by one [Publisher]. Revisions start at 1 and
// increase by at least one per published event; a zero revision is invalid.
type Revision uint64

// AttemptGeneration identifies the execution attempt an event belongs to. The
// zero value means the event is not scoped to a single attempt, which is the
// normal case for session-level events such as [TypeSessionCreated].
type AttemptGeneration uint64

// Timestamp is an event instant. Every timestamp the package produces is UTC,
// so `time.Time` monotonic-clock and location differences cannot leak into the
// event stream or into equality assertions.
type Timestamp time.Time

// NewTimestamp normalises t to UTC.
func NewTimestamp(t time.Time) Timestamp {
	return Timestamp(t.UTC())
}

// Time returns the instant as a UTC [time.Time].
func (ts Timestamp) Time() time.Time {
	return time.Time(ts).UTC()
}

// String renders the instant as RFC 3339 with nanoseconds in UTC.
func (ts Timestamp) String() string {
	return ts.Time().Format(time.RFC3339Nano)
}

// Spec is the producer input a [Publisher] stamps into a complete [Event]. The
// zero ID means "mint one"; the zero Time means "stamp the publisher clock".
// The revision is never taken from a Spec: it is owned by the publisher.
type Spec struct {
	// ID is the optional caller-supplied event ID.
	ID ID
	// Type is the required event kind.
	Type Type
	// Session scopes the event to a logical session when non-empty.
	Session SessionID
	// Attempt scopes the event to an execution attempt when non-zero.
	Attempt AttemptGeneration
	// Time is the optional caller-supplied event instant.
	Time Timestamp
	// Metadata is the typed, byte-free metadata of the event.
	Metadata Metadata
}

// validate reports whether the spec can be published. The revision, the ID and
// the timestamp are stamped by the publisher, so they are not validated here.
func (s Spec) validate() error {
	if s.Type == "" {
		return ErrInvalidType
	}
	if s.ID != "" {
		if err := validateIdentifier("event id", string(s.ID)); err != nil {
			return err
		}
	}
	if s.Session != "" {
		if err := validateIdentifier("session id", string(s.Session)); err != nil {
			return err
		}
	}
	return nil
}

// Event is one published metadata event: an immutable value that a publisher
// created and subscribers only read.
type Event struct {
	id      ID
	typ     Type
	session SessionID
	attempt AttemptGeneration
	rev     Revision
	at      Timestamp
	meta    Metadata
}

// newEvent assembles an event from already validated parts. Only the publisher
// calls it, which is what keeps the event invariants total.
func newEvent(id ID, typ Type, session SessionID, attempt AttemptGeneration, rev Revision, at Timestamp, meta Metadata) Event {
	return Event{
		id:      id,
		typ:     typ,
		session: session,
		attempt: attempt,
		rev:     rev,
		at:      at,
		meta:    meta,
	}
}

// ID returns the event ID.
func (e Event) ID() ID { return e.id }

// Type returns the event kind.
func (e Event) Type() Type { return e.typ }

// SessionID returns the owning logical session, empty when the event is not
// session scoped.
func (e Event) SessionID() SessionID { return e.session }

// AttemptGeneration returns the owning execution attempt and false when the
// event is not scoped to a single attempt.
func (e Event) AttemptGeneration() (AttemptGeneration, bool) {
	if e.attempt == 0 {
		return 0, false
	}
	return e.attempt, true
}

// Revision returns the publisher-assigned ordering revision, always greater
// than zero on a published event.
func (e Event) Revision() Revision { return e.rev }

// Timestamp returns the event instant in UTC.
func (e Event) Timestamp() Timestamp { return e.at }

// Metadata returns the typed metadata of the event. Metadata values are
// immutable, so the returned copy is safe to read for the event's lifetime.
func (e Event) Metadata() Metadata { return e.meta }

// String renders the event for logs and test failures. It intentionally omits
// metadata values: a diagnostic line never repeats producer payloads.
func (e Event) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "event{type=%s session=%s", e.typ, quoteOrEmpty(string(e.session)))
	if e.attempt != 0 {
		fmt.Fprintf(&b, " attempt=%d", e.attempt)
	}
	fmt.Fprintf(&b, " revision=%d at=%s}", e.rev, e.at)
	return b.String()
}

func quoteOrEmpty(s string) string {
	if s == "" {
		return `""`
	}
	return s
}

// validateIdentifier keeps printable UTF-8 identifiers in the event stream.
// It is what stops a terminal byte from being smuggled through an ID field.
func validateIdentifier(field, value string) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalidID, field)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%w: %s contains a control byte", ErrInvalidID, field)
		}
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%w: %s has leading or trailing whitespace", ErrInvalidID, field)
	}
	return nil
}
