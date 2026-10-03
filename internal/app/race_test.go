package app

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// The owner is safe for concurrent use: reads run in parallel with each other and
// with a mutation in flight, while mutations are serialized owner-wide so the
// store's commit order and the event stream's revision order are the same order.
// These tests run under -race as well as without it.

func TestConcurrentReadsSeeAConsistentSnapshot(t *testing.T) {
	h := newHarness(t)
	h.createRunning(t)
	if _, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: secondWork,
		Name:          "observability",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	const readers = 8
	const rounds = 20
	var wg sync.WaitGroup
	errs := make(chan error, readers*rounds)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for round := 0; round < rounds; round++ {
				result, err := h.svc.List(context.Background(), ListRequest{})
				if err != nil {
					errs <- err
					return
				}
				if err := result.Snapshot.Validate(); err != nil {
					errs <- err
					return
				}
				if len(result.Snapshot.Sessions) != 2 {
					errs <- errors.New("a snapshot lost a row")
					return
				}
				for _, row := range result.Snapshot.Sessions {
					if row.Authority != session.AuthorityLive {
						errs <- errors.New("a row lost its authority")
						return
					}
					if row.ObservedAt.IsZero() || row.StoreRevision == 0 {
						errs <- errors.New("a row lost its revision or observation instant")
						return
					}
				}
				if _, err := h.svc.Get(context.Background(), GetRequest{Ref: "s-000001"}); err != nil {
					errs <- err
					return
				}
				if _, err := h.svc.Scan(context.Background(), ScanRequest{}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent read: %v", err)
	}
}

func TestConcurrentMutationsOfDifferentSessionsSerializeAtTheStore(t *testing.T) {
	h := newHarness(t)
	ids := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		created, err := h.svc.Create(context.Background(), CreateRequest{
			Agent:         "opencode",
			WorkspacePath: testWorkspace,
			Name:          "kestrel",
		})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		ids = append(ids, string(created.Session.ID))
	}

	const workers = 4
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i, id := range ids {
		wg.Add(1)
		go func(worker int, ref string) {
			defer wg.Done()
			if _, err := h.svc.Rename(context.Background(), RenameRequest{Ref: ref, Name: "renamed"}); err != nil {
				errs <- err
				return
			}
			if _, err := h.svc.Rename(context.Background(), RenameRequest{Ref: ref, Name: "renamed-again"}); err != nil {
				errs <- err
				return
			}
		}(i, id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent rename: %v", err)
	}

	// Every session survived both renames and the store is still consistent.
	for _, id := range ids {
		result, err := h.svc.Get(context.Background(), GetRequest{Ref: id})
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		if result.Session.Name != "renamed-again" {
			t.Fatalf("session %s name = %q, want the last rename", id, result.Session.Name)
		}
		// The two renames were serialized, so each one committed and read back.
		if !result.Persisted || result.Stored.Name != "renamed-again" {
			t.Fatalf("session %s stored reading = %+v persisted=%v, want the last rename", id, result.Stored, result.Persisted)
		}
	}
	if h.store.count() != len(ids) {
		t.Fatalf("stored sessions = %d, want %d", h.store.count(), len(ids))
	}
	// Every committed mutation produced exactly one revision: eight renames on top
	// of the four creations, with no lost or reused write.
	if want := uint64(7 + len(ids)*2 + len(ids)); h.store.revisionNow() != want {
		t.Fatalf("store revision = %d, want %d", h.store.revisionNow(), want)
	}
}

// TestReadsStayAvailableWhileAMutationRuns covers the half of the concurrency
// model that is deliberately not serialized: a graceful stop holds the mutation
// lock for its whole grace window, and a read issued in that window must still
// answer instead of waiting for the mutation to finish.
func TestReadsStayAvailableWhileAMutationRuns(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	id := string(created.Session.ID)
	h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(0)}

	inside := make(chan struct{})
	release := make(chan struct{})
	h.runtime.stopHook = func() {
		close(inside)
		<-release
	}
	stopped := make(chan error, 1)
	go func() {
		_, err := h.svc.Stop(context.Background(), StopRequest{Ref: id})
		stopped <- err
	}()
	<-inside

	listed, err := h.svc.List(context.Background(), ListRequest{})
	if err != nil {
		t.Fatalf("List during a mutation: %v", err)
	}
	if len(listed.Snapshot.Sessions) != 1 {
		t.Fatalf("rows = %+v, want one", listed.Snapshot.Sessions)
	}
	if _, err := h.svc.Get(context.Background(), GetRequest{Ref: id}); err != nil {
		t.Fatalf("Get during a mutation: %v", err)
	}
	if _, err := h.svc.Scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatalf("Scan during a mutation: %v", err)
	}
	close(release)
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Stop never returned after the reads finished")
	}
}

func TestConcurrentLifecycleCallsOnOneSessionStaySerialized(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	id := string(created.Session.ID)

	const workers = 6
	var wg sync.WaitGroup
	won := make(chan bool, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if _, err := h.svc.Open(context.Background(), OpenRequest{Ref: id, Holder: "cli"}); err != nil {
				won <- false
				return
			}
			won <- true
		}()
	}
	close(start)
	wg.Wait()
	close(won)

	winners := 0
	for ok := range won {
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d clients attached to one session, want exactly one", winners)
	}
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: id})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Attachment != session.AttachmentAttached {
		t.Fatalf("attachment = %s, want the winner to hold it", live.Session.Attachment)
	}
	if err := live.Session.State().Validate(); err != nil {
		t.Fatalf("the session is inconsistent: %v", err)
	}
	if holder, held := h.broker.holder(session.ID(id)); !held || holder != "cli" {
		t.Fatalf("lease holder = %q/%v, want cli holding it", holder, held)
	}
}

func TestConcurrentStopsOnTheSameSessionProduceOneWinner(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(0)}

	const workers = 5
	var wg sync.WaitGroup
	stopped := make(chan bool, workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, err := h.svc.Stop(context.Background(), StopRequest{Ref: string(created.Session.ID)})
			if err != nil {
				stopped <- false
				return
			}
			stopped <- result.Stopped
		}()
	}
	close(start)
	wg.Wait()
	close(stopped)

	winners := 0
	for win := range stopped {
		if win {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d stops reported success, want exactly one", winners)
	}
	stored, found := h.store.find(created.Session.ID)
	if !found {
		t.Fatal("the stopped session was not persisted")
	}
	attempt, _ := stored.Current()
	if attempt.Lifecycle != session.LifecycleExited {
		t.Fatalf("lifecycle = %s, want exited", attempt.Lifecycle)
	}
}
