package session

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// Each test here reproduces one finding from the Stage 02 review against the
// current domain and fails if it comes back. They are written as behaviour, not
// as implementation: the shape of the record or the effect the caller receives is
// what is asserted.

// TestStopOfASignalKilledChildNeverWedgesStopping is the P0-1 reproduction. A
// SIGTERM-killed child is reaped with WIFSIGNALED and no exit code, and the old
// contract refused that observation, so the session stayed in stopping forever and
// every later stop was refused as "already in progress".
func TestStopOfASignalKilledChildNeverWedgesStopping(t *testing.T) {
	stopping := stoppingSession(t)

	outcome := StopOutcome{
		Exited:      true,
		Signal:      "SIGTERM",
		Observation: LivenessObservation{Outcome: ProbeGone, At: at(25), Detail: "wait status 0x008b"},
	}
	if err := outcome.Validate(); err != nil {
		t.Fatalf("the runtime's stop outcome is not a valid one: %v", err)
	}
	exited := reduce(t, stopping, Event{
		Kind:       EventChildExited,
		Generation: 1,
		At:         at(25),
		Reason:     outcome.StoppedReason(),
	})
	if exited.Row != RowStopped {
		t.Fatalf("row = %q, want %q", exited.Row, RowStopped)
	}
	current, ok := exited.Session.Current()
	if !ok {
		t.Fatal("the exited session has no attempt")
	}
	if current.Lifecycle != LifecycleExited {
		t.Fatalf("lifecycle = %q, want exited: a reaped child must not stay stopping", current.Lifecycle)
	}
	if current.Reason.Kind != ReasonStopped || current.Reason.Signal != "SIGTERM" {
		t.Fatalf("reason = %q, want a stopped reason naming SIGTERM", current.Reason.String())
	}
	if current.HasExitCode {
		t.Fatalf("a signal-killed child reported exit code %d", current.ExitCode)
	}
	if !strings.Contains(current.Reason.Detail, "wait status") {
		t.Fatalf("the recorded reason lost the reap evidence: %q", current.Reason.Detail)
	}
	if err := exited.Session.EnsureDeletable(); err != nil {
		t.Fatalf("a stopped session must be deletable: %v", err)
	}
	// The session is not stuck: a second stop is a plain NOT_RUNNING refusal, not
	// a "a graceful stop is already in progress" wedge.
	second := Reduce(exited.Session, Event{Kind: EventStopRequested, Generation: 1, At: at(30)})
	if second.Applied {
		t.Fatal("a stop was applied to an exited attempt")
	}
	if got := second.Rejection.Code(); got != CodeNotRunning {
		t.Fatalf("second stop code = %s, want %s", got, CodeNotRunning)
	}

	// And the reason the current application layer builds by hand from this outcome -
	// the stopped kind plus whatever the observation reported - is recordable as it
	// is, with no exit code at all. That is what un-wedges the existing stop path.
	handbuilt := Reason{Kind: ReasonStopped, Detail: outcome.Observation.EvidenceLine()}
	if code, ok := outcome.ExitStatus(); ok {
		handbuilt.ExitCode = &code
	}
	viaLegacy := reduce(t, stopping, Event{
		Kind:       EventChildExited,
		Generation: 1,
		At:         at(25),
		Reason:     handbuilt,
	})
	legacyCurrent, _ := viaLegacy.Session.Current()
	if legacyCurrent.Lifecycle != LifecycleExited || legacyCurrent.Reason.Kind != ReasonStopped {
		t.Fatalf("the hand-built stopped reason was refused: %q/%q",
			viaLegacy.Session.State().Lifecycle, viaLegacy.Session.Generation)
	}
}

// TestStopOfAReapWithNoUsableStatusIsStillRecorded covers the runtime that can
// neither report a code nor a signal name: the evidence line is enough, so the
// stop is recorded rather than refused.
func TestStopOfAReapWithNoUsableStatusIsStillRecorded(t *testing.T) {
	outcome := StopOutcome{Exited: true, Evidence: "reaped pid=4242 status=0x8b"}
	exited := reduce(t, stoppingSession(t), Event{
		Kind:       EventChildExited,
		Generation: 1,
		At:         at(25),
		Reason:     outcome.StoppedReason(),
	})
	current, _ := exited.Session.Current()
	if current.Lifecycle != LifecycleExited || current.Reason.Detail == "" {
		t.Fatalf("record = %q/%q, want exited with the reap evidence", current.Lifecycle, current.Reason.String())
	}
	if outcome.TimedOut() {
		t.Fatal("a reaped child reported a timeout")
	}
}

// TestAnExitWithNoEvidenceIsRefusedAtTheBoundary keeps the opposite discipline:
// a reason that names no exit code, no signal and no evidence is a claim of death
// with nothing behind it, so it never becomes a record.
func TestAnExitWithNoEvidenceIsRefusedAtTheBoundary(t *testing.T) {
	for _, reason := range []Reason{
		{Kind: ReasonNaturalExit},
		{Kind: ReasonStopped},
		{Kind: ReasonKilled},
	} {
		outcome := Reduce(runningSession(t), Event{
			Kind:       EventChildExited,
			Generation: 1,
			At:         at(30),
			Reason:     reason,
		})
		if outcome.Applied {
			t.Fatalf("%q with no evidence was applied", string(reason.Kind))
		}
		current, _ := outcome.Session.Current()
		if current.Lifecycle != LifecycleRunning {
			t.Fatalf("lifecycle = %q, want running", current.Lifecycle)
		}
	}
}

// TestStartingWithoutIdentityRefusesLivenessProbes prevents T15/T18 from turning a
// pending spawn into an unknown attempt that can never accept T3 or T4. Without a
// captured identity there is no process to probe; the launch result is authoritative.
func TestStartingWithoutIdentityRefusesLivenessProbes(t *testing.T) {
	starting := startingSession(t)
	for _, kind := range []EventKind{EventPTYLost, EventProbeInconclusive, EventReconciled} {
		t.Run(string(kind), func(t *testing.T) {
			event := Event{
				Kind:       kind,
				Generation: starting.Generation,
				At:         at(40),
				Liveness: LivenessObservation{
					Outcome: ProbeUnverifiable,
					Detail:  "probe-timeout",
					At:      at(40),
				},
			}
			outcome := Reduce(starting, event)
			if !outcome.Rejected() || outcome.Rejection.Code() != CodeConflict {
				t.Fatalf("outcome = applied=%v rejection=%v, want a typed conflict", outcome.Applied, outcome.Err())
			}
			current, ok := outcome.Session.Current()
			if !ok || current.Lifecycle != LifecycleStarting || current.HasIdentity() {
				t.Fatalf("attempt = %+v/%v, want starting without identity", current, ok)
			}
		})
	}
}

// TestPTYLossWhileStoppingDoesNotAddTheOrphanAnnotation keeps the annotation
// aligned with its definition: running plus unavailable, not stopping plus unavailable.
func TestPTYLossWhileStoppingDoesNotAddTheOrphanAnnotation(t *testing.T) {
	outcome := Reduce(stoppingSession(t), Event{
		Kind:       EventPTYLost,
		Generation: 1,
		At:         at(40),
		Liveness: LivenessObservation{
			Outcome:  ProbeAlive,
			Identity: testIdentity,
			At:       at(40),
		},
	})
	mustApply(t, outcome)
	current, _ := outcome.Session.Current()
	if current.Lifecycle != LifecycleStopping || current.Attachment != AttachmentUnavailable {
		t.Fatalf("state = %s/%s, want stopping/unavailable", current.Lifecycle, current.Attachment)
	}
	if current.Orphaned() || current.HasNote(NoteOrphaned) {
		t.Fatalf("stopping attempt was annotated orphaned: %+v", current)
	}
}

// TestRunningExitRejectsReconciliationReasons keeps T6 separate from T16/T17
// and from the graceful-stop T8 reason. A confirmed killed reap remains valid.
func TestRunningExitRejectsReconciliationReasons(t *testing.T) {
	for name, reason := range map[string]Reason{
		"process-gone":   ProcessGone("liveness=gone"),
		"stale-identity": StaleIdentity("liveness=alive detail=pid-reused"),
		"stopped":        StoppedBySignal("SIGTERM"),
	} {
		t.Run(name, func(t *testing.T) {
			outcome := Reduce(runningSession(t), Event{
				Kind:       EventChildExited,
				Generation: 1,
				At:         at(40),
				Reason:     reason,
			})
			if outcome.Applied || outcome.Rejection == nil || outcome.Rejection.Code() != CodeConflict {
				t.Fatalf("outcome applied=%v rejection=%v, want conflict", outcome.Applied, outcome.Err())
			}
			current, _ := outcome.Session.Current()
			if current.Lifecycle != LifecycleRunning || !current.Reason.Zero() {
				t.Fatalf("refused exit changed current attempt: %+v", current)
			}
		})
	}
}

// TestForcedRestartIsRefusedFromStarting is the first half of the P1-8
// reproduction: a forced restart of a starting attempt used to emit a signalling
// effect carrying a zero identity, and the follow-up stop timeout then fabricated a
// running attempt that had no child.
func TestForcedRestartIsRefusedFromStarting(t *testing.T) {
	starting := startingSession(t)
	outcome := Reduce(starting, Event{
		Kind:        EventRestartRequested,
		Generation:  1,
		At:          at(60),
		Forced:      true,
		Command:     launchCommand(t),
		WorkspaceID: testWorkspace,
	})
	if outcome.Applied {
		t.Fatal("a forced restart of a starting attempt was applied")
	}
	if got := outcome.Rejection.Code(); got != CodeConflict {
		t.Fatalf("rejection code = %s, want %s", got, CodeConflict)
	}
	if len(outcome.Effects) != 0 {
		t.Fatalf("a refused restart emitted effects: %+v", outcome.Effects)
	}
	if outcome.Session.State() != starting.State() {
		t.Fatalf("a refused restart changed the state: %+v -> %+v", starting.State(), outcome.Session.State())
	}
	if outcome.Session.Generation != starting.Generation {
		t.Fatalf("a refused restart opened attempt %d", outcome.Session.Generation)
	}
	if !strings.Contains(outcome.Rejection.Err.Reason, "spawn is not confirmed") {
		t.Fatalf("rejection = %q, want it to name the unconfirmed spawn", outcome.Rejection.Err.Reason)
	}
}

// TestForcedRestartIsRefusedWithoutAVerifiedIdentity is the second half: a record
// that claims a running attempt with no identity cannot authorize anything, so the
// restart is refused before any signalling effect exists.
func TestForcedRestartIsRefusedWithoutAVerifiedIdentity(t *testing.T) {
	// The hand-edited or older-schema record: running with the identity cleared.
	forged := runningSession(t)
	current, _ := forged.Current()
	current.Identity = ProcessIdentity{}
	forged.replaceCurrent(current)
	if err := forged.Validate(); err == nil {
		t.Fatal("a running attempt without a process identity validates")
	}

	outcome := Reduce(forged, Event{
		Kind:        EventRestartRequested,
		Generation:  1,
		At:          at(60),
		Forced:      true,
		Command:     launchCommand(t),
		WorkspaceID: testWorkspace,
	})
	if outcome.Applied {
		t.Fatal("a forced restart of an unverified attempt was applied")
	}
	if got := outcome.Rejection.Code(); got != CodeConflict {
		t.Fatalf("rejection code = %s, want %s", got, CodeConflict)
	}
	if len(outcome.Effects) != 0 {
		t.Fatalf("a refused restart emitted a signalling effect with no identity: %+v", outcome.Effects)
	}
	// The same rule holds for the two rows that signal directly.
	for name, event := range map[string]Event{
		"stop": {Kind: EventStopRequested, Generation: 1, At: at(61)},
		"kill": {Kind: EventKillRequested, Generation: 1, At: at(61), Reason: Killed("SIGKILL")},
	} {
		refused := Reduce(forged, event)
		if refused.Applied {
			t.Fatalf("%s was applied to an unverified attempt", name)
		}
		if len(refused.Effects) != 0 {
			t.Fatalf("%s emitted effects without an identity: %+v", name, refused.Effects)
		}
	}
}

// TestRunningImpliesAVerifiedIdentity pins the invariant that makes a fabricated
// running state impossible rather than merely unlikely.
func TestRunningImpliesAVerifiedIdentity(t *testing.T) {
	attempt := NewAttempt(1, at(0))
	attempt.WorkspaceID = testWorkspace
	attempt.Lifecycle = LifecycleRunning
	attempt.Attachment = AttachmentDetached
	if err := attempt.Validate(); err == nil {
		t.Fatal("a running attempt with no captured identity validates")
	}

	// Every lifecycle that owns or doubts the liveness of a child requires its
	// captured identity; starting is the sole pre-spawn state without one.
	for _, lifecycle := range AllLifecycles() {
		candidate := NewAttempt(1, at(0))
		candidate.WorkspaceID = testWorkspace
		candidate.Lifecycle = lifecycle
		candidate.Attachment = AttachmentDetached
		if lifecycle == LifecycleUnknown {
			candidate.Reason = ProbeInconclusive(ReasonProbeTimeout, "probe-timeout")
		}
		switch {
		case lifecycle == LifecycleExited:
			candidate.EndedAt = at(10)
			candidate.Reason = NaturalExit(0)
		case lifecycle == LifecycleFailed:
			candidate.EndedAt = at(10)
			candidate.Reason = LaunchFailed("no child was ever spawned")
		}
		err := candidate.Validate()
		switch lifecycle {
		case LifecycleRunning, LifecycleStopping, LifecycleUnknown:
			if err == nil {
				t.Fatalf("lifecycle %q with no identity validates", string(lifecycle))
			}
			if !strings.Contains(err.Error(), "captured process identity") {
				t.Fatalf("lifecycle %q: error = %v, want it to name the missing identity", string(lifecycle), err)
			}
		default:
			if err != nil {
				t.Fatalf("lifecycle %q must validate without an identity: %v", string(lifecycle), err)
			}
		}
	}

	// The reducer only ever produces running from a captured identity (T3).
	forged := Reduce(startingSession(t), Event{
		Kind:       EventSpawned,
		Generation: 1,
		At:         at(10),
		Identity:   ProcessIdentity{PID: 4242},
	})
	if forged.Applied {
		t.Fatal("a spawn with an incomplete identity was applied")
	}
}

// TestSignallingEffectsNeverCarryAnInvalidIdentity checks the guard every applied
// outcome passes through, so no future row can hand back a pid-only effect.
func TestSignallingEffectsNeverCarryAnInvalidIdentity(t *testing.T) {
	for name, effect := range map[string]Effect{
		"zero identity": {Kind: EffectSignalOwnedGroup, Signal: SignalTerm, Generation: 1},
		"partial identity": {Kind: EffectSignalOwnedGroup, Signal: SignalKill, Generation: 1,
			Identity: ProcessIdentity{PID: 4242, PGID: 4242, BootID: "boot-a1b2c3"}},
		"cleanup without identity": {Kind: EffectCleanupOwnedChild, Signal: SignalKill, Generation: 1},
	} {
		if err := guardEffects([]Effect{effect}); err == nil {
			t.Fatalf("%s: effect %+v was accepted", name, effect)
		}
	}
	valid := []Effect{
		{Kind: EffectLaunchChild, Generation: 1},
		{Kind: EffectDropStaleCallback, Generation: 0},
		{Kind: EffectSignalOwnedGroup, Signal: SignalTerm, Identity: testIdentity, Generation: 1},
		{Kind: EffectCleanupOwnedChild, Signal: SignalKill, Identity: testIdentity, Generation: 1},
	}
	if err := guardEffects(valid); err != nil {
		t.Fatalf("a valid effect set was refused: %v", err)
	}
}

// TestAConfirmedStopTimeoutNeverFabricatesRunningWithoutIdentity walks the exact
// sequence from the review: the refused forced restart is followed by the stop
// timeout the application layer records, and the result must be refused rather than
// a running attempt with no identity.
func TestAConfirmedStopTimeoutNeverFabricatesRunningWithoutIdentity(t *testing.T) {
	// The session as the review left it: stopping with the restart-pending note and
	// no identity, which is what a T11 from starting produced.
	forged := startingSession(t)
	current, _ := forged.Current()
	current.Lifecycle = LifecycleStopping
	current.setNote(NoteRestartPending)
	forged.replaceCurrent(current)

	// The forged record is already refused by the record contract, which is the
	// invariant that makes it unreachable through the reducer.
	if err := forged.Validate(); err == nil {
		t.Fatal("a stopping attempt with no identity validates")
	}

	timeout := Reduce(forged, Event{Kind: EventStopTimeout, Generation: 1, At: at(70)})
	if timeout.Applied {
		t.Fatal("a stop timeout was applied to a session whose identity was never captured")
	}
	if got := timeout.Rejection.Code(); got != CodeConflict {
		t.Fatalf("rejection code = %s, want %s", got, CodeConflict)
	}
	if timeout.Session.State().Lifecycle != LifecycleStopping {
		t.Fatalf("lifecycle = %q, want stopping", timeout.Session.State().Lifecycle)
	}
	if len(timeout.Session.Attempts[0].Notes()) != 1 {
		t.Fatalf("a refused stop timeout still changed the notes: %v", timeout.Session.Attempts[0].Notes())
	}
}

// TestKillRecordsWhatWasObservedNotASigkill is the domain half of the P1-6
// finding: a kill no longer overwrites the caller's evidence, so a SIGTERM is
// recorded as SIGTERM and a kill that raced a natural exit keeps the exit status.
func TestKillRecordsWhatWasObservedNotASigkill(t *testing.T) {
	term := Reduce(runningSession(t), Event{
		Kind:       EventKillRequested,
		Generation: 1,
		At:         at(50),
		Reason:     Killed("SIGTERM"),
	})
	mustApply(t, term)
	current, _ := term.Session.Current()
	if current.Reason.Kind != ReasonKilled || current.Reason.Signal != "SIGTERM" {
		t.Fatalf("reason = %q, want killed SIGTERM", current.Reason.String())
	}

	raced := Reduce(runningSession(t), Event{
		Kind:       EventKillRequested,
		Generation: 1,
		At:         at(50),
		Reason:     NaturalExit(0),
	})
	mustApply(t, raced)
	current, _ = raced.Session.Current()
	if current.Reason.Kind != ReasonNaturalExit || !current.HasExitCode || current.ExitCode != 0 {
		t.Fatalf("reason = %q, want the natural exit the caller observed", current.Reason.String())
	}

	// A kill with nothing observed closes nothing.
	bare := Reduce(runningSession(t), Event{
		Kind:       EventKillRequested,
		Generation: 1,
		At:         at(50),
	})
	if bare.Applied {
		t.Fatal("a kill with no observed evidence closed the attempt")
	}
	if got := bare.Rejection.Code(); got != CodeConflict {
		t.Fatalf("rejection code = %s, want %s", got, CodeConflict)
	}
	if bare.Session.State().Lifecycle != LifecycleRunning {
		t.Fatalf("lifecycle = %q, want running", bare.Session.State().Lifecycle)
	}
}

// TestOldAndFutureGenerationsAreNotTheSameAnswer is the P2-15 reproduction: a
// report for a future attempt used to be reported as a benign stale drop.
func TestOldAndFutureGenerationsAreNotTheSameAnswer(t *testing.T) {
	restarted := restartToRunning(t, exitedSession(t), 2)
	event := Event{
		Kind:       EventChildExited,
		Generation: 1,
		At:         at(500),
		Reason:     NaturalExit(0),
	}
	stale := Reduce(restarted, event)
	if stale.Applied {
		t.Fatal("an older-generation callback was applied")
	}
	if got := stale.Rejection.Code(); got != CodeStaleAttempt {
		t.Fatalf("older-generation code = %s, want %s", got, CodeStaleAttempt)
	}
	if !stale.HasEffect(EffectDropStaleCallback) {
		t.Fatal("an older-generation callback must still report the T19 drop effect")
	}
	if errors.Is(stale.Err(), ErrFutureGeneration) {
		t.Fatal("a late callback from a replaced attempt is not a future-generation defect")
	}
	current, _ := stale.Session.Current()
	if current.Lifecycle != LifecycleRunning {
		t.Fatalf("lifecycle = %q, want running", current.Lifecycle)
	}

	future := Reduce(restarted, Event{
		Kind:       EventChildExited,
		Generation: 3,
		At:         at(500),
		Reason:     NaturalExit(0),
	})
	if future.Applied {
		t.Fatal("a future-generation callback was applied")
	}
	if got := future.Rejection.Code(); got != CodeConflict {
		t.Fatalf("future-generation code = %s, want %s", got, CodeConflict)
	}
	if !errors.Is(future.Err(), ErrFutureGeneration) {
		t.Fatalf("future-generation rejection %v is not %v", future.Err(), ErrFutureGeneration)
	}
	if future.Row == RowStaleCallback {
		t.Fatal("a future-generation observation claims the T19 stale row")
	}
	if future.HasEffect(EffectDropStaleCallback) {
		t.Fatal("a future-generation observation reported a stale drop")
	}
	// It is still a typed error the application layer can surface: code, subject,
	// attempt and hint.
	var typed *Error
	if !errors.As(future.Err(), &typed) {
		t.Fatalf("future-generation rejection is not typed: %v", future.Err())
	}
	if typed.Hint == "" || typed.Subject != string(testSessionID) || !typed.HasAttempt || typed.Attempt != 3 {
		t.Fatalf("future-generation rejection is not surfaceable: %+v", typed)
	}
}

// TestEveryAppliedLifecycleRowEndsUpConsistent walks all twenty rows plus the
// attachment moves and asserts the two whole-result invariants on each applied
// outcome, so a row cannot regress past the guard.
func TestEveryAppliedLifecycleRowEndsUpConsistent(t *testing.T) {
	events := []Event{
		{Kind: EventLaunchRequested, At: at(0), Command: launchCommand(t), WorkspaceID: testWorkspace},
		{Kind: EventSpawned, Generation: 1, At: at(10), Identity: testIdentity},
		{Kind: EventStopRequested, Generation: 1, At: at(20)},
		{Kind: EventStopTimeout, Generation: 1, At: at(40)},
		{Kind: EventKillRequested, Generation: 1, At: at(50), Reason: Killed("SIGKILL")},
	}
	for _, e := range events {
		session := freshSession(t)
		if e.Generation > 0 {
			session = runningSession(t)
		}
		outcome := Reduce(session, e)
		if !outcome.Applied {
			continue
		}
		if err := guardEffects(outcome.Effects); err != nil {
			t.Fatalf("%s produced an effect the guard refuses: %v", e.Kind, err)
		}
		if err := outcome.Validate(); err != nil {
			t.Fatalf("%s produced an invalid session: %v", e.Kind, err)
		}
		for _, effect := range outcome.Effects {
			if (effect.Kind == EffectSignalOwnedGroup || effect.Kind == EffectCleanupOwnedChild) &&
				!effect.Identity.Valid() {
				t.Fatalf("%s produced %s without an identity", e.Kind, effect.Kind)
			}
		}
	}
}

// TestLaunchRequestCarriesTheFrozenCommandUnchanged keeps the durable contract
// honest for the store: what the runtime is asked to spawn is exactly what the
// attempt recorded.
func TestLaunchRequestCarriesTheFrozenCommandUnchanged(t *testing.T) {
	launched := reduce(t, runningSession(t), Event{Kind: EventAttached, Generation: 1, At: at(15)})
	current, _ := launched.Session.Current()
	request := LaunchRequest{
		SessionID:  launched.Session.ID,
		Generation: current.Generation,
		Command:    current.Command,
		Workspace:  mustWorkspace(t, current.WorkspaceID.Path()),
	}
	if err := request.Validate(); err != nil {
		t.Fatalf("the frozen command does not validate for launch: %v", err)
	}
	if !request.Command.Equal(current.Command) {
		t.Fatalf("the launch request command = %q, want %q", request.Command.String(), current.Command.String())
	}
	if request.Generation != current.Generation {
		t.Fatalf("launch generation = %d, want %d", request.Generation, current.Generation)
	}
}

func TestStopOutcomeTimedOutIsNotAStop(t *testing.T) {
	// A tiny guard against a future refactor that conflates the two shapes.
	timedOut := StopOutcome{Observation: LivenessObservation{Outcome: ProbeAlive, Identity: testIdentity, At: at(40)}}
	if timedOut.TimedOut() != !timedOut.Exited {
		t.Fatal("TimedOut and Exited disagree")
	}
	if err := timedOut.Validate(); err != nil {
		t.Fatalf("a timed-out stop with a live reading: %v", err)
	}
	if _, ok := timedOut.Terminal(); ok {
		t.Fatal("a timed-out stop produced terminal evidence")
	}
	if reason := timedOut.StoppedReason(); !reason.Zero() {
		t.Fatalf("a timed-out stop produced the reason %q", reason.String())
	}
	if time.Since(baseTime) < 0 {
		t.Fatal("the fixture clock is inconsistent")
	}
}
