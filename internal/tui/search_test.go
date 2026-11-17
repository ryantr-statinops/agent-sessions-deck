package tui

import (
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestFuzzyScore(t *testing.T) {
	// Exact match
	score, ok := FuzzyScore("claude", "claude")
	if !ok || score < 1000 {
		t.Fatalf("expected exact match score >= 1000, got %d, %v", score, ok)
	}

	// Prefix match
	pScore, ok := FuzzyScore("clau", "claude-run")
	if !ok || pScore < 600 {
		t.Fatalf("expected prefix match score >= 600, got %d, %v", pScore, ok)
	}

	// Substring at word boundary
	wScore, ok := FuzzyScore("deck", "agent-sessions-deck")
	if !ok || wScore < 400 {
		t.Fatalf("expected boundary substring match score >= 400, got %d, %v", wScore, ok)
	}

	// Subsequence match
	sScore, ok := FuzzyScore("asd", "agent-sessions-deck")
	if !ok || sScore <= 0 {
		t.Fatalf("expected subsequence match, got %d, %v", sScore, ok)
	}

	// Non-match
	_, ok = FuzzyScore("xyz", "claude")
	if ok {
		t.Fatalf("expected non-match for 'xyz' in 'claude'")
	}
}

func TestFilterSessionsMultiField(t *testing.T) {
	t1 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

	sessions := []session.SessionSnapshot{
		{
			ID:          session.ID("sess-1"),
			Name:        "kestrel-worker",
			AgentID:     agent.ID("opencode"),
			WorkspaceID: workspace.ID("/home/user/kestrel"),
			Lifecycle:   session.LifecycleRunning,
			ObservedAt:  t1,
			HasIdentity: true,
			Identity: session.ProcessIdentity{
				PID: 1234,
			},
		},
		{
			ID:          session.ID("sess-2"),
			Name:        "observability-pipeline",
			AgentID:     agent.ID("claude"),
			WorkspaceID: workspace.ID("/home/user/cluster"),
			Lifecycle:   session.LifecycleExited,
			ObservedAt:  t2,
			HasIdentity: true,
			Identity: session.ProcessIdentity{
				PID: 5678,
			},
		},
	}

	// 1. Search by Name
	res := FilterSessions(sessions, "kestrel", "", "")
	if len(res) != 1 || res[0].ID != "sess-1" {
		t.Fatalf("expected to match sess-1 by name 'kestrel', got %v", res)
	}

	// 2. Search by Agent
	res = FilterSessions(sessions, "claude", "", "")
	if len(res) != 1 || res[0].ID != "sess-2" {
		t.Fatalf("expected to match sess-2 by agent 'claude', got %v", res)
	}

	// 3. Search by Workspace
	res = FilterSessions(sessions, "cluster", "", "")
	if len(res) != 1 || res[0].ID != "sess-2" {
		t.Fatalf("expected to match sess-2 by workspace 'cluster', got %v", res)
	}

	// 4. Search by PID
	res = FilterSessions(sessions, "1234", "", "")
	if len(res) != 1 || res[0].ID != "sess-1" {
		t.Fatalf("expected to match sess-1 by PID 1234, got %v", res)
	}

	// 5. Facet filter by Agent
	res = FilterSessions(sessions, "", "opencode", "")
	if len(res) != 1 || res[0].ID != "sess-1" {
		t.Fatalf("expected to filter by agent opencode, got %v", res)
	}

	// 6. Facet filter by Lifecycle Status
	res = FilterSessions(sessions, "", "", "exited")
	if len(res) != 1 || res[0].ID != "sess-2" {
		t.Fatalf("expected to filter by lifecycle exited, got %v", res)
	}

	// 7. Combined Query + Facet
	res = FilterSessions(sessions, "pipeline", "claude", "exited")
	if len(res) != 1 || res[0].ID != "sess-2" {
		t.Fatalf("expected combined match for sess-2, got %v", res)
	}

	// 8. Conflicting Filter -> Empty
	res = FilterSessions(sessions, "kestrel", "claude", "")
	if len(res) != 0 {
		t.Fatalf("expected 0 matches for kestrel + claude, got %d", len(res))
	}
}

func TestFilterSessionsEmptyQueryStableSort(t *testing.T) {
	t1 := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)

	sessions := []session.SessionSnapshot{
		{ID: session.ID("older"), ObservedAt: t1},
		{ID: session.ID("newer"), ObservedAt: t2},
	}

	res := FilterSessions(sessions, "", "", "")
	if len(res) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(res))
	}
	// Newer observed first
	if res[0].ID != "newer" || res[1].ID != "older" {
		t.Fatalf("expected stable sort by ObservedAt descending, got %s then %s", res[0].ID, res[1].ID)
	}
}
