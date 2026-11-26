package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
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

func TestPruneAttemptsAndActiveProtection(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	// Create a session with 5 attempts
	s1, err := session.New("s-000001", "worker-1", "echo-agent", "/workspaces/default", now)
	if err != nil {
		t.Fatalf("New session: %v", err)
	}

	for gen := 1; gen <= 5; gen++ {
		att := session.NewAttempt(session.Generation(gen), now)
		att.Lifecycle = session.LifecycleExited
		att.EndedAt = now
		s1.Attempts = append(s1.Attempts, att)
		s1.Generation = session.Generation(gen)
	}

	// 1. Prune with maxAttempts = 2 -> drops attempts 1, 2, 3; keeps 4, 5
	pruned, result := PruneAttempts([]session.Session{s1}, 2)
	if pruned != 3 {
		t.Fatalf("pruned = %d, want 3", pruned)
	}
	if len(result[0].Attempts) != 2 {
		t.Fatalf("len(attempts) = %d, want 2", len(result[0].Attempts))
	}
	if result[0].Attempts[0].Generation != 4 || result[0].Attempts[1].Generation != 5 {
		t.Fatalf("generations = %v, %v, want 4, 5", result[0].Attempts[0].Generation, result[0].Attempts[1].Generation)
	}

	// 2. Active Session Protection: Session has running current attempt
	sActive, err := session.New("s-000002", "worker-active", "echo-agent", "/workspaces/default", now)
	if err != nil {
		t.Fatalf("New session: %v", err)
	}
	for gen := 1; gen <= 4; gen++ {
		att := session.NewAttempt(session.Generation(gen), now)
		att.Lifecycle = session.LifecycleExited
		att.EndedAt = now
		sActive.Attempts = append(sActive.Attempts, att)
	}
	// Attempt 5 is RUNNING
	runningAtt := session.NewAttempt(session.Generation(5), now)
	runningAtt.Lifecycle = session.LifecycleRunning
	runningAtt.RunningAt = now
	sActive.Attempts = append(sActive.Attempts, runningAtt)
	sActive.Generation = 5

	pruned, resultActive := PruneAttempts([]session.Session{sActive}, 3)
	if pruned != 2 {
		t.Fatalf("pruned = %d, want 2", pruned)
	}
	if len(resultActive[0].Attempts) != 3 {
		t.Fatalf("len = %d, want 3", len(resultActive[0].Attempts))
	}
	curr, ok := resultActive[0].Current()
	if !ok || curr.Generation != 5 || curr.Lifecycle != session.LifecycleRunning {
		t.Fatalf("current attempt corrupted: ok=%v, curr=%+v", ok, curr)
	}
}

func TestPruneFinishedSessionsActiveProtection(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)

	// Old finished session
	sFinished, err := session.New("s-000001", "old-finished", "echo-agent", "/workspaces/default", now.Add(-100*24*time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sFinished.UpdatedAt = now.Add(-100 * 24 * time.Hour)
	att1 := session.NewAttempt(1, now.Add(-100*24*time.Hour))
	att1.Lifecycle = session.LifecycleExited
	att1.EndedAt = now.Add(-100 * 24 * time.Hour)
	sFinished.Attempts = append(sFinished.Attempts, att1)
	sFinished.Generation = 1

	// Old active session (running for 100 days)
	sActive, err := session.New("s-000002", "old-running", "echo-agent", "/workspaces/default", now.Add(-100*24*time.Hour))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sActive.UpdatedAt = now.Add(-100 * 24 * time.Hour)
	att2 := session.NewAttempt(1, now.Add(-100*24*time.Hour))
	att2.Lifecycle = session.LifecycleRunning
	att2.RunningAt = now.Add(-100 * 24 * time.Hour)
	sActive.Attempts = append(sActive.Attempts, att2)
	sActive.Generation = 1

	sessions := []session.Session{sFinished, sActive}
	pruned, surviving := PruneFinishedSessions(sessions, 30*24*time.Hour, now)
	if pruned != 1 {
		t.Fatalf("pruned = %d, want 1", pruned)
	}
	if len(surviving) != 1 || surviving[0].ID != "s-000002" {
		t.Fatalf("surviving = %+v, want only active session s-000002", surviving)
	}
}
