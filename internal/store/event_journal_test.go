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
