// Package app is the application layer of asd: the use cases that the CLI, the
// TUI, foreground IPC and a future daemon all share.
//
// The layer owns sequencing, not decisions. Every lifecycle decision comes from
// the pure reducer in internal/session, every workspace and command decision
// comes from the workspace and agent ports, and every side effect goes through
// an injected port. The service therefore has no filesystem, PTY, Cobra, Bubble
// Tea, IPC or vendor SDK of its own, and its tests run against deterministic
// fakes.
//
// Three rules shape the code here.
//
// Sequencing. A use case asks the pure reducer what would happen before it calls
// a port. A refusal is therefore returned with no process spawned, no signal
// delivered, no lease taken and no state written, which is what keeps "open of
// an orphaned session leaves the process untouched" true by construction.
//
// Concurrency. Every mutating use case runs under one owner-wide mutation lock,
// and the store commit and the metadata event that announces it happen inside
// that same critical section. That is stricter than per-session parallelism and
// deliberately so: a use case spans several steps, one lock keeps the store's
// revision order and the event stream's revision order identical, and it closes
// the check-then-write windows that a delete or a restart would otherwise open.
// Reads stay concurrent. A store write that fails latches the owner: further
// mutations are refused with OWNER_UNAVAILABLE rather than writing engine state
// the store never received, and reads keep working so an operator can see what
// happened.
//
// Persistence. A use case applies its transitions in memory, then writes every
// session through the store with the optimistic expected revision it observed.
// The store is the single writer (ADR 0003), so a refused commit is reported as
// CONFLICT and the in-memory state keeps the failure visible. A child that was
// already spawned and could not be persisted is ended only through its verified
// ProcessIdentity, with a forced kill whose reap is confirmed before the cleanup
// is reported as done, because a recycled PID is never proof of ownership and a
// delivered signal is never proof of a death.
//
// Observation. Snapshots carry the authority of the reading (live from the
// owner, stored from persisted metadata), the store revision and the moment the
// rows were observed, so a stored running row is never presented as a live
// process. The stored reading is read back from the store rather than
// downgraded from memory, because after a failed write the two disagree.
// Metadata events are published only after a committed mutation and carry the
// session, the attempt, that revision and the timestamp, with no terminal bytes
// and no environment material. An event names what was observed: an inconclusive
// probe, an orphaned process and a recycled pid each have their own kind, and
// none of them is published as a death.
package app
