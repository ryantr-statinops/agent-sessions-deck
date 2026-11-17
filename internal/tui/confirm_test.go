package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestConfirmModalRendering(t *testing.T) {
	m := NewModel(Options{})
	m.mode = ViewConfirm
	m.confirmState = ConfirmState{
		Action:      ConfirmRestartRunning,
		SessionID:   "s-target-1",
		SessionName: "target-session",
		Prompt:      "Restart running session target-session (s-target-1)?",
		Impact:      "The current attempt will be stopped.",
	}

	rendered := m.renderConfirm()

	if !strings.Contains(rendered, "CONFIRM RESTART RUNNING SESSION") {
		t.Fatalf("expected title in confirm modal, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "target-session") {
		t.Fatalf("expected session name in modal prompt")
	}
	if !strings.Contains(rendered, "The current attempt will be stopped.") {
		t.Fatalf("expected impact in modal")
	}
	if !strings.Contains(rendered, "[y] Yes") {
		t.Fatalf("expected yes option in modal")
	}
}

func TestConfirmTriggersAndCancellation(t *testing.T) {
	m := NewModel(Options{})
	m.filteredSessions = []session.SessionSnapshot{
		{
			ID:        session.ID("sess-live"),
			Name:      "live-session",
			AgentID:   agent.ID("claude"),
			Lifecycle: session.LifecycleRunning,
		},
	}
	m.cursor = 0

	// 1. Press 'r' -> should enter ViewConfirm
	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	mUp := up.(Model)
	if mUp.mode != ViewConfirm || mUp.confirmState.Action != ConfirmRestartRunning {
		t.Fatalf("expected ViewConfirm with ConfirmRestartRunning after 'r', got mode=%v action=%v", mUp.mode, mUp.confirmState.Action)
	}

	// 2. Press 'n' (Cancel) -> should return to ViewDashboard
	up2, _ := mUp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mUp2 := up2.(Model)
	if mUp2.mode != ViewDashboard || mUp2.confirmState.Action != ConfirmNone {
		t.Fatalf("expected return to ViewDashboard after 'n', got mode=%v", mUp2.mode)
	}

	// 3. Press 'x' -> Graceful stop modal
	up3, _ := mUp2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	mUp3 := up3.(Model)
	if mUp3.mode != ViewConfirm || mUp3.confirmState.Action != ConfirmGracefulStop {
		t.Fatalf("expected ConfirmGracefulStop after 'x', got %v", mUp3.confirmState.Action)
	}

	// 4. Press 'K' -> Force kill modal
	mReset := mUp2
	up4, _ := mReset.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'K'}})
	mUp4 := up4.(Model)
	if mUp4.mode != ViewConfirm || mUp4.confirmState.Action != ConfirmForceKill {
		t.Fatalf("expected ConfirmForceKill after 'K', got %v", mUp4.confirmState.Action)
	}
}

func TestConfirmExecutionDispatchesCmd(t *testing.T) {
	actionCalled := false
	mockClient := &mockAppClient{
		listFunc: func(ctx context.Context, req app.ListRequest) (app.ListResult, error) {
			return app.ListResult{}, nil
		},
	}
	m := NewModel(Options{Client: mockClient})
	m.mode = ViewConfirm
	m.confirmState = ConfirmState{
		Action:    ConfirmGracefulStop,
		SessionID: "s-stop-me",
		Prompt:    "Stop?",
	}

	up, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	mUp := up.(Model)

	if mUp.mode != ViewDashboard {
		t.Fatalf("expected mode ViewDashboard after confirming, got %v", mUp.mode)
	}
	if cmd == nil {
		t.Fatalf("expected command dispatched after confirming action")
	}

	_ = actionCalled
}

func (m *mockAppClient) Stop(ctx context.Context, req app.StopRequest) (app.StopResult, error) {
	return app.StopResult{
		Session: session.SessionSnapshot{
			ID: session.ID(req.Ref),
		},
	}, nil
}
