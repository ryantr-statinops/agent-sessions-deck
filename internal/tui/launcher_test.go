package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestLauncherRendering(t *testing.T) {
	m := NewModel(Options{
		InitialAgents: []owner.AgentProbe{
			{ID: agent.ID("claude")},
			{ID: agent.ID("codex")},
		},
		RecentWorkspaces: []string{"/workspace/recent"},
	})
	m.mode = ViewLauncher

	rendered := m.renderLauncher()

	if !strings.Contains(rendered, "NEW SESSION LAUNCHER") {
		t.Fatalf("expected title in launcher, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "claude") {
		t.Fatalf("expected agent 'claude' in launcher")
	}
	if !strings.Contains(rendered, "/workspace/recent") {
		t.Fatalf("expected recent workspace path in launcher")
	}
}

func TestLauncherFieldNavigation(t *testing.T) {
	m := NewModel(Options{})
	m.mode = ViewLauncher
	m.launcherFocusedField = 0

	// Tab -> field 1 (workspace)
	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	mUp := up.(Model)
	if mUp.launcherFocusedField != 1 {
		t.Fatalf("expected focused field 1 after Tab, got %d", mUp.launcherFocusedField)
	}

	// Tab -> field 2 (name)
	up2, _ := mUp.Update(tea.KeyMsg{Type: tea.KeyTab})
	mUp2 := up2.(Model)
	if mUp2.launcherFocusedField != 2 {
		t.Fatalf("expected focused field 2 after Tab, got %d", mUp2.launcherFocusedField)
	}

	// Tab -> wrap to 0 (agent)
	up3, _ := mUp2.Update(tea.KeyMsg{Type: tea.KeyTab})
	mUp3 := up3.(Model)
	if mUp3.launcherFocusedField != 0 {
		t.Fatalf("expected focused field 0 after wrap Tab, got %d", mUp3.launcherFocusedField)
	}
}

func TestLauncherInputAndPreservationOnFailure(t *testing.T) {
	mockClient := &mockAppClient{
		// Simulate create failure
		listFunc: func(ctx context.Context, req app.ListRequest) (app.ListResult, error) {
			return app.ListResult{}, nil
		},
	}
	m := NewModel(Options{Client: mockClient})
	m.mode = ViewLauncher
	m.launcherFocusedField = 1
	m.launcherWorkspace = "/initial"

	// Type in workspace field
	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/test")})
	mUp := up.(Model)
	if mUp.launcherWorkspace != "/initial/test" {
		t.Fatalf("expected workspace '/initial/test', got %q", mUp.launcherWorkspace)
	}

	// Launch failure message received
	failMsg := LaunchCompletedMsg{
		Seq: mUp.seq,
		Err: errors.New("cannot create in directory"),
	}
	up2, _ := mUp.Update(failMsg)
	mUp2 := up2.(Model)

	// Mode should remain ViewLauncher or inputs preserved
	if mUp2.launcherWorkspace != "/initial/test" {
		t.Fatalf("expected workspace preserved on failure, got %q", mUp2.launcherWorkspace)
	}
	if mUp2.launcherErr != "cannot create in directory" {
		t.Fatalf("expected launcherErr 'cannot create in directory', got %q", mUp2.launcherErr)
	}
}

func TestLauncherSuccessSwitchesToDashboard(t *testing.T) {
	m := NewModel(Options{})
	m.mode = ViewLauncher
	m.seq = 1

	successMsg := LaunchCompletedMsg{
		Seq:       1,
		SessionID: "s-success-99",
	}
	up, _ := m.Update(successMsg)
	mUp := up.(Model)

	if mUp.mode != ViewDashboard {
		t.Fatalf("expected mode ViewDashboard after successful launch, got %v", mUp.mode)
	}
	if !strings.Contains(mUp.statusMessage, "s-success-99") {
		t.Fatalf("expected status message mentioning s-success-99, got %q", mUp.statusMessage)
	}
}

func (m *mockAppClient) Create(ctx context.Context, req app.CreateRequest) (app.CreateResult, error) {
	return app.CreateResult{
		Session: session.SessionSnapshot{
			ID:   session.ID("new-id"),
			Name: req.Name,
		},
	}, nil
}
