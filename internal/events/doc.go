// Package events defines the in-memory metadata event contract shared by the
// store, session runtime, CLI, TUI and any later daemon.
//
// The package models PRODUCT.md §21 ("meaningful state changes as events") with
// the ordering and safety rules recorded in
// docs/architecture/adr/0003-state-and-storage.md. It is transport-free and
// framework-free: standard library only, no PTY or terminal bytes, no
// transcript persistence, no vendor SDK, no Cobra and no Bubble Tea.
//
// # Event contract
//
// An [Event] carries a stable ID, a [Type], the owning [SessionID] (empty when
// the event is not session scoped), an optional [AttemptGeneration] (zero when
// the event is not scoped to a single execution attempt), a [Revision], a UTC
// [Timestamp] and typed [Metadata]. Events are immutable values: every field
// is private and reached through accessors, so a subscriber can neither observe
// a half-published event nor mutate the publisher's state.
//
// # Metadata policy
//
// Metadata is a bounded, typed field set (string, int64, uint64, float64, bool,
// UTC timestamp, string slice). There is no byte-slug field anywhere in the
// API, and both field names and values are validated:
//
//   - field names that suggest terminal bytes (`stdout`, `bytes`, `pty`,
//     `scrollback`, `transcript`, `output`, …) or environment/secret material
//     (`env`, `argv`, `token`, `secret`, `password`, `api_key`, …) are
//     rejected;
//   - string and string-slice values must be valid UTF-8 free of control
//     bytes, so no terminal escape sequence can travel inside an event.
//
// Producers that need to describe output or agent secrets must publish a
// redacted summary (counts, flags, exit reasons), never the payload itself.
//
// # Ordering
//
// A [Publisher] owns one monotonically increasing revision sequence and stamps
// every event with it. [Publisher.Publish] assigns the next revision;
// [Publisher.PublishRevisioned] accepts an authoritative revision from a store
// transaction and rejects a zero revision or one that is not strictly greater
// than the last published revision. Gaps are legal (a store-wide revision also
// advances for mutations that are not events); regressions and duplicates are
// not.
//
// # Nonblocking delivery
//
// Delivery never blocks the publisher. A publisher serialises revision
// assignment with fan-out, so subscribers observe events in publication order
// even when several producers publish concurrently. Each [Subscription] owns a
// bounded buffer; fan-out performs a nonblocking send and, when the buffer is
// full, marks the subscription as requiring a revision resync, coalesces or
// drops the pending metadata notifications, and later delivers a
// resync-required notification carrying the newest missed revision once space
// permits. A slow subscriber therefore holds no publisher lock at all — the
// publisher state lock is never held while a subscription lock is taken — and a
// subscriber that keeps up observes events in strictly increasing revision
// order. [Subscription.Close] (also reachable by cancelling the subscribe
// context) is idempotent and race-safe.
package events
