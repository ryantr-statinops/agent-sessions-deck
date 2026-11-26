package app

import (
	"context"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestServiceHistoryQueryAndFilters(t *testing.T) {
	h := newHarness(t)
	now := h.clock.Now()

	// Create session 1 (finished)
	s1, err := session.New("s-000001", "worker-finished", "echo-agent", "/workspaces/proj-a", now.Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}
	att1 := session.NewAttempt(1, now.Add(-2*time.Hour))
	att1.Lifecycle = session.LifecycleExited
	att1.EndedAt = now.Add(-1 * time.Hour)
	att1.Reason = session.NaturalExit(0)
	att1.ExitCode = 0
	att1.HasExitCode = true
	s1.Attempts = append(s1.Attempts, att1)
	s1.Generation = 1

	// Create session 2 (active)
	s2, err := session.New("s-000002", "worker-active", "other-agent", "/workspaces/proj-b", now.Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("New s2: %v", err)
	}
	att2 := session.NewAttempt(1, now.Add(-1*time.Hour))
	att2.Lifecycle = session.LifecycleRunning
	att2.RunningAt = now.Add(-1 * time.Hour)
	s2.Attempts = append(s2.Attempts, att2)
	s2.Generation = 1

	// Save to store
	existing, rev, err := h.store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := h.store.Commit(context.Background(), append(existing, s1, s2), rev); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// 1. Query all history
	res, err := h.svc.History(context.Background(), HistoryRequest{IncludeAttempts: true})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if res.TotalCount != 2 || len(res.Sessions) != 2 {
		t.Fatalf("TotalCount = %d, sessions = %d", res.TotalCount, len(res.Sessions))
	}
	// Ordered by CreatedAt desc: s2 first, s1 second
	if res.Sessions[0].ID != "s-000002" || res.Sessions[1].ID != "s-000001" {
		t.Fatalf("order mismatch: %s, %s", res.Sessions[0].ID, res.Sessions[1].ID)
	}
	if len(res.Sessions[1].Attempts) != 1 {
		t.Fatalf("attempts count = %d, want 1", len(res.Sessions[1].Attempts))
	}
	if res.Sessions[1].Attempts[0].Duration == "" || res.Sessions[1].Attempts[0].ExitCode != 0 {
		t.Fatalf("attempt metadata = %+v", res.Sessions[1].Attempts[0])
	}

	// 2. Filter by status: active
	activeRes, err := h.svc.History(context.Background(), HistoryRequest{Status: "active"})
	if err != nil {
		t.Fatalf("History active: %v", err)
	}
	if len(activeRes.Sessions) != 1 || activeRes.Sessions[0].ID != "s-000002" {
		t.Fatalf("activeRes = %+v", activeRes)
	}

	// 3. Filter by agent
	agentRes, err := h.svc.History(context.Background(), HistoryRequest{AgentID: "echo-agent"})
	if err != nil {
		t.Fatalf("History agent: %v", err)
	}
	if len(agentRes.Sessions) != 1 || agentRes.Sessions[0].ID != "s-000001" {
		t.Fatalf("agentRes = %+v", agentRes)
	}

	// 4. Pagination (Limit 1, Offset 1)
	pageRes, err := h.svc.History(context.Background(), HistoryRequest{Limit: 1, Offset: 1})
	if err != nil {
		t.Fatalf("History page: %v", err)
	}
	if len(pageRes.Sessions) != 1 || pageRes.Sessions[0].ID != "s-000001" {
		t.Fatalf("pageRes = %+v", pageRes)
	}
}
