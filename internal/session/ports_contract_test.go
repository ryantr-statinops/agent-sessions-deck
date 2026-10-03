package session

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// These tests cover the contract the domain freezes for Stage 03, Stage 05 and the
// application layer: a stop outcome that reports the evidence of a reap, a reason
// that refuses to record an exit it did not observe, a child runtime port that can
// confirm a force kill and hand out a single exit waiter, and an engine that can
// write a session's user-visible label.

func TestStopOutcomeDistinguishesNoExitCodeFromZero(t *testing.T) {
	if _, observed := (StopOutcome{Exited: true}).ExitStatus(); observed {
		t.Fatal("a stop outcome with no observed exit status reported one")
	}
	if _, observed := (StopOutcome{Exited: true, ExitCode: nil}).ExitStatus(); observed {
		t.Fatal("a nil exit code reported one")
	}
	zero := 0
	code, observed := (StopOutcome{Exited: true, ExitCode: &zero}).ExitStatus()
	if !observed || code != 0 {
		t.Fatalf("exit status = %d/%v, want an observed 0", code, observed)
	}
	// The pointer matters: "exited with code 0" and "no exit code observed" must
	// never compare equal.
	if (StopOutcome{Exited: true, ExitCode: &zero}) == (StopOutcome{Exited: true}) {
		t.Fatal("an observed zero is indistinguishable from an unobserved status")
	}
}

// TestStopOutcomeCarriesTheEvidenceOfASignalReap is the ordinary POSIX case: a
// SIGTERM-killed child is reaped with WIFSIGNALED and no exit code at all, and the
// outcome has to be able to say so.
func TestStopOutcomeCarriesTheEvidenceOfASignalReap(t *testing.T) {
	outcome := StopOutcome{
		Exited:      true,
		Signal:      "SIGTERM",
		Observation: LivenessObservation{Outcome: ProbeGone, At: at(25), Detail: "wait status 0x008b"},
	}
	if err := outcome.Validate(); err != nil {
		t.Fatalf("a reaped child with a signal and a validated observation: %v", err)
	}
	status, reaped := outcome.Terminal()
	if !reaped {
		t.Fatal("a signal reap reported no terminal evidence")
	}
	if status.Signal != "SIGTERM" || status.ExitCode != nil {
		t.Fatalf("terminal evidence = %s, want the signal with no exit code", status.Describe())
	}
	if !status.At.Equal(at(25)) {
		t.Fatalf("terminal evidence instant = %s, want the reap instant", status.At)
	}
	if outcome.TimedOut() {
		t.Fatal("a reaped child reported a stop timeout")
	}
	if err := status.Validate(); err != nil {
		t.Fatalf("terminal evidence: %v", err)
	}
}

func TestStopOutcomeRefusesAReapWithoutEvidence(t *testing.T) {
	// Exited without any of code, signal or evidence is a claim of death with
	// nothing behind it, so it is refused instead of becoming an exited record.
	if err := (StopOutcome{Exited: true}).Validate(); err == nil {
		t.Fatal("a reaped stop with no evidence was accepted")
	}
	// A child that was not reaped may report no status at all.
	if err := (StopOutcome{}).Validate(); err != nil {
		t.Fatalf("a stop timeout with no status: %v", err)
	}
	zero := 0
	if err := (StopOutcome{Exited: false, ExitCode: &zero}).Validate(); err == nil {
		t.Fatal("an unreaped stop reported an exit code")
	}
	if err := (StopOutcome{Exited: false, Signal: "SIGTERM"}).Validate(); err == nil {
		t.Fatal("an unreaped stop reported a terminating signal")
	}
}

func TestStopOutcomeNeverRecordsATimeoutAsAStop(t *testing.T) {
	// T9: the window closed with the child alive, so there is no stopped reason to
	// record; the caller records the timeout instead.
	timedOut := StopOutcome{
		Observation: LivenessObservation{Outcome: ProbeAlive, Identity: testIdentity, At: at(40)},
	}
	if !timedOut.TimedOut() {
		t.Fatal("an unreaped stop did not report a timeout")
	}
	if reason := timedOut.StoppedReason(); !reason.Zero() {
		t.Fatalf("a stop timeout produced the reason %q", reason.String())
	}
	if _, reaped := timedOut.Terminal(); reaped {
		t.Fatal("a stop timeout produced terminal evidence")
	}
}

// TestStoppedReasonValidatesOnRealEvidence keeps the two directions straight: a
// stopped reason needs evidence of the reap, and any one of the three kinds of
// evidence satisfies it.
func TestStoppedReasonValidatesOnRealEvidence(t *testing.T) {
	if err := Stopped(0).Validate(); err != nil {
		t.Fatalf("Stopped(0): %v", err)
	}
	observed := 0
	withCode := Reason{Kind: ReasonStopped, ExitCode: &observed, Detail: "liveness=gone"}
	if err := withCode.Validate(); err != nil {
		t.Fatalf("a stop outcome with an observed code: %v", err)
	}
	if withCode.ExitCode == nil {
		t.Fatal("the recorded reason lost its exit code")
	}
	if err := StoppedBySignal("SIGTERM").Validate(); err != nil {
		t.Fatalf("a signal-killed stop: %v", err)
	}
	withEvidence := Reason{Kind: ReasonStopped, Detail: "reaped pid=4242 status=0x008b"}
	if err := withEvidence.Validate(); err != nil {
		t.Fatalf("a stop whose runtime reported no usable status: %v", err)
	}
	// Nothing at all is not evidence.
	if err := (Reason{Kind: ReasonStopped}).Validate(); err == nil {
		t.Fatal("a stopped reason with no exit code, signal or evidence was accepted")
	}
	if err := (Reason{Kind: ReasonNaturalExit}).Validate(); err == nil {
		t.Fatal("a natural exit with no exit code, signal or evidence was accepted")
	}
	if err := (Reason{Kind: ReasonKilled}).Validate(); err == nil {
		t.Fatal("a kill with no delivered signal and no evidence was accepted")
	}
	// A code and a signal at once is contradictory, not richer evidence.
	both := Reason{Kind: ReasonStopped, ExitCode: &observed, Signal: "SIGTERM"}
	if err := both.Validate(); err == nil {
		t.Fatal("a reason with both an exit code and a signal was accepted")
	}
}

func TestExitStatusAndKillOutcomeRequireConfirmedEvidence(t *testing.T) {
	zero := 0
	status := ExitStatus{ExitCode: &zero, At: at(25)}
	if err := status.Validate(); err != nil {
		t.Fatalf("a reaped exit code: %v", err)
	}
	if !status.Valid() {
		t.Fatal("a valid exit status reported itself invalid")
	}
	reason, err := status.Reason(ReasonKilled)
	if err == nil {
		t.Fatal("a kill reason was built from an exit code")
	}
	if !reason.Zero() {
		t.Fatalf("a refused reason was returned anyway: %q", reason.String())
	}

	// Delivering a signal is not a reap. An outcome that neither confirms the reap
	// nor admits the timeout is ambiguous, so it is refused rather than adopted.
	delivered := KillOutcome{Delivered: true, Signal: "SIGKILL"}
	if err := delivered.Validate(); err == nil {
		t.Fatal("a delivered signal with neither a confirmation nor a timeout was accepted")
	}
	if _, confirmed := delivered.Terminal(); confirmed {
		t.Fatal("a delivered signal confirmed an exit")
	}
	if _, err := delivered.KilledReason(); err == nil {
		t.Fatal("a delivered signal produced a killed reason")
	}

	confirmed := KillOutcome{Delivered: true, Signal: "SIGKILL",
		Terminated: ExitStatus{Signal: "SIGKILL", At: at(25), Evidence: "reaped pid=4242"}}
	if err := confirmed.Validate(); err != nil {
		t.Fatalf("a confirmed kill: %v", err)
	}
	killed, err := confirmed.KilledReason()
	if err != nil {
		t.Fatalf("KilledReason: %v", err)
	}
	if killed.Kind != ReasonKilled || killed.Signal != "SIGKILL" {
		t.Fatalf("killed reason = %q, want killed SIGKILL", killed.String())
	}

	// A timed-out kill reports the delivery and no terminal evidence at all.
	timedOut := KillOutcome{Delivered: true, Signal: "SIGKILL", Timeout: true}
	if err := timedOut.Validate(); err != nil {
		t.Fatalf("a timed-out kill: %v", err)
	}
	if timedOut.Terminated.hasEvidence() {
		t.Fatal("a timed-out kill carries terminal evidence")
	}
	if err := (KillOutcome{Delivered: true, Signal: "SIGKILL", Timeout: true,
		Terminated: ExitStatus{Signal: "SIGKILL", At: at(25)}}).Validate(); err == nil {
		t.Fatal("a timed-out kill with terminal evidence was accepted")
	}
}

// TestExitStatusNeedsAnInstant keeps a reap timestampable: an observation with no
// instant cannot be ordered against the rest of the record.
func TestExitStatusNeedsAnInstant(t *testing.T) {
	zero := 0
	if err := (ExitStatus{ExitCode: &zero}).Validate(); err == nil {
		t.Fatal("a reap with no instant was accepted")
	}
	if err := (ExitStatus{At: at(25)}).Validate(); err == nil {
		t.Fatal("a reap with no evidence was accepted")
	}
}

// TestExitWaitersGrantExactlyOneWaiterPerChild makes ADR 0003's single-waiter rule
// checkable instead of conventional.
func TestExitWaitersGrantExactlyOneWaiterPerChild(t *testing.T) {
	waiters := NewExitWaiters()
	wait := ExitWait{
		SessionID:  testSessionID,
		Generation: 1,
		Identity:   testIdentity,
		Waiter:     "owner-a",
	}
	claim, err := waiters.Claim(wait)
	if err != nil {
		t.Fatalf("the first claim was refused: %v", err)
	}
	if claim.Wait() != wait {
		t.Fatalf("claim wait = %+v, want %+v", claim.Wait(), wait)
	}

	duplicate, err := waiters.Claim(ExitWait{
		SessionID:  testSessionID,
		Generation: 1,
		Identity:   testIdentity,
		Waiter:     "owner-b",
	})
	if err == nil {
		t.Fatalf("a second waiter on the same child was granted: %+v", duplicate)
	}
	if got := CodeOf(err); got != CodeConflict {
		t.Fatalf("second claim code = %s, want %s", got, CodeConflict)
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.Attempt != 1 {
		t.Fatalf("second claim does not name the attempt that holds it: %v", err)
	}

	// A restart is a new child under the same session id, so it must be waited on
	// by the same single waiter until that one releases.
	waiters.Release(claim)
	if _, held := waiters.Holder(testSessionID); held {
		t.Fatal("the released claim is still held")
	}
	wait.Generation = 2
	if _, err := waiters.Claim(wait); err != nil {
		t.Fatalf("a claim after release was refused: %v", err)
	}
	// Releasing twice is not an error: a waiter that already finished must still be
	// able to release.
	waiters.Release(claim)
	waiters.Release(claim)
}

func TestExitWaitersRefuseClaimsThatCannotBeVerified(t *testing.T) {
	waiters := NewExitWaiters()
	for name, wait := range map[string]ExitWait{
		"no session id":   {Generation: 1, Identity: testIdentity, Waiter: "owner-a"},
		"no generation":   {SessionID: testSessionID, Identity: testIdentity, Waiter: "owner-a"},
		"unverified pid":  {SessionID: testSessionID, Generation: 1, Identity: ProcessIdentity{PID: 4242}, Waiter: "owner-a"},
		"no waiter":       {SessionID: testSessionID, Generation: 1, Identity: testIdentity},
		"zero generation": {SessionID: testSessionID, Identity: testIdentity, Waiter: "owner-a"},
	} {
		if _, err := waiters.Claim(wait); err == nil {
			t.Fatalf("%s: an unverifiable wait was granted", name)
		}
	}
	if _, held := waiters.Holder(testSessionID); held {
		t.Fatal("a refused claim took the waiter slot anyway")
	}
}

func TestEngineRenameChangesOnlyTheLabel(t *testing.T) {
	engine, err := NewEngine(runningSession(t))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	before := mustGet(t, engine, testSessionID)
	if before.Name == "kestrel-2" {
		t.Fatal("the fixture already carries the new name")
	}

	renamed, err := engine.Rename(testSessionID, "kestrel-2", at(90))
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if renamed.Name != "kestrel-2" {
		t.Fatalf("name = %q, want kestrel-2", renamed.Name)
	}
	if renamed.ID != before.ID || renamed.AgentID != before.AgentID || renamed.WorkspaceID != before.WorkspaceID {
		t.Fatalf("rename changed the identity: %+v", renamed)
	}
	if renamed.Generation != before.Generation || renamed.AttemptCount() != before.AttemptCount() {
		t.Fatalf("rename changed the attempt history: %+v", renamed)
	}
	if renamed.State() != before.State() {
		t.Fatalf("rename changed the observable triple: %+v -> %+v", before.State(), renamed.State())
	}
	if !renamed.UpdatedAt.Equal(at(90)) {
		t.Fatalf("updated-at = %s, want the supplied instant", renamed.UpdatedAt)
	}
	if renamed.UpdatedAt.Location() != time.UTC {
		t.Fatalf("updated-at is not UTC: %s", renamed.UpdatedAt)
	}
	// The stored copy is the renamed one, not just the returned value.
	if stored := mustGet(t, engine, testSessionID); stored.Name != "kestrel-2" {
		t.Fatalf("stored name = %q, want kestrel-2", stored.Name)
	}
}

func TestEngineRenameRefusesAnUnusableNameAndLeavesTheRecord(t *testing.T) {
	engine, err := NewEngine(runningSession(t))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	long := strings.Repeat("x", maxNameLen+1)
	tests := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "padded", value: " kestrel "},
		{name: "control character", value: "kestrel\n"},
		{name: "too long", value: long},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := engine.Rename(testSessionID, tt.value, at(91)); err == nil {
				t.Fatal("an unusable name was accepted")
			} else {
				var typed *Error
				if !errors.As(err, &typed) || typed.Code != CodeInvalidConfiguration {
					t.Fatalf("error = %v, want a typed INVALID_CONFIGURATION", err)
				}
				if typed.Hint == "" {
					t.Fatal("the refusal carries no hint")
				}
			}
			if got := mustGet(t, engine, testSessionID); got.Name == tt.value && tt.value != "" {
				t.Fatalf("the refused name %q was stored anyway", tt.value)
			}
		})
	}
}

func TestEngineRenameRefusesAnUnregisteredSessionAndAMissingInstant(t *testing.T) {
	engine, err := NewEngine(runningSession(t))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if _, err := engine.Rename(ID("s-missing"), "kestrel-2", at(92)); CodeOf(err) != CodeNotFound {
		t.Fatalf("error = %v, want NOT_FOUND", err)
	}
	if _, err := engine.Rename(testSessionID, "kestrel-2", time.Time{}); CodeOf(err) != CodeInvalidConfiguration {
		t.Fatalf("error = %v, want INVALID_CONFIGURATION", err)
	}
}

func TestEngineRenameIsSafeUnderConcurrentLifecycleEvents(t *testing.T) {
	engine, err := NewEngine(runningSession(t))
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		// A rename must not race a lifecycle event into a half-applied state.
		for i := 0; i < 50; i++ {
			if _, err := engine.Rename(testSessionID, "kestrel-2", at(100+i)); err != nil {
				t.Errorf("Rename: %v", err)
				return
			}
		}
	}()
	for i := 0; i < 50; i++ {
		if _, found := engine.Get(testSessionID); !found {
			t.Fatal("the session vanished during a rename")
		}
	}
	<-done
	if err := mustGet(t, engine, testSessionID).Validate(); err != nil {
		t.Fatalf("the session is inconsistent after concurrent renames: %v", err)
	}
}
