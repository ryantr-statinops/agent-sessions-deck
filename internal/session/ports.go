package session

import (
	"context"
	"fmt"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// The ports below are the Stage 02 contract between the domain and the layers
// that implement it: the store (Stage 03), discovery and workspace resolution
// (Stage 04) and the process runtime (Stage 05). They are declared here, in the
// domain, because the domain must be able to describe what it needs without
// depending on a store format, a PATH lookup or a process-spawning library.
//
// The ports deliberately carry no PTY handle, no raw file descriptor and no
// terminal stream: those belong to the runtime and terminal-view owners, and the
// interactive lease below is the whole I/O claim the domain needs.

// Store is the single-writer session metadata port (ADR 0003).
//
// Implementations own durability, the schema-version envelope and the revision
// discipline. One call updates every session it carries plus the revision, so no
// observable state spans a half-written file.
//
// The record encoding is not left to the store: EncodeSessions and DecodeSessions
// are the domain's durable form, so a stored session keeps its resolved argv, its
// attempt notes, its reasons, its identities and its timestamps, and a record that
// cannot be read back is refused instead of half-restored. The store adds the
// envelope and the revision around that payload.
type Store interface {
	// Load returns the stored sessions as of the store's current revision.
	Load(ctx context.Context) ([]Session, uint64, error)
	// Commit writes the given sessions and returns the new revision. The
	// expectedRevision must match the store's current revision, otherwise the
	// commit is refused as a conflict so two owners cannot interleave writes.
	Commit(ctx context.Context, sessions []Session, expectedRevision uint64) (uint64, error)
	// Delete removes one session's metadata, refusing while the session is active
	// (transition T20).
	Delete(ctx context.Context, id ID, expectedRevision uint64) (uint64, error)
}

// LaunchRequest is what the runtime needs to spawn one attempt.
type LaunchRequest struct {
	// SessionID and Generation identify the attempt being launched.
	SessionID  ID
	Generation Generation
	// Command is the frozen resolved command; argv is literal.
	Command agent.Command
	// Workspace is the validated directory the command runs in.
	Workspace workspace.Workspace
}

// Validate checks the request before a child is spawned.
func (r LaunchRequest) Validate() error {
	if !r.SessionID.Valid() {
		return NewError(CodeInvalidConfiguration, string(r.SessionID), "session id is not valid", "")
	}
	if r.Generation == 0 {
		return NewError(CodeInvalidConfiguration, string(r.SessionID), "attempt generation is zero", "")
	}
	if err := r.Command.ValidateForLaunch(); err != nil {
		return WrapError(CodeLaunchFailed, string(r.SessionID), "resolved command cannot be launched",
			"fix the configured command for this agent", err)
	}
	if err := r.Workspace.ID().Validate(); err != nil {
		return WrapError(CodeInvalidConfiguration, string(r.SessionID), "workspace identity is not valid",
			"select a directory that exists and is readable", err)
	}
	return nil
}

// ProcessRuntime owns the child process. It is the only component allowed to
// spawn, signal or probe, and it must verify identity before any signal.
//
// The port promises delivery, never death: Signal returning nil means the signal
// reached the verified owned group, nothing more. A session may only be recorded
// as exited from an observation of a reap, so a caller that needs that evidence
// uses ChildRuntime, whose ForceKill confirms it.
type ProcessRuntime interface {
	// Launch spawns the request's command and returns the captured identity. A
	// launch that cannot produce a verified identity must return an error and
	// leave no child behind (T4).
	Launch(ctx context.Context, req LaunchRequest) (ProcessIdentity, error)
	// Observe reads one liveness reading for a recorded identity. An
	// inconclusive probe must report ProbeUnverifiable with a detail, never an
	// exit (T18).
	Observe(ctx context.Context, recorded ProcessIdentity) (LivenessObservation, error)
	// Stop signals the verified owned group and waits up to grace for the child to
	// be reaped. A child that is still alive when the window closes yields
	// Exited false plus the liveness observation, which the caller records as the
	// stop timeout of T9; it must not escalate on its own.
	//
	// When the child is reaped the outcome carries the terminal evidence and the
	// observation is stamped at the reap with Outcome ProbeGone, so the caller can
	// record a stopped attempt even when the wait status holds no exit code.
	Stop(ctx context.Context, recorded ProcessIdentity, grace time.Duration) (StopOutcome, error)
	// Signal delivers a signal to the verified owned process group. It must
	// refuse when the observed identity does not equal the recorded one.
	Signal(ctx context.Context, recorded ProcessIdentity, signal SignalKind) error
}

// StopOutcome is the result of a bounded graceful stop (T7-T9).
//
// A stop ends in exactly one of two shapes, and the fields below are how they are
// told apart. Either the child was reaped inside the window, in which case the
// outcome carries the terminal evidence of that reap - an exit code, a
// terminating signal, or at minimum the runtime's own evidence line, because a
// SIGTERM-killed child is reaped with no exit code at all - or it was not, in
// which case no status may be reported and the caller records the stop timeout of
// T9.
type StopOutcome struct {
	// Exited reports whether the child was reaped inside the grace window.
	Exited bool
	// ExitCode is the observed exit status, or nil when the runtime could not
	// observe one. It is a pointer because "exited with code 0" and "no exit
	// code observed" must never be confused, and the caller records a stopped
	// attempt as either.
	ExitCode *int
	// Signal is the signal that terminated the child, e.g. "SIGTERM". A child
	// killed by a signal produces no exit code, so this is the evidence that keeps
	// such a stop recordable instead of refused.
	Signal string
	// Evidence is the runtime's own line describing the reap, e.g.
	// "reaped pid=4242". It is the last resort for a runtime whose wait status
	// carries neither a code nor a signal name, and it is what keeps a stop from
	// wedging in stopping. Empty means "no evidence reported", never "no exit".
	Evidence string
	// Observation is the liveness reading after the window closed, so the caller
	// can distinguish stopped from stop-timeout with real evidence, and
	// StoppedReason can quote it.
	Observation LivenessObservation
}

// ExitStatus returns the observed exit status and whether one was observed.
func (o StopOutcome) ExitStatus() (int, bool) {
	if o.ExitCode == nil {
		return 0, false
	}
	return *o.ExitCode, true
}

// Terminal returns the terminal evidence of the reap and whether any was
// reported. The instant comes from Observation, which a runtime stamps when it
// reaped the child.
func (o StopOutcome) Terminal() (ExitStatus, bool) {
	status := ExitStatus{ExitCode: o.ExitCode, Signal: o.Signal, Evidence: o.evidenceLine()}
	if o.Observation.Validate() == nil {
		status.At = o.Observation.At
	}
	return status, o.Exited && status.hasEvidence()
}

// TimedOut reports that the child outlived the graceful window (T9).
func (o StopOutcome) TimedOut() bool { return !o.Exited }

// StoppedReason returns the cause to record for a child reaped inside the stop
// window (T8). It is only meaningful when Exited is true and returns the zero
// reason otherwise, so a timeout can never be recorded as a stop.
//
// The reason keeps whatever the runtime observed - exit code, terminating signal
// or evidence line - so a signal-killed child is recordable. The caller does not
// have to assemble it, which is what keeps a code-less stop from being refused
// and leaving the session stopping forever.
func (o StopOutcome) StoppedReason() Reason {
	if !o.Exited {
		return Reason{}
	}
	reason := Reason{Kind: ReasonStopped, Signal: o.Signal, Detail: o.evidenceLine()}
	if code, observed := o.ExitStatus(); observed {
		reason.ExitCode = &code
	}
	return reason
}

// evidenceLine returns the runtime's description of the reap, preferring the
// explicit field and falling back to a validated observation.
func (o StopOutcome) evidenceLine() string {
	if o.Evidence != "" {
		return o.Evidence
	}
	if o.Observation.Validate() == nil {
		return o.Observation.EvidenceLine()
	}
	return ""
}

// Validate checks that the outcome is internally consistent: a reaped child
// carries terminal evidence, and a child that was not reaped carries no status.
func (o StopOutcome) Validate() error {
	if !o.Exited {
		switch {
		case o.ExitCode != nil:
			return fmt.Errorf("stop outcome reports exit code %d for a child that was not reaped", *o.ExitCode)
		case o.Signal != "":
			return fmt.Errorf("stop outcome reports signal %q for a child that was not reaped", o.Signal)
		case o.Evidence != "":
			return fmt.Errorf("stop outcome reports reap evidence for a child that was not reaped")
		}
		return nil
	}
	if _, ok := o.Terminal(); !ok {
		return fmt.Errorf("a reaped stop must report the exit code, the terminating signal or the reap evidence")
	}
	return nil
}

// InteractiveLease is the domain-side claim on a session's interactive I/O.
//
// V1 allows exactly one lease per session; a second writer is refused with a
// reason. The stream plumbing belongs to the runtime owner.
type InteractiveLease struct {
	// SessionID is the leased session.
	SessionID ID
	// Generation is the attempt the lease is bound to, so a lease cannot outlive
	// its attempt across a restart.
	Generation Generation
	// Holder names the client that owns the lease.
	Holder string
	// AcquiredAt is when the lease was granted, UTC.
	AcquiredAt time.Time
}

// LeaseBroker grants and releases interactive leases.
type LeaseBroker interface {
	// Acquire grants the lease, or returns a CONFLICT error naming the holder
	// when one is already granted.
	Acquire(ctx context.Context, req Lease) (InteractiveLease, error)
	// Release drops the lease. Releasing a lease for a finished attempt is not an
	// error: a client disconnect must always be able to detach.
	Release(ctx context.Context, lease InteractiveLease) error
}

// Lease is the request to acquire an interactive lease.
type Lease struct {
	// SessionID and Generation identify the attempt to attach to.
	SessionID  ID
	Generation Generation
	// Holder names the requesting client.
	Holder string
	// At is the request timestamp, UTC.
	At time.Time
}

// Validate checks a lease request.
func (l Lease) Validate() error {
	if !l.SessionID.Valid() {
		return NewError(CodeInvalidConfiguration, string(l.SessionID), "session id is not valid", "")
	}
	if l.Generation == 0 {
		return NewError(CodeStaleAttempt, string(l.SessionID), "lease request names no attempt", "re-read the session, then attach")
	}
	if l.Holder == "" {
		return NewError(CodeInvalidConfiguration, string(l.SessionID), "lease request names no holder", "")
	}
	if l.At.IsZero() {
		return NewError(CodeInvalidConfiguration, string(l.SessionID), "lease request has no timestamp", "")
	}
	return nil
}
