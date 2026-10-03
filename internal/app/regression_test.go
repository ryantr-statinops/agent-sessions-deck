package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// Every test here reproduces one contract-breaking failure that the Stage 02
// review observed against this use-case layer, or one race that the owner-wide
// mutation lock is there to close. Each one is deterministic: fixed clock, fixed
// identities, scripted runtime, no sleeps and no real process.

func ctx() context.Context { return context.Background() }

// ---------------------------------------------------------------------------
// Graceful stop evidence
// ---------------------------------------------------------------------------

// TestStopRecordsASignalOnlyReap covers the ordinary POSIX case: SIGTERM reaps
// the child with WIFSIGNALED and no exit code at all. Refusing that record would
// leave the session wedged in stopping, un-stoppable and unpublished.
func TestStopRecordsASignalOnlyReap(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.runtime.stop = session.StopOutcome{Exited: true, Signal: "SIGTERM"}

	result, err := h.svc.Stop(ctx(), StopRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !result.Stopped || result.TimedOut {
		t.Fatalf("result = %+v, want a plain stop", result)
	}
	if result.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("lifecycle = %s, want exited", result.Session.Lifecycle)
	}
	if result.Session.Reason.Kind != session.ReasonStopped || result.Session.Reason.Signal != "SIGTERM" {
		t.Fatalf("reason = %s, want a stopped reason carrying SIGTERM", result.Session.Reason)
	}
	if _, observed := result.Session.Reason.ExitStatus(); observed {
		t.Fatal("a signal-killed child reported an exit code it never had")
	}
	stored, found := h.store.find(created.Session.ID)
	if !found {
		t.Fatal("the stopped session was not persisted")
	}
	if attempt, _ := stored.Current(); attempt.Lifecycle != session.LifecycleExited {
		t.Fatalf("persisted attempt = %+v, want exited", attempt)
	}
}

// TestStopSucceedsWhenTheWaiterRecordedTheReapFirst covers the interleaving
// where the owner's single waiter lands its report while the stop is still
// inside its grace window. The stop achieved what it was asked to do, so it
// reports success instead of a duplicate-exit failure that reads as if nothing
// stopped.
func TestStopSucceedsWhenTheWaiterRecordedTheReapFirst(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	id := created.Session.ID
	h.runtime.stop = session.StopOutcome{Exited: true, Signal: "SIGTERM"}
	// The waiter stands in for the runtime owner's exit goroutine: it records the
	// reap and commits it through the same lifecycle authority while the stop is
	// still waiting.
	h.runtime.stopHook = func() {
		if _, err := h.svc.apply(id, session.Event{
			Kind:       session.EventChildExited,
			Generation: 1,
			At:         baseTime,
			Reason:     session.StoppedBySignal("SIGTERM"),
		}); err != nil {
			t.Errorf("the waiter could not record the reap: %v", err)
			return
		}
		if _, err := h.svc.commit(ctx()); err != nil {
			t.Errorf("the waiter could not commit the reap: %v", err)
		}
	}

	result, err := h.svc.Stop(ctx(), StopRequest{Ref: string(id)})
	if err != nil {
		t.Fatalf("Stop must report success once the reap is recorded: %v", err)
	}
	if !result.Stopped || result.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("result = %+v, want the recorded stop", result)
	}
	stored, found := h.store.find(id)
	if !found {
		t.Fatal("the stopped session was not persisted")
	}
	if stored.AttemptCount() != 1 {
		t.Fatalf("attempts = %d, want exactly one recorded exit", stored.AttemptCount())
	}
}

// TestStopRefusesAReapWithoutTerminalEvidence covers a runtime that reports the
// child gone but names nothing that could be recorded. The exit is not evidence,
// so the session goes back to running instead of being wedged in stopping.
func TestStopRefusesAReapWithoutTerminalEvidence(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.runtime.stop = session.StopOutcome{Exited: true}

	result, err := h.svc.Stop(ctx(), StopRequest{Ref: string(created.Session.ID)})
	typed := requireCode(t, err, session.CodeConflict)
	if !contains(typed.Reason, "still recorded as running") {
		t.Fatalf("reason = %q, must say the session is still recorded as running", typed.Reason)
	}
	if !result.TimedOut || result.Stopped {
		t.Fatalf("result = %+v, want a timeout and no stop", result)
	}
	if result.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("lifecycle = %s, want running", result.Session.Lifecycle)
	}
}

// ---------------------------------------------------------------------------
// Interactive claims outlive nothing
// ---------------------------------------------------------------------------

// TestFinishedAttemptReleasesItsInteractiveClaim covers a session that ends
// while a client is attached. The lease is bound to its attempt, so leaving it
// held would strand a file descriptor, leave the client on a dead session and
// block the next generation from attaching - with nothing the client could do to
// clear it.
func TestFinishedAttemptReleasesItsInteractiveClaim(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	id := created.Session.ID
	if _, err := h.svc.Open(ctx(), OpenRequest{Ref: string(id), Holder: "cli-1"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	stream, ok := h.terminal.last()
	if !ok {
		t.Fatal("no stream was handed out")
	}
	if _, held := h.broker.holderOf(id, 1); !held {
		t.Fatal("the attach did not take a lease")
	}

	h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(0)}
	if _, err := h.svc.Stop(ctx(), StopRequest{Ref: string(id)}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, held := h.broker.holderOf(id, 1); held {
		t.Fatal("the finished attempt still holds its lease")
	}
	if h.broker.claims() != 0 {
		t.Fatalf("live claims = %d, want none", h.broker.claims())
	}
	if stream.closeCount() != 1 {
		t.Fatalf("stream closes = %d, want exactly one", stream.closeCount())
	}

	// The next generation is attachable without a detach nobody can perform.
	if _, err := h.svc.Restart(ctx(), RestartRequest{Ref: string(id)}); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	opened, err := h.svc.Open(ctx(), OpenRequest{Ref: string(id), Holder: "cli-2"})
	if err != nil {
		t.Fatalf("the new attempt must be attachable: %v", err)
	}
	if opened.Lease.Generation != 2 || opened.Lease.Holder != "cli-2" {
		t.Fatalf("lease = %+v, want a generation 2 lease held by cli-2", opened.Lease)
	}
}

// TestReportedExitReleasesItsInteractiveClaim covers the same guarantee for an
// attempt that ends because the waiter reported the exit: nothing else touched
// the claim, so nothing else would release it.
func TestReportedExitReleasesItsInteractiveClaim(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	id := created.Session.ID
	if _, err := h.svc.Open(ctx(), OpenRequest{Ref: string(id), Holder: "cli-1"}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, err := h.svc.Report(ctx(), ReportRequest{
		Ref:    string(id),
		Kind:   ReportChildExited,
		Reason: session.NaturalExit(0),
	}); err != nil {
		t.Fatalf("Report: %v", err)
	}
	if _, held := h.broker.holderOf(id, 1); held {
		t.Fatal("the reaped attempt still holds its lease")
	}
	if h.broker.claims() != 0 {
		t.Fatalf("live claims = %d, want none", h.broker.claims())
	}
	live, err := h.svc.Get(ctx(), GetRequest{Ref: string(id)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("lifecycle = %s, want exited", live.Session.Lifecycle)
	}
}

// TestOpenRollsBackAnUnpersistedAttach covers a transient store failure during
// an attach. The caller never receives the lease or the stream, so it has no
// handle to clean up with: without a rollback the session would be wedged for
// good.
func TestOpenRollsBackAnUnpersistedAttach(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	id := created.Session.ID
	h.store.failNextCommit(errors.New("disk full"))

	if _, err := h.svc.Open(ctx(), OpenRequest{Ref: string(id), Holder: "client-b"}); err == nil {
		t.Fatal("the attach should not have succeeded")
	}
	stream, ok := h.terminal.last()
	if !ok {
		t.Fatal("no stream was handed out")
	}
	if stream.closeCount() != 1 {
		t.Fatalf("stream closes = %d, want the rollback to close it once", stream.closeCount())
	}
	if h.broker.claims() != 0 {
		t.Fatalf("live claims = %d, want the lease released", h.broker.claims())
	}
	if holder, held := h.broker.holder(id); held {
		t.Fatalf("the broker still names %q as holder", holder)
	}
	live, err := h.svc.Get(ctx(), GetRequest{Ref: string(id)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Attachment != session.AttachmentDetached {
		t.Fatalf("attachment = %s, want the rollback to record a detach", live.Session.Attachment)
	}
	if live.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("lifecycle = %s, want the process untouched", live.Session.Lifecycle)
	}
}

// ---------------------------------------------------------------------------
// Write failures latch the owner
// ---------------------------------------------------------------------------

// TestStoreFailureLatchesTheOwnerAgainstLaterMutations covers the divergence a
// failed write creates: the engine holds state the store never received. A later
// mutation would write all of it at once, so the owner refuses instead, while the
// reads an operator needs keep working.
func TestStoreFailureLatchesTheOwnerAgainstLaterMutations(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	id := string(created.Session.ID)
	second, err := h.svc.Create(ctx(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: secondWork,
		Name:          "observability",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	storedRevision := h.svc.Revision()

	h.store.failNextCommit(errors.New("disk full"))
	if _, err := h.svc.Rename(ctx(), RenameRequest{Ref: id, Name: "renamed"}); err == nil {
		t.Fatal("the rename should have failed with the store")
	}

	// The uncommitted rename exists in memory and nowhere else.
	live, err := h.svc.Get(ctx(), GetRequest{Ref: id})
	if err != nil {
		t.Fatalf("reads must stay available: %v", err)
	}
	if live.Session.Name != "renamed" {
		t.Fatalf("live name = %q, want the uncommitted rename", live.Session.Name)
	}
	if !live.Persisted || live.Stored.Name != "kestrel" {
		t.Fatalf("stored reading = %+v persisted=%v, want the pre-failure record", live.Stored, live.Persisted)
	}

	// Every later mutation is refused, including one about an unrelated session.
	mutations := map[string]func() error{
		"create": func() error {
			_, err := h.svc.Create(ctx(), CreateRequest{Agent: "opencode", WorkspacePath: testWorkspace})
			return err
		},
		"rename": func() error {
			_, err := h.svc.Rename(ctx(), RenameRequest{Ref: id, Name: "again"})
			return err
		},
		"kill": func() error {
			_, err := h.svc.Kill(ctx(), KillRequest{Ref: string(second.Session.ID)})
			return err
		},
		"report": func() error {
			_, err := h.svc.Report(ctx(), ReportRequest{
				Ref:    id,
				Kind:   ReportChildExited,
				Reason: session.NaturalExit(0),
			})
			return err
		},
		"delete": func() error {
			_, err := h.svc.Delete(ctx(), DeleteRequest{Ref: id})
			return err
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			typed := requireCode(t, mutate(), session.CodeOwnerUnavailable)
			if typed.Hint == "" || !contains(typed.Hint, "restart asd") {
				t.Fatalf("hint = %q, must name the recovery", typed.Hint)
			}
		})
	}

	// The store never received the state the engine is holding.
	if h.svc.Revision() != storedRevision {
		t.Fatalf("revision = %d, want the unchanged %d", h.svc.Revision(), storedRevision)
	}
	if h.store.count() != 2 {
		t.Fatalf("stored sessions = %d, want 2", h.store.count())
	}
	stored, _ := h.store.find(created.Session.ID)
	if stored.Name != "kestrel" {
		t.Fatalf("persisted name = %q, want the un-renamed kestrel", stored.Name)
	}
	if len(h.runtime.forcedKills()) != 0 {
		t.Fatal("a refused mutation still killed something")
	}

	// Reads are unaffected: the operator needs them to see what happened.
	if _, err := h.svc.List(ctx(), ListRequest{}); err != nil {
		t.Fatalf("List after the latch: %v", err)
	}
	if _, err := h.svc.Scan(ctx(), ScanRequest{}); err != nil {
		t.Fatalf("Scan after the latch: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Commit order and event order
// ---------------------------------------------------------------------------

// TestConcurrentCreationsCommitAndPublishInOneOrder covers the lost-event
// failure: the store's revision order and the event stream's revision order have
// to be the same order, or a committed mutation loses its event while the caller
// is told it failed.
func TestConcurrentCreationsCommitAndPublishInOneOrder(t *testing.T) {
	publisher := realPublisher()
	h := newHarness(t)
	h.svc.events = publisher
	sub, err := publisher.Subscribe(ctx(), 32)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	const workers = 8
	var wg sync.WaitGroup
	type outcome struct {
		id  session.ID
		err error
	}
	results := make([]outcome, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			created, err := h.svc.Create(ctx(), CreateRequest{
				Agent:         "opencode",
				WorkspacePath: testWorkspace,
				Name:          "kestrel",
			})
			results[worker] = outcome{id: created.Session.ID, err: err}
		}(i)
	}
	close(start)
	wg.Wait()

	seen := map[session.ID]bool{}
	for i, got := range results {
		if got.err != nil {
			t.Fatalf("Create %d: %v", i, got.err)
		}
		if seen[got.id] {
			t.Fatalf("session id %q was minted twice", got.id)
		}
		seen[got.id] = true
	}
	if h.store.count() != workers {
		t.Fatalf("stored sessions = %d, want %d", h.store.count(), workers)
	}

	received := recvAll(t, sub, workers)
	if len(received) != workers {
		t.Fatalf("received %d events, want %d", len(received), workers)
	}
	previous := events.Revision(0)
	published := map[events.SessionID]bool{}
	for i, notification := range received {
		event, ok := notification.Event()
		if !ok {
			t.Fatalf("notification %v carries no event", notification)
		}
		if event.Type() != events.TypeSessionStarted {
			t.Fatalf("event %d type = %s, want %s", i, event.Type(), events.TypeSessionStarted)
		}
		if event.Revision() <= previous {
			t.Fatalf("event %d revision %d arrived after %d", i, event.Revision(), previous)
		}
		previous = event.Revision()
		if published[event.SessionID()] {
			t.Fatalf("session %q published two creation events", event.SessionID())
		}
		published[event.SessionID()] = true
	}
	if len(published) != workers {
		t.Fatalf("published sessions = %d, want %d", len(published), workers)
	}
	if uint64(publisher.Revision()) != h.store.revisionNow() {
		t.Fatalf("published revision = %d, want the store's %d", publisher.Revision(), h.store.revisionNow())
	}
}

// TestConcurrentMutationsAcrossSessionsNeverLoseAnEvent covers the same ordering
// property for a mix of mutations over different sessions: every committed
// mutation publishes exactly one event, in the store's order.
func TestConcurrentMutationsAcrossSessionsNeverLoseAnEvent(t *testing.T) {
	publisher := realPublisher()
	h := newHarness(t)
	h.svc.events = publisher
	sub, err := publisher.Subscribe(ctx(), 64)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	ids := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		created := h.createRunning(t)
		ids = append(ids, string(created.Session.ID))
	}
	h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(0)}

	var wg sync.WaitGroup
	errs := make(chan error, len(ids)*2)
	start := make(chan struct{})
	for _, id := range ids {
		wg.Add(1)
		go func(ref string) {
			defer wg.Done()
			<-start
			if _, err := h.svc.Rename(ctx(), RenameRequest{Ref: ref, Name: "renamed"}); err != nil {
				errs <- err
				return
			}
			if _, err := h.svc.Stop(ctx(), StopRequest{Ref: ref}); err != nil {
				errs <- err
				return
			}
		}(id)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent mutation: %v", err)
	}

	received := recvAll(t, sub, len(ids)*2)
	if len(received) != len(ids)*2 {
		t.Fatalf("received %d events, want %d", len(received), len(ids)*2)
	}
	previous := events.Revision(0)
	for i, notification := range received {
		event, _ := notification.Event()
		if event.Revision() <= previous {
			t.Fatalf("event %d revision %d arrived after %d", i, event.Revision(), previous)
		}
		previous = event.Revision()
	}
	if uint64(publisher.Revision()) != h.store.revisionNow() {
		t.Fatalf("published revision = %d, want the store's %d", publisher.Revision(), h.store.revisionNow())
	}
}

// ---------------------------------------------------------------------------
// Generation fencing
// ---------------------------------------------------------------------------

// TestReportForAFutureAttemptIsAnError covers the mis-attributed observation: a
// runtime that reports against an attempt the session has not reached is a
// defect, not the benign late callback a stale report is.
func TestReportForAFutureAttemptIsAnError(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	id := string(created.Session.ID)

	result, err := h.svc.Report(ctx(), ReportRequest{
		Ref:     id,
		Attempt: 2,
		Kind:    ReportChildExited,
		Reason:  session.NaturalExit(0),
	})
	typed := requireCode(t, err, session.CodeConflict)
	if result.Stale {
		t.Fatal("a future attempt was tolerated as a stale callback")
	}
	if !errors.Is(err, session.ErrFutureGeneration) {
		t.Fatalf("error %v does not identify the future generation", err)
	}
	if typed.Attempt != 2 {
		t.Fatalf("error attempt = %d, want 2", typed.Attempt)
	}
	live, err := h.svc.Get(ctx(), GetRequest{Ref: id})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Lifecycle != session.LifecycleRunning || live.Session.Generation != 1 {
		t.Fatalf("the refused report changed the session: %+v", live.Session)
	}
	if types := h.capture.types(); len(types) != 1 {
		t.Fatalf("published events = %v, want only the creation event", types)
	}
}

// ---------------------------------------------------------------------------
// Event kinds say what was observed
// ---------------------------------------------------------------------------

// TestReportsPublishWhatTheyObserved covers the event policy: an observation that
// could not decide, a surviving process with no I/O and a recycled pid are three
// different facts, and none of them is a death.
func TestReportsPublishWhatTheyObserved(t *testing.T) {
	tests := []struct {
		name string
		kind ReportKind
		obs  session.LivenessObservation
		want string
	}{
		{
			name: "inconclusive probe",
			kind: ReportProbeInconclusive,
			obs: session.LivenessObservation{
				Outcome: session.ProbeUnverifiable,
				Detail:  "probe-timeout",
				At:      baseTime,
			},
			want: string(TypeSessionUnknown),
		},
		{
			name: "unverifiable pty loss",
			kind: ReportPTYLost,
			obs: session.LivenessObservation{
				Outcome: session.ProbeUnverifiable,
				Detail:  "permission-denied",
				At:      baseTime,
			},
			want: string(TypeSessionUnknown),
		},
		{
			name: "orphaned after a pty loss",
			kind: ReportPTYLost,
			obs: session.LivenessObservation{
				Outcome:  session.ProbeAlive,
				Identity: testIdentity(4242),
				At:       baseTime,
			},
			want: string(TypeSessionOrphaned),
		},
		{
			name: "process gone",
			kind: ReportReconciled,
			obs: session.LivenessObservation{
				Outcome: session.ProbeGone,
				At:      baseTime,
			},
			want: string(events.TypeSessionDead),
		},
		{
			name: "recycled pid",
			kind: ReportReconciled,
			obs: session.LivenessObservation{
				Outcome:  session.ProbeAlive,
				Identity: testIdentity(9999),
				At:       baseTime,
			},
			want: string(TypeSessionStale),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			created := h.createRunning(t)
			if _, err := h.svc.Open(ctx(), OpenRequest{Ref: string(created.Session.ID), Holder: "cli"}); err != nil {
				t.Fatalf("Open: %v", err)
			}
			if _, err := h.svc.Report(ctx(), ReportRequest{
				Ref:      string(created.Session.ID),
				Kind:     tt.kind,
				Liveness: tt.obs,
			}); err != nil {
				t.Fatalf("Report: %v", err)
			}
			types := h.capture.types()
			last := types[len(types)-1]
			if last != tt.want {
				t.Fatalf("published events = %v, want the last one to be %s", types, tt.want)
			}
			if last == string(events.TypeSessionDead) && tt.want != string(events.TypeSessionDead) {
				t.Fatalf("published %s for an observation that does not mean dead", last)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Delete against a live attempt
// ---------------------------------------------------------------------------

// TestDeleteAndRestartNeverStrandALiveAttempt covers the delete race: metadata
// must never be removed while a new attempt is live, because the rule that exists
// to prevent that is the only thing that keeps a live process reconcilable.
// Either the delete wins on a terminal attempt or it is refused.
func TestDeleteAndRestartNeverStrandALiveAttempt(t *testing.T) {
	for round := 0; round < 25; round++ {
		h := newHarness(t)
		created := h.createRunning(t)
		id := created.Session.ID
		h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(0)}
		if _, err := h.svc.Stop(ctx(), StopRequest{Ref: string(id)}); err != nil {
			t.Fatalf("round %d: Stop: %v", round, err)
		}

		start := make(chan struct{})
		var deleteErr, restartErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, deleteErr = h.svc.Delete(ctx(), DeleteRequest{Ref: string(id)})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, restartErr = h.svc.Restart(ctx(), RestartRequest{Ref: string(id)})
		}()
		close(start)
		wg.Wait()

		if deleteErr != nil {
			// The only legitimate refusal is that the session became active again.
			requireCode(t, deleteErr, session.CodeConflict)
		}
		if restartErr != nil {
			// A delete that won first is the only legitimate failure here.
			requireCode(t, restartErr, session.CodeNotFound)
		}

		listed, err := h.svc.List(ctx(), ListRequest{})
		if err != nil {
			t.Fatalf("round %d: List: %v", round, err)
		}
		var row session.SessionSnapshot
		for _, candidate := range listed.Snapshot.Sessions {
			if candidate.ID == id {
				row = candidate
			}
		}
		_, inStore := h.store.find(id)
		if row.ID != "" && !row.Lifecycle.Terminal() && !inStore {
			t.Fatalf("round %d: a live attempt (%s) has no metadata left", round, row.Lifecycle)
		}
		if row.ID == "" && inStore {
			t.Fatalf("round %d: the engine forgot a session the store still holds", round)
		}
		if inStore {
			stored, _ := h.store.find(id)
			if err := stored.Validate(); err != nil {
				t.Fatalf("round %d: the persisted record is inconsistent: %v", round, err)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Stored readings come from the store
// ---------------------------------------------------------------------------

// TestGetReadsThePersistedRowFromTheStore covers the two readings a caller gets:
// the live row is the owner's state and the stored row is the metadata that
// exists. They agree after a committed write, and they do not pretend to agree
// when a write failed.
func TestGetReadsThePersistedRowFromTheStore(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	id := string(created.Session.ID)

	if _, err := h.svc.Rename(ctx(), RenameRequest{Ref: id, Name: "kestrel-review"}); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	read, err := h.svc.Get(ctx(), GetRequest{Ref: id})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !read.Persisted {
		t.Fatal("a committed session reports no persisted metadata")
	}
	if read.Stored.Name != "kestrel-review" || read.Stored.Lifecycle != session.LifecycleRunning {
		t.Fatalf("stored reading = %+v, want the persisted record", read.Stored)
	}
	if read.Stored.Authority != session.AuthorityStored || read.Stored.ClaimsLiveness() {
		t.Fatalf("stored reading = %+v, want no liveness claim", read.Stored)
	}
	if read.Stored.StoreRevision != uint64(h.store.revisionNow()) {
		t.Fatalf("stored reading revision = %d, want the store's %d", read.Stored.StoreRevision, h.store.revisionNow())
	}
}

// TestGetSeparatesTheLiveRowFromThePersistedRow covers a write that failed after
// the session was created: the owner's state and the store's content now disagree,
// and the stored reading has to show what is really persisted rather than a
// downgraded copy of state that was never written.
func TestGetSeparatesTheLiveRowFromThePersistedRow(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.store.failNextCommit(errors.New("read-only file system"))
	if _, err := h.svc.Rename(ctx(), RenameRequest{
		Ref:  string(created.Session.ID),
		Name: "never-persisted",
	}); err == nil {
		t.Fatal("the rename should have failed with the store")
	}

	read, err := h.svc.Get(ctx(), GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if read.Session.Name != "never-persisted" {
		t.Fatalf("live name = %q, want the uncommitted rename", read.Session.Name)
	}
	if !read.Persisted || read.Stored.Name != "kestrel" {
		t.Fatalf("stored reading = %+v persisted=%v, want the persisted kestrel", read.Stored, read.Persisted)
	}
}

// ---------------------------------------------------------------------------
// The single waiter contract
// ---------------------------------------------------------------------------

// TestTheRuntimePortConfirmsKillsAndCarriesTheSingleWaiter covers the port the
// owner depends on: a force kill that confirms the reap, and the one exit channel
// per child that ADR 0003 requires. The wait itself belongs to the runtime
// owner, which reports through Report.
func TestTheRuntimePortConfirmsKillsAndCarriesTheSingleWaiter(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	var runtime ChildRuntime = h.runtime
	if _, err := runtime.WaitExit(ctx(), session.ExitWait{
		SessionID:  created.Session.ID,
		Generation: 1,
		Identity:   testIdentity(4242),
		Waiter:     "owner-1",
	}); err != nil {
		t.Fatalf("WaitExit: %v", err)
	}
	kills := h.runtime.forcedKills()
	if len(kills) != 0 {
		t.Fatalf("waiting for the reap killed the child: %v", kills)
	}
	// The confirmed force kill is what the owner records an exit from.
	if _, err := h.svc.Kill(ctx(), KillRequest{Ref: string(created.Session.ID)}); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if len(h.runtime.forcedKills()) != 1 {
		t.Fatalf("force kills = %v, want one", h.runtime.forcedKills())
	}
}

// TestReportCannotWedgeAnUnidentifiedStartingAttempt covers the persisted window
// between T2 and T3/T4. With no captured child identity there is nothing to probe;
// an inconclusive observation must not turn the pending launch into an absorbing
// unknown state.
func TestReportCannotWedgeAnUnidentifiedStartingAttempt(t *testing.T) {
	ws, err := workspace.New(testWorkspace)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	command, err := agent.NewCommand(launchBin, []string{"--acp"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	seed, err := session.New("s-000001", "kestrel", agent.ID("opencode"), ws.ID(), baseTime)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	attempt := session.NewAttempt(1, baseTime)
	attempt.Lifecycle = session.LifecycleStarting
	attempt.WorkspaceID = ws.ID()
	attempt.Command = command
	seed.Generation = 1
	seed.Attempts = []session.Attempt{attempt}
	if err := seed.Validate(); err != nil {
		t.Fatalf("starting fixture: %v", err)
	}

	h := newHarness(t, withSeed(seed))
	beforeRevision := h.store.revisionNow()
	_, err = h.svc.Report(ctx(), ReportRequest{
		Ref:  string(seed.ID),
		Kind: ReportProbeInconclusive,
		Liveness: session.LivenessObservation{
			Outcome: session.ProbeUnverifiable,
			Detail:  "probe-timeout",
			At:      baseTime,
		},
	})
	requireCode(t, err, session.CodeConflict)

	current, found := h.svc.engine.Get(seed.ID)
	if !found || current.Lifecycle() != session.LifecycleStarting {
		t.Fatalf("live state = %q/%v, want starting retained", current.Lifecycle(), found)
	}
	stored, found := h.store.find(seed.ID)
	if !found || stored.Lifecycle() != session.LifecycleStarting {
		t.Fatalf("stored state = %q/%v, want starting retained", stored.Lifecycle(), found)
	}
	if h.store.revisionNow() != beforeRevision || len(h.capture.types()) != 0 {
		t.Fatalf("refused probe changed revision/events: revision=%d events=%v", h.store.revisionNow(), h.capture.types())
	}
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// revisionNow returns the revision the fake store is at.
func (s *fakeStore) revisionNow() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision
}
