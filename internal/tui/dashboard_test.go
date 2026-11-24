package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestDashboardRendering(t *testing.T) {
	m := NewModel(Options{
		IsOwner: true,
	})
	m.width = 100
	m.height = 30
	m.filteredSessions = []session.SessionSnapshot{
		{
			ID:          session.ID("sess-alpha"),
			Name:        "alpha-test",
			AgentID:     agent.ID("claude"),
			WorkspaceID: workspace.ID("/path/to/alpha"),
			Lifecycle:   session.LifecycleRunning,
			ObservedAt:  time.Now().UTC(),
		},
	}

	rendered := m.renderDashboard()

	// Check header elements
	if !strings.Contains(rendered, "AGENT SESSION DECK") {
		t.Fatalf("expected title in rendered dashboard")
	}
	if !strings.Contains(rendered, "[OWNER]") {
		t.Fatalf("expected [OWNER] badge in rendered dashboard")
	}
	if !strings.Contains(rendered, "alpha-test") {
		t.Fatalf("expected session name 'alpha-test' in dashboard")
	}
	if !strings.Contains(rendered, "claude") {
		t.Fatalf("expected agent 'claude' in dashboard")
	}
}

func TestDashboardEmptyRendering(t *testing.T) {
	m := NewModel(Options{})
	m.filteredSessions = nil

	rendered := m.renderDashboard()
	if !strings.Contains(rendered, "No sessions found") {
		t.Fatalf("expected empty state message when filteredSessions is empty")
	}
}

func TestDashboardDetailSplit(t *testing.T) {
	m := NewModel(Options{})
	m.width = 120
	m.height = 30
	m.showDetail = true
	m.filteredSessions = []session.SessionSnapshot{
		{
			ID:          session.ID("sess-beta"),
			Name:        "beta-test",
			AgentID:     agent.ID("codex"),
			WorkspaceID: workspace.ID("/workspace/beta"),
			Lifecycle:   session.LifecycleRunning,
		},
	}

	rendered := m.renderDashboard()
	if !strings.Contains(rendered, "SESSION INSPECTOR") {
		t.Fatalf("expected side-by-side SESSION INSPECTOR pane when showDetail=true")
	}
}

func TestDashboardWorkspaceGroupedRendering(t *testing.T) {
	m := NewModel(Options{})
	m.width = 120
	m.height = 30
	m.sessions = []session.SessionSnapshot{
		{
			ID:          session.ID("sess-1"),
			Name:        "task-1",
			AgentID:     agent.ID("opencode"),
			WorkspaceID: workspace.ID("/repo/backend"),
			Lifecycle:   session.LifecycleRunning,
		},
		{
			ID:          session.ID("sess-2"),
			Name:        "task-2",
			AgentID:     agent.ID("opencode"),
			WorkspaceID: workspace.ID("/repo/backend"),
			Lifecycle:   session.LifecycleExited,
		},
		{
			ID:          session.ID("sess-3"),
			Name:        "frontend-app",
			AgentID:     agent.ID("claude"),
			WorkspaceID: workspace.ID("/repo/frontend"),
			Lifecycle:   session.LifecycleRunning,
		},
	}
	m.filteredSessions = m.sessions
	m.SetGroupByWorkspace(true)

	rendered := m.renderDashboard()

	if !strings.Contains(rendered, "📁 /repo/backend") {
		t.Fatalf("expected workspace header for /repo/backend, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "📁 /repo/frontend") {
		t.Fatalf("expected workspace header for /repo/frontend, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "(1 active, 2 total)") {
		t.Fatalf("expected (1 active, 2 total) for backend, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "task-1") || !strings.Contains(rendered, "frontend-app") {
		t.Fatalf("expected sessions in output:\n%s", rendered)
	}
}
