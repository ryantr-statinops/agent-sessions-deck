package session

import (
	"fmt"
	"strings"
	"time"
)

// Contract rows from docs/architecture/session-state-machine.md. An outcome names
// the row it implemented so a test, a log line and a reviewer read the same
// vocabulary.
const (
	RowLaunchValidationFailed = "T1"
	RowLaunchAccepted         = "T2"
	RowSpawned                = "T3"
	RowSpawnFailed            = "T4"
	RowPersistAfterSpawn      = "T5"
	RowNaturalExit            = "T6"
	RowStopRequested          = "T7"
	RowStopped                = "T8"
	RowStopTimeout            = "T9"
	RowKilled                 = "T10"
	RowRestartLive            = "T11"
	RowRestartTerminal        = "T12"
	RowRestartPrecondition    = "T13"
	RowPTYLostAlive           = "T14"
	RowPTYLostUnverifiable    = "T15"
	RowProcessGone            = "T16"
	RowIdentityMismatch       = "T17"
	RowProbeInconclusive      = "T18"
	RowStaleCallback          = "T19"
	RowDeleteWhileActive      = "T20"
)

// AllTransitionRows returns every contract row in order.
func AllTransitionRows() []string {
	return []string{
		RowLaunchValidationFailed,
		RowLaunchAccepted,
		RowSpawned,
		RowSpawnFailed,
		RowPersistAfterSpawn,
		RowNaturalExit,
		RowStopRequested,
		RowStopped,
		RowStopTimeout,
		RowKilled,
		RowRestartLive,
		RowRestartTerminal,
		RowRestartPrecondition,
		RowPTYLostAlive,
		RowPTYLostUnverifiable,
		RowProcessGone,
		RowIdentityMismatch,
		RowProbeInconclusive,
		RowStaleCallback,
		RowDeleteWhileActive,
	}
}

// EffectKind is an instruction the reducer hands back for a caller outside the
// domain to carry out. The reducer never signals anything itself.
type EffectKind string

const (
	// EffectLaunchChild asks the runtime to spawn the frozen command.
	EffectLaunchChild EffectKind = "launch-child"
	// EffectSignalOwnedGroup asks the runtime to signal the verified owned
	// process group.
	EffectSignalOwnedGroup EffectKind = "signal-owned-group"
	// EffectCleanupOwnedChild asks the runtime to remove the child through
	// verified identity after a persist failure (T5).
	EffectCleanupOwnedChild EffectKind = "cleanup-owned-child"
	// EffectDropStaleCallback records that a fenced observation was dropped
	// (T19). It has no side effect.
	EffectDropStaleCallback EffectKind = "drop-stale-callback"
)

// SignalKind is the signal an effect asks for.
type SignalKind string

const (
	SignalTerm SignalKind = "SIGTERM"
	SignalKill SignalKind = "SIGKILL"
)

// Effect is a single instruction for the caller. Signalling and cleanup effects
// always carry a verified ProcessIdentity; an effect without one is refused by
// Reduce rather than downgraded to a bare PID.
type Effect struct {
	// Kind is the instruction.
	Kind EffectKind
	// Signal is the requested signal for signalling effects.
	Signal SignalKind
	// Identity is the verified child the instruction applies to.
	Identity ProcessIdentity
	// Generation is the attempt generation the instruction belongs to.
	Generation Generation
}

// Rejection is a refused transition. It is a typed error, not a silent drop, so
// the caller can report a code, a reason and a next action.
type Rejection struct {
	// Row is the contract row the rejection relates to, empty when the event had
	// no row to reject.
	Row string
	// Err is the typed reason.
	Err *Error
}

func (r *Rejection) Error() string { return r.Err.Error() }

// Unwrap exposes the typed error.
func (r *Rejection) Unwrap() error { return r.Err }

// Code returns the typed rejection code.
func (r *Rejection) Code() Code { return r.Err.Code }

// Outcome is the reducer's result.
type Outcome struct {
	// Session is the resulting session, or an unchanged deep copy when the event
	// was rejected or dropped.
	Session Session
	// Applied reports whether the event changed the session.
	Applied bool
	// Row is the contract row that was applied or rejected, empty when the
	// contract names no row for the case.
	Row string
	// Rejection is set when Applied is false.
	Rejection *Rejection
	// Effects are the instructions for the caller, in order.
	Effects []Effect
}

// Rejected reports whether the outcome refused the event.
func (o Outcome) Rejected() bool { return !o.Applied && o.Rejection != nil }

// HasEffect reports whether the outcome asked for an effect kind.
func (o Outcome) HasEffect(kind EffectKind) bool {
	for _, effect := range o.Effects {
		if effect.Kind == kind {
			return true
		}
	}
	return false
}

// Err returns the typed rejection error, or nil when the event was applied.
func (o Outcome) Err() error {
	if o.Rejection == nil {
		return nil
	}
	return o.Rejection
}

// Validate reports a violated session invariant, so a caller can assert the state
// it produced instead of trusting the reducer.
func (o Outcome) Validate() error { return o.Session.Validate() }

// Reduce applies one observation to a session and returns the next state.
//
// Reduce is pure and total: it never mutates its input, reports a business
// rejection as a typed Outcome instead of a Go error, and always returns a valid
// session, so a caller can drive a whole session through it without special
// cases.
//
// It implements rows T1-T20 plus the attachment-only moves, in this order:
//
//  1. validate the event,
//  2. fence the generation, so an observation from another attempt is refused (T19),
//  3. refuse the transition when the current lifecycle cannot accept it,
//  4. otherwise apply the row and hand effects to the caller.
//
// Two properties are enforced on every result rather than trusted per row: a
// non-lifecycle event never changes the lifecycle axis, and no signalling or
// cleanup effect is ever handed back without a verified ProcessIdentity. A row
// that would break either property is refused as a contract bug instead of
// producing a record nobody can act on.
//
// Restart of a live attempt (T11) is split across two observations on purpose:
// the reducer answers the request by stopping the live attempt and marking it
// restart-pending, and the new attempt is opened by a second restart event once
// the exit has been observed (T12). That keeps "never two concurrent attempts"
// checkable at every intermediate state.
func Reduce(prev Session, e Event) Outcome {
	subject := string(prev.ID)
	if err := e.Validate(); err != nil {
		return refused(prev.Clone(), "", NewError(CodeUnknown, subject, "event rejected: "+err.Error(),
			"the caller must send a complete observation"))
	}
	if err := fenceGeneration(prev, e); err != nil {
		if e.Generation < prev.Generation {
			// T19: the designed late callback. Dropped, recorded, and reported back
			// so the caller can log that it was fenced rather than lost.
			outcome := refused(prev.Clone(), RowStaleCallback, err)
			outcome.Effects = []Effect{{Kind: EffectDropStaleCallback, Generation: e.Generation}}
			return outcome
		}
		// A future generation names no contract row: it is a caller defect rather
		// than a transition, so it is refused with its typed cause and no drop
		// effect, so nothing downstream mistakes it for a benign stale callback.
		return refused(prev.Clone(), "", err)
	}

	next := prev.Clone()
	current, hasCurrent := next.Current()
	var outcome Outcome
	switch e.Kind {
	case EventLaunchRequested:
		outcome = reduceLaunch(next, hasCurrent, e)
	case EventSpawned:
		outcome = reduceSpawned(next, current, hasCurrent, e)
	case EventSpawnFailed:
		outcome = reduceSpawnFailed(next, current, hasCurrent, e)
	case EventPersistFailed:
		outcome = reducePersistFailed(next, current, hasCurrent, e)
	case EventChildExited:
		outcome = reduceChildExited(next, current, hasCurrent, e)
	case EventStopRequested:
		outcome = reduceStopRequested(next, current, hasCurrent, e)
	case EventStopTimeout:
		outcome = reduceStopTimeout(next, current, hasCurrent, e)
	case EventKillRequested:
		outcome = reduceKillRequested(next, current, hasCurrent, e)
	case EventRestartRequested:
		outcome = reduceRestartRequested(next, current, hasCurrent, e)
	case EventPTYLost:
		outcome = reducePTYLost(next, current, hasCurrent, e)
	case EventReconciled:
		outcome = reduceReconciled(next, current, hasCurrent, e)
	case EventProbeInconclusive:
		outcome = reduceProbeInconclusive(next, current, hasCurrent, e)
	case EventAttached:
		outcome = reduceAttach(next, current, hasCurrent, e)
	case EventDetached:
		outcome = reduceDetach(next, current, hasCurrent, e)
	default:
		return refused(next, "", NewError(CodeUnknown, subject,
			fmt.Sprintf("event kind %q has no reducer case", string(e.Kind)), ""))
	}
	return guardOutcome(prev, e, outcome)
}

// ApplyAll folds events in order and stops at the first rejection or drop,
// returning that outcome. It never mutates the input session.
func ApplyAll(prev Session, events ...Event) Outcome {
	outcome := Outcome{Session: prev.Clone(), Applied: true}
	for _, e := range events {
		outcome = Reduce(outcome.Session, e)
		if !outcome.Applied {
			return outcome
		}
	}
	return outcome
}

// fenceGeneration refuses an observation that belongs to another attempt.
//
// The two directions are deliberately different. An older generation is the
// designed late callback of a restarted attempt: it is dropped as T19 with a
// drop effect, and the current attempt is untouched. A newer generation is a
// caller that mis-attributed an observation to an attempt the session has not
// reached, which is a defect rather than a race, so it is refused with a typed
// error (ErrFutureGeneration) that a caller can surface instead of silently
// tolerating a runtime that reports against the wrong attempt.
func fenceGeneration(prev Session, e Event) *Error {
	if e.Generation == prev.Generation {
		return nil
	}
	if e.Generation < prev.Generation {
		return NewError(CodeStaleAttempt, string(prev.ID),
			fmt.Sprintf("observation for generation %d arrived while the session is at generation %d",
				e.Generation, prev.Generation),
			"the stale callback is ignored; the current attempt is untouched").ForAttempt(e.Generation)
	}
	refusal := WrapError(CodeConflict, string(prev.ID),
		fmt.Sprintf("observation for generation %d arrived before the session reached it (current generation %d)",
			e.Generation, prev.Generation),
		"read the session state before reporting on a newer attempt", ErrFutureGeneration)
	return refusal.ForAttempt(e.Generation)
}

// guardOutcome enforces the two whole-result invariants on every applied
// outcome. A row that breaks one is refused as a contract bug with the original
// session, because a half-applied record is worse than a refusal the caller can
// see.
func guardOutcome(prev Session, e Event, outcome Outcome) Outcome {
	if !outcome.Applied {
		return outcome
	}
	if err := guardLifecycleAxis(prev, e, outcome.Session); err != nil {
		return refused(prev.Clone(), outcome.Row, contractBug(prev, e,
			"the transition was refused instead of changing the lifecycle axis", err))
	}
	if err := guardEffects(outcome.Effects); err != nil {
		return refused(prev.Clone(), outcome.Row, contractBug(prev, e,
			"no signal is authorized without a verified process identity", err))
	}
	return outcome
}

// contractBug builds the refusal a broken invariant produces, stamped with the
// attempt only when the event names one.
func contractBug(prev Session, e Event, hint string, err error) *Error {
	bug := NewError(CodeCorruptState, string(prev.ID), err.Error(), "this is a contract bug; "+hint)
	if e.Generation != 0 {
		bug = bug.ForAttempt(e.Generation)
	}
	return bug
}

// guardLifecycleAxis keeps attach and detach off the lifecycle axis: a client
// leaving the interactive view must never terminate the agent.
func guardLifecycleAxis(prev Session, e Event, next Session) error {
	if e.Kind.LifecycleAffecting() {
		return nil
	}
	if prev.State().Lifecycle != next.State().Lifecycle {
		return fmt.Errorf("event %q changed the lifecycle from %q to %q", string(e.Kind),
			string(prev.State().Lifecycle), string(next.State().Lifecycle))
	}
	return nil
}

// guardEffects refuses a signalling or cleanup effect that carries no verified
// identity, because the caller would have nothing to verify against.
func guardEffects(effects []Effect) error {
	for _, effect := range effects {
		switch effect.Kind {
		case EffectSignalOwnedGroup, EffectCleanupOwnedChild:
			if err := effect.Identity.Validate(); err != nil {
				return fmt.Errorf("effect %q carries no verifiable process identity: %w", string(effect.Kind), err)
			}
		}
	}
	return nil
}

// reduceLaunch implements T1 and T2.
//
// T1 opens attempt generation 1 already failed, so the failure stays attributable
// to an attempt and its reason is immutable history; a retry after a failed
// validation is therefore a restart (T12) that bumps the generation. T2 opens
// generation 1 in starting with the resolved command frozen.
func reduceLaunch(next Session, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	if hasCurrent {
		return refused(next, RowRestartTerminal, NewError(CodeConflict, subject,
			fmt.Sprintf("session already has attempt %d; a second attempt needs restart", next.Generation),
			"restart the session to open a new attempt").ForAttempt(next.Generation))
	}
	generation := next.Generation + 1
	attempt := NewAttempt(generation, e.At)
	attempt.WorkspaceID = e.WorkspaceID
	if e.Failure != "" {
		attempt.Lifecycle = LifecycleFailed
		attempt.Reason = LaunchFailed(e.Failure)
		attempt.EndedAt = e.At
		next.appendAttempt(attempt)
		next.Generation = generation
		return commit(next, RowLaunchValidationFailed, e.At)
	}
	attempt.Command = e.Command
	attempt.Lifecycle = LifecycleStarting
	next.appendAttempt(attempt)
	next.Generation = generation
	return commit(next, RowLaunchAccepted, e.At, Effect{
		Kind:       EffectLaunchChild,
		Generation: generation,
	})
}

// reduceSpawned implements T3.
func reduceSpawned(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return refused(next, "", NewError(CodeNotRunning, subject, "session has no attempt", "launch the session first"))
	case current.Lifecycle.Terminal():
		return refused(next, "", terminalRejection(subject, current, "spawn was reported for an attempt that already ended"))
	case current.Lifecycle != LifecycleStarting:
		return refused(next, "", stateRejection(subject, current, "a spawn can only be confirmed while starting"))
	case current.HasIdentity():
		return refused(next, "", stateRejection(subject, current, "attempt already captured a process identity"))
	}
	current.Identity = e.Identity
	current.OwnerInstanceID = e.Identity.OwnerInstanceID
	current.RunningAt = e.At
	current.Lifecycle = LifecycleRunning
	current.Attachment = AttachmentDetached
	next.replaceCurrent(current)
	next.LastSeenAt = e.At
	return commit(next, RowSpawned, e.At)
}

// reduceSpawnFailed implements T4.
func reduceSpawnFailed(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return refused(next, "", NewError(CodeNotRunning, subject, "session has no attempt", "launch the session first"))
	case current.Lifecycle.Terminal():
		return refused(next, "", terminalRejection(subject, current, "spawn failure was reported for an attempt that already ended"))
	case current.Lifecycle != LifecycleStarting:
		return refused(next, "", stateRejection(subject, current, "spawn failure can only be recorded while starting"))
	}
	current.Lifecycle = LifecycleFailed
	current.Reason = LaunchFailed(e.Failure)
	current.EndedAt = e.At
	next.replaceCurrent(current)
	return commit(next, RowSpawnFailed, e.At)
}

// reducePersistFailed implements T5: the child exists but its running state could
// not be persisted, so it is removed through verified identity only and the
// attempt ends as a launch failure.
func reducePersistFailed(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return refused(next, "", NewError(CodeNotRunning, subject, "session has no attempt", "launch the session first"))
	case current.Lifecycle.Terminal():
		return refused(next, "", terminalRejection(subject, current, "a persist failure was reported for an attempt that already ended"))
	case current.Lifecycle != LifecycleStarting && current.Lifecycle != LifecycleRunning:
		return refused(next, "", stateRejection(subject, current, "a persist failure can only be recorded while starting or running"))
	}
	current.setNote(NoteCleanupRequired)
	var effects []Effect
	if current.HasIdentity() {
		effects = append(effects, Effect{
			Kind:       EffectCleanupOwnedChild,
			Signal:     SignalKill,
			Identity:   current.Identity,
			Generation: current.Generation,
		})
	} else {
		// Fail closed: without a verified identity ASD cannot signal anything and
		// must say so instead of signalling a possibly recycled PID.
		current.setNote(NoteCleanupUnverified)
	}
	closeAttempt(&current, LifecycleFailed, LaunchFailed("store-error: "+e.Failure), e.At)
	next.replaceCurrent(current)
	return commit(next, RowPersistAfterSpawn, e.At, effects...)
}

// reduceChildExited implements T6 (natural exit from running) and T8 (exit inside
// the stop window). The lifecycle decides how the exit is recorded: an exit
// observed while stopping is a stop outcome even if the caller reported otherwise,
// so a stop can never be presented as a natural exit.
//
// The recorded reason keeps whatever terminal evidence the caller observed. A
// signal-killed child is reaped with WIFSIGNALED and no exit code, so refusing a
// code-less exit would leave the session stopping forever; conversely a reason
// with no evidence at all never reaches this point, because Event.Validate
// refuses it first.
func reduceChildExited(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return refused(next, "", NewError(CodeNotRunning, subject, "session has no attempt", ""))
	case current.Lifecycle.Terminal():
		return refused(next, "", terminalRejection(subject, current, "exit was reported twice for the same attempt"))
	case current.Lifecycle == LifecycleStarting:
		return refused(next, "", stateRejection(subject, current,
			"a child exit arrived before the spawn was confirmed; record a spawn failure instead"))
	case current.Lifecycle == LifecycleRunning && e.Reason.Kind != ReasonNaturalExit && e.Reason.Kind != ReasonKilled:
		return refused(next, RowNaturalExit, stateRejection(subject, current,
			"a running child exit must be natural-exit or a confirmed killed reap"))
	}
	reason := e.Reason
	row := RowNaturalExit
	if current.Lifecycle == LifecycleStopping {
		row = RowStopped
		reason = Reason{Kind: ReasonStopped, ExitCode: reason.ExitCode, Signal: reason.Signal, Detail: reason.Detail}
	}
	if err := reason.Validate(); err != nil {
		return refused(next, row, NewError(CodeUnknown, subject, err.Error(), ""))
	}
	closeAttempt(&current, LifecycleExited, reason, e.At)
	next.replaceCurrent(current)
	next.LastSeenAt = e.At
	return commit(next, row, e.At)
}

// reduceStopRequested implements T7.
func reduceStopRequested(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return refused(next, "", NewError(CodeNotRunning, subject, "session has no attempt", "launch the session first"))
	case current.Lifecycle.Terminal():
		return refused(next, "", terminalRejection(subject, current, "there is nothing to stop"))
	case current.Lifecycle == LifecycleStopping:
		return refused(next, RowStopRequested, stateRejection(subject, current, "a graceful stop is already in progress"))
	case current.Lifecycle == LifecycleStarting:
		return refused(next, RowStopRequested, stateRejection(subject, current,
			"the child identity is not captured yet, so ASD cannot signal a verified group"))
	case current.Lifecycle == LifecycleUnknown:
		return refused(next, RowStopRequested, unverifiedSignalRejection(subject, current))
	case !current.signalAuthorizable():
		return refused(next, RowStopRequested, NewError(CodeConflict, subject,
			"attempt has no verified process identity, so no signal is authorized",
			"establish identity first, then stop explicitly").ForAttempt(current.Generation))
	}
	current.Lifecycle = LifecycleStopping
	next.replaceCurrent(current)
	next.LastSeenAt = e.At
	return commit(next, RowStopRequested, e.At, Effect{
		Kind:       EffectSignalOwnedGroup,
		Signal:     SignalTerm,
		Identity:   current.Identity,
		Generation: current.Generation,
	})
}

// reduceStopTimeout implements T9: the attempt returns to running with a stop
// timeout note and kill guidance. It never becomes killed, because escalation to
// SIGKILL requires an explicit action.
//
// A stop timeout can only follow a signalled stop, so it also requires the
// identity that stop was authorized on: returning to running is a claim that a
// child exists, and a claim without an identity would fabricate one.
func reduceStopTimeout(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return refused(next, "", NewError(CodeNotRunning, subject, "session has no attempt", ""))
	case current.Lifecycle.Terminal():
		return refused(next, RowStopTimeout, terminalRejection(subject, current, "the attempt already ended"))
	case current.Lifecycle != LifecycleStopping:
		return refused(next, RowStopTimeout, stateRejection(subject, current, "no graceful stop is in progress"))
	case !current.HasIdentity():
		return refused(next, RowStopTimeout, NewError(CodeConflict, subject,
			"the attempt has no verified process identity, so no stop could have been signalled",
			"re-read the session state; a stop timeout cannot return an unverified attempt to running").
			ForAttempt(current.Generation))
	}
	current.Lifecycle = LifecycleRunning
	current.setNote(NoteStopTimeout)
	current.setNote(NoteKillGuidance)
	next.replaceCurrent(current)
	next.LastSeenAt = e.At
	return commit(next, RowStopTimeout, e.At)
}

// reduceKillRequested implements T10.
//
// The recorded cause is the one the caller observed, never one the reducer
// invents: a forced termination is recorded as killed with the signal that was
// actually delivered, and a kill that raced a natural exit is recorded with the
// exit status that was actually reaped. The reducer never overwrites a caller's
// evidence with SIGKILL, because a record that says the wrong signal sent is
// exactly the conflation the state machine separates.
func reduceKillRequested(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return refused(next, "", NewError(CodeNotRunning, subject, "session has no attempt", ""))
	case current.Lifecycle.Terminal():
		return refused(next, RowKilled, terminalRejection(subject, current, "there is nothing to kill"))
	case current.Lifecycle == LifecycleStarting:
		return refused(next, RowKilled, NewError(CodeConflict, subject,
			"the child identity is not captured yet, so no signal is authorized",
			"wait for the spawn to be recorded, then kill explicitly").ForAttempt(current.Generation))
	case !current.signalAuthorizable():
		return refused(next, RowKilled, NewError(CodeConflict, subject,
			"attempt has no verified process identity, so no signal is authorized",
			"establish identity first, then kill explicitly").ForAttempt(current.Generation))
	}
	if e.Identity.Valid() && !e.Identity.Equal(current.Identity) {
		return refused(next, RowKilled, NewError(CodeStaleAttempt, subject,
			"kill observation carries a different process identity than the recorded one",
			"ASD refuses to signal a pid it cannot prove it owns").ForAttempt(current.Generation))
	}
	reason := e.Reason
	if !reason.Kind.Terminal() {
		return refused(next, RowKilled, NewError(CodeConflict, subject,
			fmt.Sprintf("a kill must be reported with the reason the child ended with, got %q", string(reason.Kind)),
			"report the delivered signal or the reaped exit status; ASD never records an exit it did not observe").
			ForAttempt(current.Generation))
	}
	closeAttempt(&current, LifecycleExited, reason, e.At)
	next.replaceCurrent(current)
	next.LastSeenAt = e.At
	return commit(next, RowKilled, e.At, Effect{
		Kind:       EffectSignalOwnedGroup,
		Signal:     SignalKill,
		Identity:   current.Identity,
		Generation: current.Generation,
	})
}

// reduceRestartRequested implements T11, T12 and T13.
//
// Restart keeps the session ID and increments the attempt generation. It re-runs
// the resolved command in the same workspace and never resumes a vendor
// conversation, so the prior attempt's exit reason stays immutable history.
//
// The forced live path (T11) is only available to an attempt that may still own a
// child and whose identity is verified: stopping a starting attempt has nothing
// to signal, and a record without a verified identity authorizes nothing, so both
// are refused instead of producing a signalling effect with a zero identity.
func reduceRestartRequested(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return refused(next, "", NewError(CodeConflict, subject,
			"session has no attempt to restart",
			"launch the session instead"))
	case e.WorkspaceID != "" && e.WorkspaceID != next.WorkspaceID:
		return refused(next, RowRestartTerminal, NewError(CodeConflict, subject,
			fmt.Sprintf("restart resolved a different workspace (%s, session is %s)", e.WorkspaceID, next.WorkspaceID),
			"restart stays in the session's workspace").ForAttempt(current.Generation))
	case current.Orphaned():
		return refused(next, RowRestartLive, NewError(CodeConflict, subject,
			"attempt is orphaned: a surviving process with no PTY blocks restart until an explicit identity-verified action resolves it",
			"stop or kill the verified process explicitly, then restart").ForAttempt(current.Generation))
	case current.Lifecycle.Terminal():
		return openRestartAttempt(next, current, e)
	case current.Lifecycle == LifecycleStopping:
		return refused(next, RowRestartLive, stateRejection(subject, current,
			"a graceful stop is in progress; the new attempt opens once the exit is observed"))
	case current.Lifecycle == LifecycleStarting:
		return refused(next, RowRestartLive, NewError(CodeConflict, subject,
			"the spawn is not confirmed yet, so there is no verified child to stop",
			"wait for the spawn to be recorded, or kill it explicitly once its identity is known").
			ForAttempt(current.Generation))
	case current.Lifecycle == LifecycleUnknown:
		return refused(next, RowRestartLive, NewError(CodeConflict, subject,
			"liveness is unverified, so the live attempt cannot be replaced",
			"establish identity first, then stop or kill explicitly").ForAttempt(current.Generation))
	case !current.HasIdentity():
		return refused(next, RowRestartLive, NewError(CodeConflict, subject,
			"attempt has no verified process identity, so no signal is authorized",
			"establish identity first, then restart again").ForAttempt(current.Generation))
	case !e.Forced:
		return refused(next, RowRestartLive, NewError(CodeConflict, subject,
			fmt.Sprintf("restarting a live attempt (lifecycle %q) needs an explicit force confirmation", string(current.Lifecycle)),
			"confirm explicitly, for example with --force or --yes, then restart").ForAttempt(current.Generation))
	}
	current.Lifecycle = LifecycleStopping
	current.setNote(NoteRestartPending)
	next.replaceCurrent(current)
	next.LastSeenAt = e.At
	return commit(next, RowRestartLive, e.At, Effect{
		Kind:       EffectSignalOwnedGroup,
		Signal:     SignalTerm,
		Identity:   current.Identity,
		Generation: current.Generation,
	})
}

// openRestartAttempt implements T12 and T13 on a terminal attempt.
func openRestartAttempt(next Session, current Attempt, e Event) Outcome {
	generation := current.Generation + 1
	attempt := NewAttempt(generation, e.At)
	attempt.WorkspaceID = next.WorkspaceID
	if e.Failure != "" {
		// T13: the new attempt carries the precondition failure and the prior
		// attempt record stays untouched.
		attempt.Lifecycle = LifecycleFailed
		attempt.Reason = RestartFailed(e.Failure)
		attempt.EndedAt = e.At
		next.appendAttempt(attempt)
		next.Generation = generation
		return commit(next, RowRestartPrecondition, e.At)
	}
	attempt.Command = e.Command
	attempt.Lifecycle = LifecycleStarting
	next.appendAttempt(attempt)
	next.Generation = generation
	return commit(next, RowRestartTerminal, e.At, Effect{
		Kind:       EffectLaunchChild,
		Generation: generation,
	})
}

// reducePTYLost implements T14, T15 and the evidence classes that close an
// attempt from a PTY loss.
//
// The process is never signalled here: a lost PTY is an I/O fact, not a lifecycle
// command (T14, T15).
func reducePTYLost(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	if refusal, guarded := livenessGuard(next, current, hasCurrent); guarded {
		return *refusal
	}
	switch current.Identity.Classify(e.Liveness) {
	case ClassificationVerified:
		if current.Lifecycle == LifecycleUnknown {
			current.Lifecycle = LifecycleRunning
		}
		current.Attachment = AttachmentUnavailable
		current.Reason = Reason{}
		if current.Lifecycle == LifecycleRunning {
			current.setNote(NoteOrphaned)
		}
		next.replaceCurrent(current)
		next.LastSeenAt = e.At
		return commit(next, RowPTYLostAlive, e.At)
	case ClassificationGone:
		closeAttempt(&current, LifecycleExited, ProcessGone(e.Liveness.EvidenceLine()), e.At)
		next.replaceCurrent(current)
		return commit(next, RowProcessGone, e.At)
	case ClassificationStale:
		// The live pid is another process: close the old observation without
		// adopting or signalling it (T17).
		closeAttempt(&current, LifecycleExited, StaleIdentity(e.Liveness.EvidenceLine()), e.At)
		next.replaceCurrent(current)
		return commit(next, RowIdentityMismatch, e.At)
	default:
		current.Lifecycle = LifecycleUnknown
		current.Attachment = AttachmentUnavailable
		current.Reason = probeReason(e.Liveness)
		next.replaceCurrent(current)
		return commit(next, RowPTYLostUnverifiable, e.At)
	}
}

// reduceReconciled implements T16, T17 and the resolution of a sticky unknown from
// real evidence. Reconciliation never recreates a PTY, never auto-restarts and
// never auto-kills (invariant 4).
func reduceReconciled(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	if refusal, guarded := livenessGuard(next, current, hasCurrent); guarded {
		return *refusal
	}
	switch current.Identity.Classify(e.Liveness) {
	case ClassificationVerified:
		if current.Lifecycle == LifecycleUnknown {
			// Sticky unknown resolved by positive evidence, not by a timer.
			current.Lifecycle = LifecycleRunning
		}
		current.setNote(NoteReconciled)
		next.replaceCurrent(current)
		next.LastSeenAt = e.At
		return commit(next, "", e.At)
	case ClassificationGone:
		closeAttempt(&current, LifecycleExited, ProcessGone(e.Liveness.EvidenceLine()), e.At)
		current.setNote(NoteReconciled)
		next.replaceCurrent(current)
		return commit(next, RowProcessGone, e.At)
	case ClassificationStale:
		closeAttempt(&current, LifecycleExited, StaleIdentity(e.Liveness.EvidenceLine()), e.At)
		current.setNote(NoteReconciled)
		next.replaceCurrent(current)
		return commit(next, RowIdentityMismatch, e.At)
	default:
		current.Lifecycle = LifecycleUnknown
		current.Reason = probeReason(e.Liveness)
		current.setNote(NoteReconciled)
		next.replaceCurrent(current)
		return commit(next, RowProbeInconclusive, e.At)
	}
}

// livenessGuard is the shared guard for observation-driven rows. It refuses an
// observation for an attempt that is already closed, never spawned, or still
// starting without a captured child identity. A liveness probe cannot prove
// anything about a process that has not yet been identified.
func livenessGuard(next Session, current Attempt, hasCurrent bool) (*Outcome, bool) {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return ptr(refused(next, "", NewError(CodeNotRunning, subject, "session has no attempt", ""))), true
	case current.Lifecycle.Terminal():
		return ptr(refused(next, "", terminalRejection(subject, current, "the attempt already has a terminal observation"))), true
	case current.Lifecycle == LifecycleCreated:
		return ptr(refused(next, "", stateRejection(subject, current, "no child was ever spawned"))), true
	case current.Lifecycle == LifecycleStarting && !current.HasIdentity():
		return ptr(refused(next, "", startingLivenessRejection(next, current))), true
	}
	return nil, false
}

// startingLivenessRejection refuses a probe before spawn has returned an identity.
// Keep the attempt starting: only a positive spawn/failure result can resolve T2.
func startingLivenessRejection(next Session, current Attempt) *Error {
	return NewError(CodeConflict, string(next.ID),
		"spawn is still pending and no process identity has been captured, so liveness cannot be probed",
		"wait for the launch result before reporting process liveness").ForAttempt(current.Generation)
}

func ptr[T any](v T) *T { return &v }

// reduceProbeInconclusive implements T18.
//
// T18 applies only when an attempt has a captured process identity. A starting
// attempt without one has no process to probe; keep it starting until launch
// returns a spawn-success or spawn-failure result.
func reduceProbeInconclusive(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return refused(next, "", NewError(CodeNotRunning, subject, "session has no attempt", ""))
	case current.Lifecycle.Terminal():
		return refused(next, RowProbeInconclusive, terminalRejection(subject, current,
			"a terminal attempt has no live child to probe"))
	case current.Lifecycle == LifecycleCreated:
		return refused(next, RowProbeInconclusive, stateRejection(subject, current, "no child was ever spawned"))
	case current.Lifecycle == LifecycleStarting && !current.HasIdentity():
		return refused(next, RowProbeInconclusive, startingLivenessRejection(next, current))
	}
	current.Lifecycle = LifecycleUnknown
	current.Reason = probeReason(e.Liveness)
	next.replaceCurrent(current)
	return commit(next, RowProbeInconclusive, e.At)
}

// reduceAttach moves only the attachment axis. Attaching to a session without a PTY
// handle fails with SESSION_IO_FAILED and leaves the process untouched, which
// covers the orphan case as well as the exited case.
func reduceAttach(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return refused(next, "", NewError(CodeSessionIOFailed, subject,
			"session has no attempt, so there is no PTY to attach to",
			"launch the session first"))
	case !current.Attachment.Available():
		return refused(next, "", NewError(CodeSessionIOFailed, subject,
			fmt.Sprintf("no PTY handle for attempt %d (attachment %q)", current.Generation, string(current.Attachment)),
			"the managed PTY is gone; the process may still be running and is left untouched"))
	case current.Attachment == AttachmentAttached:
		return refused(next, "", NewError(CodeConflict, subject,
			"another client already holds the interactive lease",
			"detach the other client first"))
	}
	current.Attachment = AttachmentAttached
	next.replaceCurrent(current)
	return commit(next, "", e.At)
}

// reduceDetach moves only the attachment axis and never touches lifecycle: leaving
// the interactive view must not terminate the agent.
func reduceDetach(next Session, current Attempt, hasCurrent bool, e Event) Outcome {
	subject := string(next.ID)
	switch {
	case !hasCurrent:
		return refused(next, "", NewError(CodeConflict, subject, "session has no attempt to detach from", ""))
	case current.Attachment != AttachmentAttached:
		return refused(next, "", NewError(CodeConflict, subject,
			fmt.Sprintf("session is not attached (attachment %q)", string(current.Attachment)),
			"nothing to detach"))
	}
	current.Attachment = AttachmentDetached
	next.replaceCurrent(current)
	return commit(next, "", e.At)
}

// commit finalizes an applied transition.
func commit(next Session, row string, at time.Time, effects ...Effect) Outcome {
	next.UpdatedAt = at
	return Outcome{Session: next, Applied: true, Row: row, Effects: effects}
}

// refused finalizes a rejected transition with an unchanged deep copy.
func refused(next Session, row string, err *Error) Outcome {
	return Outcome{Session: next, Applied: false, Row: row, Rejection: &Rejection{Row: row, Err: err}}
}

// closeAttempt records a terminal observation. Notes are preserved: the sequence
// that led to the exit (stop timeout, restart pending) is part of the history.
func closeAttempt(attempt *Attempt, lifecycle Lifecycle, reason Reason, at time.Time) {
	attempt.Lifecycle = lifecycle
	attempt.Reason = reason
	attempt.EndedAt = at
	attempt.Attachment = AttachmentUnavailable
	if code, ok := reason.ExitStatus(); ok {
		attempt.ExitCode = code
		attempt.HasExitCode = true
	}
}

// probeReason records why a probe was inconclusive.
func probeReason(obs LivenessObservation) Reason {
	kind := ReasonProbeTimeout
	if strings.Contains(strings.ToLower(obs.Detail), "permission") {
		kind = ReasonPermissionDenied
	}
	return Reason{Kind: kind, Detail: obs.Detail}
}

// stateRejection builds the CONFLICT error used when a transition is not legal in
// the current lifecycle.
func stateRejection(subject string, current Attempt, reason string) *Error {
	return NewError(CodeConflict, subject,
		fmt.Sprintf("%s (attempt %d is %q)", reason, current.Generation, string(current.Lifecycle)),
		"inspect the session first, then use an operation the lifecycle allows").ForAttempt(current.Generation)
}

// terminalRejection builds the NOT_RUNNING error used when the attempt already has
// a terminal observation.
func terminalRejection(subject string, current Attempt, reason string) *Error {
	return NewError(CodeNotRunning, subject,
		fmt.Sprintf("%s (attempt %d is %q with reason %q)", reason, current.Generation,
			string(current.Lifecycle), current.Reason.String()),
		"the attempt is finished; start a new attempt with restart").ForAttempt(current.Generation)
}

// unverifiedSignalRejection builds the refusal used when liveness is unknown: no
// signal may be sent on a possibly recycled PID.
func unverifiedSignalRejection(subject string, current Attempt) *Error {
	return NewError(CodeConflict, subject,
		fmt.Sprintf("lifecycle is %q, so ASD cannot prove the child is still the recorded process", string(current.Lifecycle)),
		"establish identity first, then stop or kill explicitly").ForAttempt(current.Generation)
}
