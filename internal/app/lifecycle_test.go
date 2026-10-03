package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestOpenAcquiresExactlyOneInteractiveLease(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)

	opened, err := h.svc.Open(context.Background(), OpenRequest{Ref: string(created.Session.ID), Holder: "cli-1"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.Session.Attachment != session.AttachmentAttached {
		t.Fatalf("attachment = %s, want attached", opened.Session.Attachment)
	}
	if opened.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("a lease must not change the lifecycle: %s", opened.Session.Lifecycle)
	}
	if opened.Lease.Holder != "cli-1" || opened.Lease.Generation != 1 {
		t.Fatalf("lease = %+v, want generation 1 held by cli-1", opened.Lease)
	}
	terminal, ok := h.terminal.last()
	if !ok {
		t.Fatal("no terminal stream was handed out")
	}
	if terminal.Holder() != "cli-1" || terminal.Generation() != 1 {
		t.Fatalf("stream = %s@%d, want cli-1@1", terminal.Holder(), terminal.Generation())
	}
	// A second client is refused by the reducer before the broker is asked, so the
	// running client keeps its lease.
	_, err = h.svc.Open(context.Background(), OpenRequest{Ref: string(created.Session.ID), Holder: "cli-2"})
	requireCode(t, err, session.CodeConflict)
	holder, held := h.broker.holder(created.Session.ID)
	if !held || holder != "cli-1" {
		t.Fatalf("lease holder = %q/%v, want cli-1 still holding it", holder, held)
	}
}

func TestOpenReportsUnavailableIOAndLeavesTheProcessUntouched(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	// A PTY loss with verified liveness is an orphan: running, no I/O.
	if _, err := h.svc.Report(context.Background(), ReportRequest{
		Ref:  string(created.Session.ID),
		Kind: ReportPTYLost,
		Liveness: session.LivenessObservation{
			Outcome:  session.ProbeAlive,
			Identity: testIdentity(4242),
			At:       baseTime,
		},
	}); err != nil {
		t.Fatalf("Report pty-lost: %v", err)
	}

	_, err := h.svc.Open(context.Background(), OpenRequest{Ref: string(created.Session.ID), Holder: "cli-1"})
	typed := requireCode(t, err, session.CodeSessionIOFailed)
	if typed.Hint == "" {
		t.Fatal("the I/O failure must carry re-attach guidance")
	}
	if _, held := h.broker.holder(created.Session.ID); held {
		t.Fatal("a refused open must not take a lease")
	}
	if terminal, ok := h.terminal.last(); ok {
		t.Fatalf("a refused open handed out a stream: %+v", terminal)
	}
	if len(h.runtime.signalKinds()) != 0 {
		t.Fatal("a refused open signalled the process")
	}
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Lifecycle != session.LifecycleRunning || !live.Session.Orphaned() {
		t.Fatalf("the refused open changed the session: %+v", live.Session)
	}
}

func TestOpenReportsUnavailableIOWhenTheStreamCannotBeHandedOut(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.terminal.subscribeErr = errors.New("pty allocation failed")

	_, err := h.svc.Open(context.Background(), OpenRequest{Ref: string(created.Session.ID), Holder: "cli-1"})
	requireCode(t, err, session.CodeSessionIOFailed)
	if _, held := h.broker.holder(created.Session.ID); held {
		t.Fatal("a stream failure must release the lease it took")
	}
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Attachment != session.AttachmentDetached {
		t.Fatalf("attachment = %s, want the rollback to detached", live.Session.Attachment)
	}
}

func TestDetachReleasesTheLeaseAndChangesOnlyTheAttachment(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	opened, err := h.svc.Open(context.Background(), OpenRequest{Ref: string(created.Session.ID), Holder: "cli-1"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	before := opened.Session

	detached, err := h.svc.Detach(context.Background(), DetachRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Detach: %v", err)
	}
	if detached.Session.Attachment != session.AttachmentDetached {
		t.Fatalf("attachment = %s, want detached", detached.Session.Attachment)
	}
	if detached.Session.Lifecycle != before.Lifecycle || detached.Session.Generation != before.Generation {
		t.Fatalf("detach changed more than the attachment: %+v", detached.Session)
	}
	if _, held := h.broker.holder(created.Session.ID); held {
		t.Fatal("detach must release the lease")
	}
	if terminal, ok := h.terminal.last(); !ok || terminal.closeCount() != 1 {
		t.Fatalf("the stream was not closed exactly once")
	}
	if types := h.capture.types(); types[len(types)-1] != string(events.TypeSessionDetached) {
		t.Fatalf("published events = %v, want the detach last", types)
	}
}

func TestDetachRefusesWhenNothingIsAttached(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	_, err := h.svc.Detach(context.Background(), DetachRequest{Ref: string(created.Session.ID)})
	requireCode(t, err, session.CodeConflict)
	if types := h.capture.types(); len(types) != 1 {
		t.Fatalf("published events = %v, want only the creation event", types)
	}
}

func TestStopIsGracefulAndRecordsTheExit(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(0)}

	stopped, err := h.svc.Stop(context.Background(), StopRequest{Ref: string(created.Session.ID), Grace: time.Second})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !stopped.Stopped || stopped.TimedOut {
		t.Fatalf("result = %+v, want a plain stop", stopped)
	}
	if stopped.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("lifecycle = %s, want exited", stopped.Session.Lifecycle)
	}
	code, observed := stopped.Session.Reason.ExitStatus()
	if !observed || code != 0 {
		t.Fatalf("recorded exit status = %d/%v, want 0", code, observed)
	}
	if stopped.Session.Reason.Kind != session.ReasonStopped {
		t.Fatalf("reason = %s, want stopped", stopped.Session.Reason.Kind)
	}
	if types := h.capture.types(); types[len(types)-1] != string(events.TypeSessionStopped) {
		t.Fatalf("published events = %v, want the stop last", types)
	}
	// A graceful stop is a SIGTERM to the verified group, never a SIGKILL.
	if signals := h.runtime.signalKinds(); len(signals) != 0 {
		t.Fatalf("Stop signalled directly: %v", signals)
	}
	if len(h.runtime.stops) != 1 || h.runtime.stops[0] != testIdentity(4242) {
		t.Fatalf("stop calls = %v, want the recorded identity", h.runtime.stops)
	}
}

func TestStopTimeoutKeepsTheSessionRunningAndOffersKillGuidance(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.runtime.stop = session.StopOutcome{
		Exited: false,
		Observation: session.LivenessObservation{
			Outcome:  session.ProbeAlive,
			Identity: testIdentity(4242),
			At:       baseTime,
		},
	}

	stopped, err := h.svc.Stop(context.Background(), StopRequest{Ref: string(created.Session.ID), Grace: 10 * time.Millisecond})
	typed := requireCode(t, err, session.CodeConflict)
	if typed.Subject != string(created.Session.ID) {
		t.Fatalf("subject = %q, want the session id", typed.Subject)
	}
	if !contains(typed.Reason, "still running") {
		t.Fatalf("reason = %q, must say the session is still running", typed.Reason)
	}
	if !contains(typed.Hint, "kill") {
		t.Fatalf("hint = %q, must name the explicit kill", typed.Hint)
	}
	if !stopped.TimedOut || stopped.Stopped {
		t.Fatalf("result = %+v, want a timeout and no stop", stopped)
	}
	if stopped.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("lifecycle = %s, want running: a timeout is never a kill", stopped.Session.Lifecycle)
	}
	if stopped.Session.Reason.Kind == session.ReasonKilled {
		t.Fatal("a stop timeout was recorded as killed")
	}
	if !slices.Contains(stopped.Session.Notes, session.NoteStopTimeout) {
		t.Fatalf("notes = %v, want the stop-timeout note", stopped.Session.Notes)
	}
	if !slices.Contains(stopped.Session.Notes, session.NoteKillGuidance) {
		t.Fatalf("notes = %v, want the kill guidance note", stopped.Session.Notes)
	}
	if signals := h.runtime.signalKinds(); len(signals) != 0 {
		t.Fatalf("a stop timeout escalated on its own: %v", signals)
	}
	if types := h.capture.types(); types[len(types)-1] != string(TypeSessionStopTimeout) {
		t.Fatalf("published events = %v, want the stop timeout last", types)
	}
}

func TestStopTreatsAnUnobservableChildAsStillRunning(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.runtime.stopErr = errors.New("wait4: no child processes")

	stopped, err := h.svc.Stop(context.Background(), StopRequest{Ref: string(created.Session.ID)})
	typed := requireCode(t, err, session.CodeConflict)
	if !contains(typed.Reason, "still running") {
		t.Fatalf("reason = %q, must say the session is still running", typed.Reason)
	}
	if !stopped.TimedOut || stopped.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("result = %+v, want the session left running", stopped)
	}
}

func TestKillIsExplicitAndSignalsTheVerifiedGroup(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)

	killed, err := h.svc.Kill(context.Background(), KillRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if killed.Signal != session.SignalKill {
		t.Fatalf("signal = %s, want SIGKILL", killed.Signal)
	}
	if killed.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("lifecycle = %s, want exited", killed.Session.Lifecycle)
	}
	if killed.Session.Reason.Kind != session.ReasonKilled {
		t.Fatalf("reason = %s, want killed", killed.Session.Reason.Kind)
	}
	if killed.Session.Reason.ExitCode != nil && killed.Session.Reason.Signal == "" {
		t.Fatalf("the kill recorded no evidence: %s", killed.Session.Reason)
	}
	kills := h.runtime.forcedKills()
	if len(kills) != 1 || kills[0] != testIdentity(4242) {
		t.Fatalf("force kills = %v, want one SIGKILL of the recorded identity", kills)
	}
	if types := h.capture.types(); types[len(types)-1] != string(events.TypeSessionKilled) {
		t.Fatalf("published events = %v, want the kill last", types)
	}
}

func TestKillRecordsOnlyAConfirmedReap(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	// The signal reached the verified group, but the runtime could not confirm the
	// reap inside the window.
	h.runtime.kill = killDeliveredOnly()

	result, err := h.svc.Kill(context.Background(), KillRequest{Ref: string(created.Session.ID)})
	typed := requireCode(t, err, session.CodeConflict)
	if !contains(typed.Reason, "still recorded as running") {
		t.Fatalf("reason = %q, must say the session is still running", typed.Reason)
	}
	if typed.Hint == "" {
		t.Fatal("the refusal must say what to do next")
	}
	if result.Session.Lifecycle == session.LifecycleExited {
		t.Fatalf("a delivered signal was recorded as an exit: %+v", result.Session)
	}
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("lifecycle = %s, want running: only a reap may close an attempt", live.Session.Lifecycle)
	}
	stored, found := h.store.find(created.Session.ID)
	if !found {
		t.Fatal("the running session was not persisted")
	}
	if attempt, _ := stored.Current(); attempt.Reason.Kind == session.ReasonKilled {
		t.Fatal("the persisted record claims a kill that was never confirmed")
	}
	if len(h.capture.all()) != 1 {
		t.Fatalf("published events = %v, want only the creation event", h.capture.types())
	}
	// The attempt is still open, so the owner's waiter can still complete the
	// record with the reap it observes.
	reported, err := h.svc.Report(ctx(), ReportRequest{
		Ref:    string(created.Session.ID),
		Kind:   ReportChildExited,
		Reason: session.Killed("SIGKILL"),
	})
	if err != nil {
		t.Fatalf("Report after an unconfirmed kill: %v", err)
	}
	if reported.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("lifecycle = %s, want exited once the reap is observed", reported.Session.Lifecycle)
	}
	if reported.Session.Reason.Kind != session.ReasonKilled {
		t.Fatalf("reason = %s, want killed", reported.Session.Reason.Kind)
	}
}

func TestKillAcceptsAForcedKillOnly(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)

	// SIGTERM is a graceful stop: the two are different operations and the record
	// has to say which one happened.
	_, err := h.svc.Kill(context.Background(), KillRequest{Ref: string(created.Session.ID), Signal: session.SignalTerm})
	typed := requireCode(t, err, session.CodeInvalidConfiguration)
	if !contains(typed.Reason, "graceful stop") {
		t.Fatalf("reason = %q, must name the graceful stop", typed.Reason)
	}
	if len(h.runtime.forcedKills()) != 0 {
		t.Fatal("a refused kill signalled the process group")
	}
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("lifecycle = %s, want running", live.Session.Lifecycle)
	}
}

func TestKillLeavesTheSessionUntouchedWhenTheForcedKillIsRefused(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.runtime.killErr = errors.New("operation not permitted")

	_, err := h.svc.Kill(context.Background(), KillRequest{Ref: string(created.Session.ID)})
	requireCode(t, err, session.CodePermissionDenied)
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("lifecycle = %s, want running: nothing may claim a kill that failed", live.Session.Lifecycle)
	}
	if len(h.capture.all()) != 1 {
		t.Fatalf("published events = %v, want only the creation event", h.capture.types())
	}
}

func TestRestartKeepsTheSessionIDAndIncrementsTheGeneration(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	// A finished attempt needs no force: restarting re-runs the resolved command.
	if _, err := h.svc.Kill(context.Background(), KillRequest{Ref: string(created.Session.ID)}); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	restarted, err := h.svc.Restart(context.Background(), RestartRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if restarted.Session.ID != created.Session.ID {
		t.Fatalf("session id = %q, want the unchanged %q", restarted.Session.ID, created.Session.ID)
	}
	if restarted.PreviousGeneration != 1 || restarted.Session.Generation != 2 {
		t.Fatalf("generations = %d -> %d, want 1 -> 2", restarted.PreviousGeneration, restarted.Session.Generation)
	}
	if restarted.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("lifecycle = %s, want running", restarted.Session.Lifecycle)
	}
	if restarted.Session.AttemptCount != 2 {
		t.Fatalf("attempt count = %d, want 2", restarted.Session.AttemptCount)
	}
	launch, ok := h.runtime.lastLaunch()
	if !ok || launch.Generation != 2 || launch.SessionID != created.Session.ID {
		t.Fatalf("launch = %+v, want generation 2 of %q", launch, created.Session.ID)
	}
	if types := h.capture.types(); types[len(types)-1] != string(events.TypeSessionRestarted) {
		t.Fatalf("published events = %v, want the restart last", types)
	}
	captured := h.capture.all()
	if captured[len(captured)-1].spec.Attempt != events.AttemptGeneration(2) {
		t.Fatalf("event attempt = %d, want 2", captured[len(captured)-1].spec.Attempt)
	}
}

func TestRestartOfALiveAttemptNeedsForce(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)

	_, err := h.svc.Restart(context.Background(), RestartRequest{Ref: string(created.Session.ID)})
	typed := requireCode(t, err, session.CodeConflict)
	if !contains(typed.Reason, "force") {
		t.Fatalf("reason = %q, must name the missing force confirmation", typed.Reason)
	}
	if h.runtime.launchCount() != 1 {
		t.Fatal("a refused restart spawned a second attempt")
	}
	if len(h.runtime.stops) != 0 {
		t.Fatal("a refused restart signalled the live child")
	}
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Generation != 1 || live.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("the refused restart changed the session: %+v", live.Session)
	}
}

func TestForcedRestartStopsTheOldAttemptBeforeOpeningTheNext(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(143)}

	restarted, err := h.svc.Restart(context.Background(), RestartRequest{Ref: string(created.Session.ID), Force: true})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if restarted.Session.Generation != 2 {
		t.Fatalf("generation = %d, want 2", restarted.Session.Generation)
	}
	if len(h.runtime.stops) != 1 || h.runtime.stops[0] != testIdentity(4242) {
		t.Fatalf("stop calls = %v, want the recorded identity once", h.runtime.stops)
	}
	if h.runtime.launchCount() != 2 {
		t.Fatalf("launches = %d, want the initial one plus the restart", h.runtime.launchCount())
	}
	stored, found := h.store.find(created.Session.ID)
	if !found {
		t.Fatal("the restarted session was not persisted")
	}
	first, ok := stored.FindAttempt(1)
	if !ok {
		t.Fatal("the first attempt is gone; its exit reason is immutable history")
	}
	if first.Lifecycle != session.LifecycleExited || !first.HasNote(session.NoteRestartPending) {
		t.Fatalf("first attempt = %+v, want exited and restart-pending", first)
	}
	second, ok := stored.FindAttempt(2)
	if !ok || second.Lifecycle != session.LifecycleRunning {
		t.Fatalf("second attempt = %+v, want running", second)
	}
}

func TestForcedRestartLeavesTheOldAttemptRunningWhenTheStopTimesOut(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.runtime.stop = session.StopOutcome{
		Exited: false,
		Observation: session.LivenessObservation{
			Outcome:  session.ProbeAlive,
			Identity: testIdentity(4242),
			At:       baseTime,
		},
	}

	result, err := h.svc.Restart(context.Background(), RestartRequest{Ref: string(created.Session.ID), Force: true})
	requireCode(t, err, session.CodeConflict)
	if result.Session.Lifecycle != session.LifecycleRunning || result.Session.Generation != 1 {
		t.Fatalf("result = %+v, want the first attempt still running", result.Session)
	}
	if h.runtime.launchCount() != 1 {
		t.Fatal("an unfinished restart opened a second attempt")
	}
}

func TestStaleCallbackFromAnOlderAttemptIsReportedNotRaised(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	if _, err := h.svc.Kill(context.Background(), KillRequest{Ref: string(created.Session.ID)}); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	restarted, err := h.svc.Restart(context.Background(), RestartRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if restarted.Session.Generation != 2 {
		t.Fatalf("generation = %d, want 2", restarted.Session.Generation)
	}
	publishedBefore := len(h.capture.all())

	// The old attempt's callback finally arrives. It must be fenced out.
	result, err := h.svc.Report(context.Background(), ReportRequest{
		Ref:     string(created.Session.ID),
		Attempt: 1,
		Kind:    ReportChildExited,
		Reason:  session.NaturalExit(0),
	})
	if err != nil {
		t.Fatalf("a stale callback must not be an error: %v", err)
	}
	if !result.Stale || result.Row != session.RowStaleCallback {
		t.Fatalf("result = %+v, want the stale-callback row", result)
	}
	if result.Session.Lifecycle != session.LifecycleRunning || result.Session.Generation != 2 {
		t.Fatalf("the stale callback changed the session: %+v", result.Session)
	}
	if len(h.capture.all()) != publishedBefore {
		t.Fatal("a dropped callback published an event")
	}
	if h.svc.Revision() != restarted.Revision {
		t.Fatalf("revision = %d, want the unchanged %d", h.svc.Revision(), restarted.Revision)
	}
}

func TestReportAppliesAnObservationForTheCurrentAttempt(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)

	result, err := h.svc.Report(context.Background(), ReportRequest{
		Ref:  string(created.Session.ID),
		Kind: ReportProbeInconclusive,
		Liveness: session.LivenessObservation{
			Outcome: session.ProbeUnverifiable,
			Detail:  "permission-denied",
			At:      baseTime,
		},
	})
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if result.Session.Lifecycle != session.LifecycleUnknown {
		t.Fatalf("lifecycle = %s, want unknown", result.Session.Lifecycle)
	}
	if result.Row != session.RowProbeInconclusive {
		t.Fatalf("row = %q, want %q", result.Row, session.RowProbeInconclusive)
	}
	if result.Session.Reason.Kind != session.ReasonPermissionDenied {
		t.Fatalf("reason = %s, want permission-denied", result.Session.Reason.Kind)
	}
}

func TestReportRequiresTheEvidenceItsKindNeeds(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	tests := []struct {
		name string
		req  ReportRequest
	}{
		{
			name: "unknown kind",
			req:  ReportRequest{Ref: string(created.Session.ID), Kind: "hallucinated"},
		},
		{
			name: "child exit without a terminal reason",
			req:  ReportRequest{Ref: string(created.Session.ID), Kind: ReportChildExited},
		},
		{
			name: "liveness observation without an instant",
			req: ReportRequest{
				Ref:  string(created.Session.ID),
				Kind: ReportReconciled,
				Liveness: session.LivenessObservation{
					Outcome: session.ProbeGone,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.svc.Report(context.Background(), tt.req)
			requireCode(t, err, session.CodeInvalidConfiguration)
		})
	}
}

func TestDeleteRefusesAnActiveSessionAndKeepsItsRecord(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)

	_, err := h.svc.Delete(context.Background(), DeleteRequest{Ref: string(created.Session.ID)})
	typed := requireCode(t, err, session.CodeConflict)
	if !contains(typed.Reason, "active") {
		t.Fatalf("reason = %q, must say the lifecycle is active", typed.Reason)
	}
	if typed.Attempt != 1 {
		t.Fatalf("error attempt = %d, want 1", typed.Attempt)
	}
	if h.store.count() != 1 {
		t.Fatal("a refused delete removed the record")
	}
	if types := h.capture.types(); len(types) != 1 {
		t.Fatalf("published events = %v, want only the creation event", types)
	}
}

func TestDeleteRemovesAFinishedSession(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	if _, err := h.svc.Kill(context.Background(), KillRequest{Ref: string(created.Session.ID)}); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	deleted, err := h.svc.Delete(context.Background(), DeleteRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleted.ID != created.Session.ID {
		t.Fatalf("deleted id = %q, want %q", deleted.ID, created.Session.ID)
	}
	if h.store.count() != 0 {
		t.Fatalf("stored sessions = %d, want 0", h.store.count())
	}
	if types := h.capture.types(); types[len(types)-1] != string(TypeSessionDeleted) {
		t.Fatalf("published events = %v, want the deletion last", types)
	}
}

func TestDeleteRefusesAnOrphanedSession(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	if _, err := h.svc.Report(context.Background(), ReportRequest{
		Ref:  string(created.Session.ID),
		Kind: ReportPTYLost,
		Liveness: session.LivenessObservation{
			Outcome:  session.ProbeAlive,
			Identity: testIdentity(4242),
			At:       baseTime,
		},
	}); err != nil {
		t.Fatalf("Report: %v", err)
	}
	_, err := h.svc.Delete(context.Background(), DeleteRequest{Ref: string(created.Session.ID)})
	requireCode(t, err, session.CodeConflict)
	if h.store.count() != 1 {
		t.Fatal("an orphan's metadata was deleted, stranding a live process")
	}
}

func TestAmbiguousPrefixIsRefusedRatherThanGuessed(t *testing.T) {
	h := newHarness(t)
	h.createRunning(t)
	// A second session shares the first one's prefix.
	h.registry.providers = append(h.registry.providers, newFakeProvider(t, "codex", launchBin))
	h.runtime.identity = testIdentity(5150)
	second, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "codex",
		WorkspacePath: secondWork,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	prefix := string(second.Session.ID[0])

	_, err = h.svc.Get(context.Background(), GetRequest{Ref: prefix})
	typed := requireCode(t, err, session.CodeConflict)
	if !contains(typed.Reason, "ambiguous") {
		t.Fatalf("reason = %q, must say the prefix is ambiguous", typed.Reason)
	}
}

func TestOpenReportsALeaseTheBrokerRefuses(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.broker.acquireErr = session.NewError(session.CodeConflict, string(created.Session.ID),
		"the interactive lease is held by another-owner", "wait for that client to detach")

	_, err := h.svc.Open(context.Background(), OpenRequest{Ref: string(created.Session.ID), Holder: "cli-2"})
	typed := requireCode(t, err, session.CodeConflict)
	if !contains(typed.Hint, "detach") {
		t.Fatalf("hint = %q, must tell the caller to wait for the holder", typed.Hint)
	}
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Attachment != session.AttachmentDetached {
		t.Fatalf("attachment = %s, want the refused attach to change nothing", live.Session.Attachment)
	}
	if types := h.capture.types(); len(types) != 1 {
		t.Fatalf("published events = %v, want only the creation event", types)
	}
}

func TestRestartRecordsTheNewAttemptWhenTheSpawnFails(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	if _, err := h.svc.Kill(context.Background(), KillRequest{Ref: string(created.Session.ID)}); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	h.runtime.launchErr = errors.New("fork/exec: permission denied")

	result, err := h.svc.Restart(context.Background(), RestartRequest{Ref: string(created.Session.ID)})
	typed := requireCode(t, err, session.CodeLaunchFailed)
	if typed.Attempt != 2 {
		t.Fatalf("error attempt = %d, want the new generation 2", typed.Attempt)
	}
	if result.Session.ID != created.Session.ID {
		t.Fatalf("session id = %q, want the unchanged %q", result.Session.ID, created.Session.ID)
	}
	if result.PreviousGeneration != 1 || result.Session.Generation != 2 {
		t.Fatalf("generations = %d -> %d, want 1 -> 2", result.PreviousGeneration, result.Session.Generation)
	}
	if result.Session.Lifecycle != session.LifecycleFailed {
		t.Fatalf("lifecycle = %s, want failed", result.Session.Lifecycle)
	}
	stored, _ := h.store.find(created.Session.ID)
	if first, ok := stored.FindAttempt(1); !ok || first.Lifecycle != session.LifecycleExited {
		t.Fatalf("the first attempt was rewritten: %+v", first)
	}
	if types := h.capture.types(); types[len(types)-1] != string(TypeSessionLaunchFailed) {
		t.Fatalf("published events = %v, want the launch failure last", types)
	}
}

func TestRestartRefusesAnOrphanedSession(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	if _, err := h.svc.Report(context.Background(), ReportRequest{
		Ref:  string(created.Session.ID),
		Kind: ReportPTYLost,
		Liveness: session.LivenessObservation{
			Outcome:  session.ProbeAlive,
			Identity: testIdentity(4242),
			At:       baseTime,
		},
	}); err != nil {
		t.Fatalf("Report: %v", err)
	}
	_, err := h.svc.Restart(context.Background(), RestartRequest{Ref: string(created.Session.ID), Force: true})
	typed := requireCode(t, err, session.CodeConflict)
	if !contains(typed.Reason, "surviving process") {
		t.Fatalf("reason = %q, must name the unowned surviving process", typed.Reason)
	}
	if h.runtime.launchCount() != 1 {
		t.Fatal("a refused orphan restart spawned a second attempt")
	}
}

func TestRestartRefusesASessionWithoutAnAttempt(t *testing.T) {
	stored := sessionWithoutAttempt(t, "s-000009")
	h := newHarness(t, withSeed(stored))
	_, err := h.svc.Restart(context.Background(), RestartRequest{Ref: "s-000009"})
	requireCode(t, err, session.CodeConflict)
	if h.runtime.launchCount() != 0 {
		t.Fatal("a refused restart spawned something")
	}
}

func TestRestartReresolvesTheCommandForAnAttemptThatNeverHadOne(t *testing.T) {
	stored := commandlessFailedSession(t, "s-000010")
	h := newHarness(t, withSeed(stored))

	restarted, err := h.svc.Restart(context.Background(), RestartRequest{Ref: "s-000010"})
	if err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if restarted.Session.Generation != 2 || restarted.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("restarted = %+v, want a running generation 2", restarted.Session)
	}
	if restarted.Session.Command.Executable() != launchBin {
		t.Fatalf("resolved executable = %q, want %q", restarted.Session.Command.Executable(), launchBin)
	}
	launch, ok := h.runtime.lastLaunch()
	if !ok || launch.Workspace.Path() != testWorkspace {
		t.Fatalf("launch = %+v, want the stored workspace", launch)
	}
}

// sessionWithoutAttempt builds a stored record with no attempt at all.
func sessionWithoutAttempt(t *testing.T, id string) session.Session {
	t.Helper()
	built, err := session.New(session.ID(id), "kestrel", "opencode", workspace.ID(testWorkspace), baseTime)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return built
}

// commandlessFailedSession builds a stored record whose only attempt failed
// before it ever resolved a command, which is the T1 shape.
func commandlessFailedSession(t *testing.T, id string) session.Session {
	t.Helper()
	outcome := session.ApplyAll(sessionWithoutAttempt(t, id), session.Event{
		Kind:       session.EventLaunchRequested,
		Generation: 0,
		At:         baseTime,
		Failure:    "the configured command could not be resolved",
	})
	if err := outcome.Session.Validate(); err != nil {
		t.Fatalf("fixture does not validate: %v", err)
	}
	if attempt, ok := outcome.Session.Current(); !ok || attempt.HasCommand() {
		t.Fatal("the fixture must carry no command")
	}
	return outcome.Session
}

func TestDetachReportsAStreamThatRefusesToClose(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	if _, err := h.svc.Open(context.Background(), OpenRequest{Ref: string(created.Session.ID), Holder: "cli-1"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	terminal, ok := h.terminal.last()
	if !ok {
		t.Fatal("no stream was handed out")
	}
	terminal.closeErr = errors.New("the pty fd was already closed")

	_, err := h.svc.Detach(context.Background(), DetachRequest{Ref: string(created.Session.ID)})
	typed := requireCode(t, err, session.CodeSessionIOFailed)
	if !contains(typed.Hint, "detach again") {
		t.Fatalf("hint = %q, must tell the caller what to do next", typed.Hint)
	}
	// The attachment axis is left alone: a stream that fails to close must not be
	// reported as a detach that happened.
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Attachment != session.AttachmentAttached {
		t.Fatalf("attachment = %s, want the refused detach to change nothing", live.Session.Attachment)
	}
}

func TestDetachReportsALeaseTheBrokerRefusesToRelease(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	if _, err := h.svc.Open(context.Background(), OpenRequest{Ref: string(created.Session.ID), Holder: "cli-1"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	h.broker.releaseErr = errors.New("the lease record could not be written")

	_, err := h.svc.Detach(context.Background(), DetachRequest{Ref: string(created.Session.ID)})
	requireCode(t, err, session.CodeSessionIOFailed)
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Attachment != session.AttachmentAttached {
		t.Fatalf("attachment = %s, want the refused release to change nothing", live.Session.Attachment)
	}
}
