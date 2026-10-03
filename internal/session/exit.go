package session

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

// This file holds the exit half of the runtime contract: the terminal evidence a
// reap produces, the confirmed force kill that carries it, and the single waiter
// ADR 0003 requires per child. Together they exist for one reason - delivering a
// signal and observing a child die are different facts, and a record may only
// claim the second one.

// ErrExitAlreadyReaped reports a second wait on a child that was already reaped.
// A runtime must return it rather than reaping twice, so the single-waiter rule
// is enforced by the port and not only by convention.
var ErrExitAlreadyReaped = errors.New("the child was already reaped by the single waiter")

// ExitStatus is the terminal evidence of one reaped child.
//
// It is deliberately small and explicitly optional: a reaped child yields an
// exit code (WIFEXITED), a terminating signal (WIFSIGNALED) or neither, and a
// runtime that cannot report its wait status still has to be able to say that it
// reaped something. What it may not do is say nothing and leave the caller to
// guess.
type ExitStatus struct {
	// ExitCode is the observed exit status, nil when the child produced none.
	ExitCode *int
	// Signal is the signal that terminated the child, e.g. "SIGKILL".
	Signal string
	// Evidence is the runtime's own description of the reap, e.g.
	// "reaped pid=4242 status=0x008b". It is what a runtime supplies when its
	// wait status carries neither a code nor a signal name.
	Evidence string
	// At is when the child was reaped, UTC.
	At time.Time
}

// hasEvidence reports whether the status names how the child ended.
func (s ExitStatus) hasEvidence() bool {
	return s.ExitCode != nil || s.Signal != "" || s.Evidence != ""
}

// Valid reports whether the status carries terminal evidence and an instant.
func (s ExitStatus) Valid() bool { return s.Validate() == nil }

// Validate checks the terminal evidence.
func (s ExitStatus) Validate() error {
	if !s.hasEvidence() {
		return errors.New("a reaped child must report its exit code, the terminating signal or the reap evidence")
	}
	if s.ExitCode != nil && s.Signal != "" {
		return errors.New("a reaped child carries either an exit code or a signal, never both")
	}
	if s.At.IsZero() {
		return errors.New("a reap has no timestamp")
	}
	return nil
}

// Reason renders the status as the recorded cause of a terminal observation. The
// kind must be a terminal reason kind: evidence of a reap can never explain an
// unknown lifecycle.
func (s ExitStatus) Reason(kind ReasonKind) (Reason, error) {
	if !kind.Terminal() {
		return Reason{}, errors.New("a reaped child can only record a terminal reason kind")
	}
	reason := Reason{Kind: kind, Signal: s.Signal, Detail: s.Evidence}
	if s.ExitCode != nil {
		code := *s.ExitCode
		reason.ExitCode = &code
	}
	if err := reason.Validate(); err != nil {
		return Reason{}, err
	}
	return reason, nil
}

// Describe renders the evidence for logs and reason details.
func (s ExitStatus) Describe() string {
	text := "reaped"
	if s.ExitCode != nil {
		text = "exit-code=" + strconv.Itoa(*s.ExitCode)
	}
	if s.Signal != "" {
		text += " signal=" + s.Signal
	}
	if s.Evidence != "" {
		text += " detail=" + s.Evidence
	}
	return text
}

// KillOutcome is the confirmed result of a forced termination (T10).
//
// Delivered and Terminated are separate on purpose. A delivered signal is not
// evidence that the group died - a member may survive, or the runtime may return
// before the reap - so only a confirmed termination may be recorded as an exit.
type KillOutcome struct {
	// Delivered reports whether the signal reached the verified owned group.
	Delivered bool
	// Signal is the signal that was delivered.
	Signal string
	// Status is the terminal evidence of the reap. It is required once
	// Terminated is true and must be empty of evidence before that.
	Terminated ExitStatus
	// Timeout reports that the child outlived the confirmation window. The caller
	// records that as an unverifiable outcome, never as an exit.
	Timeout bool
}

// Validate checks that the outcome is internally consistent: a confirmed kill
// carries terminal evidence, and a kill that timed out carries none, because a
// delivered signal on its own is never proof of an exit.
func (o KillOutcome) Validate() error {
	if o.Timeout {
		if !o.Delivered {
			return errors.New("a timed-out kill must report the delivered signal")
		}
		if o.Terminated.hasEvidence() {
			return errors.New("a kill that timed out reports no terminal evidence")
		}
		return nil
	}
	if !o.Delivered && !o.Terminated.hasEvidence() {
		return errors.New("a kill reports either a delivered signal or a confirmed terminal status")
	}
	return o.Terminated.Validate()
}

// Terminal returns the confirmed terminal evidence and whether the kill ended the
// child.
func (o KillOutcome) Terminal() (ExitStatus, bool) {
	return o.Terminated, !o.Timeout && o.Terminated.hasEvidence()
}

// KilledReason returns the cause to record for a confirmed kill. It is only
// meaningful when Terminal reports a confirmation.
func (o KillOutcome) KilledReason() (Reason, error) {
	status, confirmed := o.Terminal()
	if !confirmed {
		return Reason{}, errors.New("the kill was not confirmed by a reap, so no exit may be recorded")
	}
	return status.Reason(ReasonKilled)
}

// ChildRuntime is the frozen process port of record: ProcessRuntime plus the two
// capabilities the lifecycle contract needs and a signal-only port cannot express.
//
// It is the shape Stage 05 implements, and the one the contract tests hold the
// runtime to. ProcessRuntime stays the narrow port the application layer uses
// today; a runtime that also satisfies ChildRuntime can confirm a force kill and
// hand out the single exit channel.
type ChildRuntime interface {
	ProcessRuntime
	// WaitExit blocks until the recorded child is reaped and returns its terminal
	// evidence. Exactly one waiter per child is permitted, and it is the owner who
	// launches that must establish it with ExitWaiters before calling this: a
	// second wait on the same child must fail with ErrExitAlreadyReaped rather
	// than reap twice or race the first reaper.
	WaitExit(ctx context.Context, wait ExitWait) (ExitStatus, error)
	// ForceKill signals the verified owned group and waits up to grace for the
	// reap, so the caller can record an exit only from an observed one. A child
	// that outlives the window yields Delivered true, Timeout true and no terminal
	// status; the caller then keeps the attempt alive instead of claiming an exit.
	ForceKill(ctx context.Context, recorded ProcessIdentity, grace time.Duration) (KillOutcome, error)
}

// ExitWait identifies the one child a single waiter is allowed to reap.
type ExitWait struct {
	// SessionID and Generation identify the attempt that owns the child.
	SessionID  ID
	Generation Generation
	// Identity is the recorded child the waiter reaps. It must be the verified
	// identity: a waiter on an unverified pid could reap an unrelated process.
	Identity ProcessIdentity
	// Waiter names the owner that is allowed to wait, for diagnostics.
	Waiter string
}

// Validate checks the wait request.
func (w ExitWait) Validate() error {
	if !w.SessionID.Valid() {
		return NewError(CodeInvalidConfiguration, string(w.SessionID), "session id is not valid", "")
	}
	if w.Generation == 0 {
		return NewError(CodeInvalidConfiguration, string(w.SessionID), "exit wait names no attempt", "wait on the attempt that spawned the child")
	}
	if err := w.Identity.Validate(); err != nil {
		return WrapError(CodeInvalidConfiguration, string(w.SessionID),
			"only a verified child identity can be waited on", "re-verify the child before waiting on it", err)
	}
	if w.Waiter == "" {
		return NewError(CodeInvalidConfiguration, string(w.SessionID), "exit wait names no waiter", "")
	}
	return nil
}

// ExitWaiters grants the single waiter right for a child and refuses a second
// one.
//
// "Exactly one waiter per child" (ADR 0003) is otherwise a convention nothing
// checks: two owners may each spawn a goroutine that calls Wait on the same pid
// and only one of them learns anything. The registry makes the rule
// checkable before any spawn happens - claim the attempt, then launch - so a
// duplicate claim is refused with the holder named, exactly like an interactive
// lease.
type ExitWaiters struct {
	mu     sync.Mutex
	claims map[ID]ExitClaim
}

// NewExitWaiters builds an empty registry.
func NewExitWaiters() *ExitWaiters {
	return &ExitWaiters{claims: make(map[ID]ExitClaim)}
}

// Claim grants the right to wait on an attempt's child.
//
// A second claim on the same session is refused with CONFLICT naming the waiter
// that holds it, whether it is for the same generation or a later one: only the
// owner that launched the child may reap it, and a restart is a new child under
// the same session id.
func (w *ExitWaiters) Claim(wait ExitWait) (ExitClaim, error) {
	if err := wait.Validate(); err != nil {
		return ExitClaim{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if held, exists := w.claims[wait.SessionID]; exists {
		return ExitClaim{}, NewError(CodeConflict, string(wait.SessionID),
			"the exit waiter "+held.Waiter+" already owns attempt "+held.Generation.String(),
			"only the owner that launched the child may wait for it; do not start a second waiter").
			ForAttempt(held.Generation)
	}
	claim := ExitClaim{
		SessionID:  wait.SessionID,
		Generation: wait.Generation,
		Identity:   wait.Identity,
		Waiter:     wait.Waiter,
	}
	w.claims[wait.SessionID] = claim
	return claim, nil
}

// Release drops the claim, which the owner does once the child is reaped. It is
// idempotent: a waiter that already finished must still be able to release.
func (w *ExitWaiters) Release(claim ExitClaim) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if held, exists := w.claims[claim.SessionID]; exists && held.Generation == claim.Generation {
		delete(w.claims, claim.SessionID)
	}
}

// Holder returns the current claim for a session, if any.
func (w *ExitWaiters) Holder(id ID) (ExitClaim, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	claim, ok := w.claims[id]
	return claim, ok
}

// ExitClaim is the granted right to wait on one child.
type ExitClaim struct {
	// SessionID and Generation identify the attempt that owns the child.
	SessionID  ID
	Generation Generation
	// Identity is the verified child the waiter may reap.
	Identity ProcessIdentity
	// Waiter names the owner that holds the claim.
	Waiter string
}

// Wait converts the claim into the request the runtime port takes.
func (c ExitClaim) Wait() ExitWait {
	return ExitWait{
		SessionID:  c.SessionID,
		Generation: c.Generation,
		Identity:   c.Identity,
		Waiter:     c.Waiter,
	}
}
