package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestSecondaryClientQuitDetachesImmediately(t *testing.T) {
	m := NewModel(Options{
		IsOwner: false, // Secondary client
	})
	m.sessions = []session.SessionSnapshot{
		{
			ID:        session.ID("sess-active"),
			Lifecycle: session.LifecycleRunning,
		},
	}

	// Press 'q'
	up, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	mUp := up.(Model)

	if mUp.mode == ViewConfirm {
		t.Fatalf("secondary client must NOT show owner quit confirm dialog")
	}
	// Cmd should be tea.Quit
	if cmd == nil {
		t.Fatalf("expected tea.Quit command for secondary client")
	}
}

func TestOwnerQuitWithActiveSessionsPromptsConfirmation(t *testing.T) {
	m := NewModel(Options{
		IsOwner: true, // Foreground Owner
	})
	m.sessions = []session.SessionSnapshot{
		{
			ID:        session.ID("sess-1"),
			Lifecycle: session.LifecycleRunning,
		},
		{
			ID:        session.ID("sess-2"),
			Lifecycle: session.LifecycleStarting,
		},
	}

	// Press 'q'
	up, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	mUp := up.(Model)

	if mUp.mode != ViewConfirm || mUp.confirmState.Action != ConfirmOwnerQuit {
		t.Fatalf("expected ViewConfirm with ConfirmOwnerQuit, got mode=%v action=%v", mUp.mode, mUp.confirmState.Action)
	}
	if mUp.confirmState.ActiveCount != 2 {
		t.Fatalf("expected active count 2, got %d", mUp.confirmState.ActiveCount)
	}
	if cmd != nil {
		t.Fatalf("must not dispatch quit command before confirmation")
	}

	// Verify modal prompt mentions active sessions count
	rendered := mUp.renderConfirm()
	if !strings.Contains(rendered, "2 active session(s)") {
		t.Fatalf("expected modal to mention '2 active session(s)', got:\n%s", rendered)
	}

	// Press 'c' (Cancel) -> stays in dashboard
	cancelUp, _ := mUp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	mCancel := cancelUp.(Model)
	if mCancel.mode != ViewDashboard {
		t.Fatalf("expected mode ViewDashboard after cancel, got %v", mCancel.mode)
	}

	// Press 's' (Stop all & Quit) -> exits
	confirmUp, confirmCmd := mUp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	_ = confirmUp.(Model)
	if confirmCmd == nil {
		t.Fatalf("expected quit command after owner confirmation")
	}
}

func TestOwnerQuitWithZeroActiveSessionsExitsImmediately(t *testing.T) {
	m := NewModel(Options{
		IsOwner: true,
	})
	// Only exited / non-active sessions
	m.sessions = []session.SessionSnapshot{
		{
			ID:        session.ID("sess-done"),
			AgentID:   agent.ID("generic"),
			Lifecycle: session.LifecycleExited,
		},
	}

	// Press 'q'
	up, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	mUp := up.(Model)

	if mUp.mode == ViewConfirm {
		t.Fatalf("owner with zero active sessions should NOT show confirm dialog")
	}
	if cmd == nil {
		t.Fatalf("expected immediate quit command")
	}
}
