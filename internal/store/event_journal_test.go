package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
)

func TestEventJournalAppendAndReopen(t *testing.T) {
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "events.jsonl")
	lockPath := filepath.Join(tmpDir, ".lock")

	j, err := NewEventJournal(journalPath, lockPath)
	if err != nil {
		t.Fatalf("NewEventJournal: %v", err)
	}
	if got := j.LastRevision(); got != 0 {
		t.Fatalf("LastRevision() = %d, want 0", got)
	}

	ev1 := events.NewEvent("evt-1", events.TypeSessionCreated, "s-1", 1, 1, events.NewTimestamp(time.Now()), events.Metadata{})
	if err := j.Append(context.Background(), ev1); err != nil {
		t.Fatalf("Append ev1: %v", err)
	}
	if got := j.LastRevision(); got != 1 {
		t.Fatalf("LastRevision() = %d, want 1", got)
	}

	ev2 := events.NewEvent("evt-2", events.TypeSessionStarted, "s-1", 1, 2, events.NewTimestamp(time.Now()), events.Metadata{})
	if err := j.Append(context.Background(), ev2); err != nil {
		t.Fatalf("Append ev2: %v", err)
	}
	if got := j.LastRevision(); got != 2 {
		t.Fatalf("LastRevision() = %d, want 2", got)
	}

	// Regressing or duplicate revision must fail
	evDup := events.NewEvent("evt-dup", events.TypeSessionStopped, "s-1", 1, 2, events.NewTimestamp(time.Now()), events.Metadata{})
	if err := j.Append(context.Background(), evDup); !errors.Is(err, ErrNonMonotonicRevision) {
		t.Fatalf("Append evDup error = %v, want ErrNonMonotonicRevision", err)
	}

	// Reopening must rediscover lastRevision = 2
	j2, err := NewEventJournal(journalPath, lockPath)
	if err != nil {
		t.Fatalf("reopen NewEventJournal: %v", err)
	}
	if got := j2.LastRevision(); got != 2 {
		t.Fatalf("j2.LastRevision() = %d, want 2", got)
	}

	// Next append after reopen
	ev3 := events.NewEvent("evt-3", events.TypeSessionStopped, "s-1", 1, 3, events.NewTimestamp(time.Now()), events.Metadata{})
	if err := j2.Append(context.Background(), ev3); err != nil {
		t.Fatalf("Append ev3: %v", err)
	}
	if got := j2.LastRevision(); got != 3 {
		t.Fatalf("j2.LastRevision() = %d, want 3", got)
	}
}

func TestEventJournalOwnerMode(t *testing.T) {
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "events.jsonl")
	lockPath := filepath.Join(tmpDir, ".lock")

	lock, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer lock.Release()

	j, err := NewOwnerEventJournal(journalPath, lock)
	if err != nil {
		t.Fatalf("NewOwnerEventJournal: %v", err)
	}

	ev := events.NewEvent("evt-1", events.TypeSessionCreated, "s-1", 1, 10, events.NewTimestamp(time.Now()), events.Metadata{})
	if err := j.Append(context.Background(), ev); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if got := j.LastRevision(); got != 10 {
		t.Fatalf("LastRevision() = %d, want 10", got)
	}
}

func TestEventJournalQuery(t *testing.T) {
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "events.jsonl")
	lockPath := filepath.Join(tmpDir, ".lock")

	j, err := NewEventJournal(journalPath, lockPath)
	if err != nil {
		t.Fatalf("NewEventJournal: %v", err)
	}

	// Query empty journal
	evs, nextRev, err := j.Query(context.Background(), EventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("Query empty: %v", err)
	}
	if len(evs) != 0 || nextRev != 0 {
		t.Fatalf("Query empty = %d events, nextRev = %d", len(evs), nextRev)
	}

	// Append 10 events across 2 sessions and types
	ctx := context.Background()
	for i := 1; i <= 10; i++ {
		sessID := events.SessionID("s-1")
		if i%2 == 0 {
			sessID = events.SessionID("s-2")
		}
		typ := events.TypeSessionStarted
		if i > 5 {
			typ = events.TypeSessionStopped
		}
		ev := events.NewEvent(events.ID(string(sessID)+"-evt"), typ, sessID, 1, events.Revision(i), events.NewTimestamp(time.Now()), events.Metadata{})
		if err := j.Append(ctx, ev); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	// 1. Query with Limit = 3
	page1, nextRev, err := j.Query(ctx, EventFilter{Limit: 3})
	if err != nil {
		t.Fatalf("Query limit 3: %v", err)
	}
	if len(page1) != 3 || nextRev != 3 {
		t.Fatalf("page1 = %d items, nextRev = %d", len(page1), nextRev)
	}

	// 2. Query page 2 with AfterRevision = 3, Limit = 3
	page2, nextRev2, err := j.Query(ctx, EventFilter{AfterRevision: nextRev, Limit: 3})
	if err != nil {
		t.Fatalf("Query page 2: %v", err)
	}
	if len(page2) != 3 || nextRev2 != 6 {
		t.Fatalf("page2 = %d items, nextRev2 = %d", len(page2), nextRev2)
	}
	if page2[0].Revision() != 4 || page2[2].Revision() != 6 {
		t.Fatalf("page2 revisions = %d..%d", page2[0].Revision(), page2[2].Revision())
	}

	// 3. Filter by session s-2
	s2Events, _, err := j.Query(ctx, EventFilter{SessionID: "s-2", Limit: 10})
	if err != nil {
		t.Fatalf("Query s-2: %v", err)
	}
	if len(s2Events) != 5 {
		t.Fatalf("s-2 events = %d, want 5", len(s2Events))
	}
	for _, ev := range s2Events {
		if ev.SessionID() != "s-2" {
			t.Fatalf("unexpected session id: %s", ev.SessionID())
		}
	}

	// 4. Filter by type session.stopped
	stoppedEvents, _, err := j.Query(ctx, EventFilter{Type: events.TypeSessionStopped, Limit: 10})
	if err != nil {
		t.Fatalf("Query stopped: %v", err)
	}
	if len(stoppedEvents) != 5 {
		t.Fatalf("stopped events = %d, want 5", len(stoppedEvents))
	}
}
