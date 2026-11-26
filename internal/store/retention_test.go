package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
)

func TestEventJournalPrune(t *testing.T) {
	tmpDir := t.TempDir()
	journalPath := filepath.Join(tmpDir, "events.jsonl")
	lockPath := filepath.Join(tmpDir, ".lock")

	j, err := NewEventJournal(journalPath, lockPath)
	if err != nil {
		t.Fatalf("NewEventJournal: %v", err)
	}

	baseTime := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	// Append 10 events:
	// Events 1-3: 10 hours ago
	// Events 4-7: 5 hours ago
	// Events 8-10: 1 hour ago
	for i := 1; i <= 10; i++ {
		var evTime time.Time
		switch {
		case i <= 3:
			evTime = baseTime.Add(-10 * time.Hour)
		case i <= 7:
			evTime = baseTime.Add(-5 * time.Hour)
		default:
			evTime = baseTime.Add(-1 * time.Hour)
		}
		ev := events.NewEvent(
			events.ID("evt-"+string(rune('0'+i))),
			events.TypeSessionStarted,
			"s-1",
			1,
			events.Revision(i),
			events.NewTimestamp(evTime),
			events.Metadata{},
		)
		if err := j.Append(ctx, ev); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}

	// 1. Prune by age: maxAge = 6 hours (should drop events 1-3)
	pruned, err := j.Prune(ctx, 0, 6*time.Hour, baseTime)
	if err != nil {
		t.Fatalf("Prune by age: %v", err)
	}
	if pruned != 3 {
		t.Fatalf("pruned = %d, want 3", pruned)
	}

	// Verify surviving events: 7 events left (revisions 4..10)
	surviving, _, err := j.Query(ctx, EventFilter{Limit: 100})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(surviving) != 7 {
		t.Fatalf("surviving count = %d, want 7", len(surviving))
	}
	if surviving[0].Revision() != 4 || surviving[6].Revision() != 10 {
		t.Fatalf("revisions = %d..%d, want 4..10", surviving[0].Revision(), surviving[6].Revision())
	}

	// 2. Prune by maxEvents: retain only newest 4 events (events 7, 8, 9, 10; dropping 4, 5, 6)
	pruned, err = j.Prune(ctx, 4, 0, baseTime)
	if err != nil {
		t.Fatalf("Prune by maxEvents: %v", err)
	}
	if pruned != 3 {
		t.Fatalf("pruned = %d, want 3", pruned)
	}

	surviving, _, err = j.Query(ctx, EventFilter{Limit: 100})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(surviving) != 4 {
		t.Fatalf("surviving count = %d, want 4", len(surviving))
	}
	if surviving[0].Revision() != 7 || surviving[3].Revision() != 10 {
		t.Fatalf("revisions = %d..%d, want 7..10", surviving[0].Revision(), surviving[3].Revision())
	}
}
