package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// ActiveSessionCount returns the number of sessions currently in an active lifecycle state.
func (m Model) ActiveSessionCount() int {
	count := 0
	for _, s := range m.sessions {
		if s.Lifecycle == session.LifecycleRunning || s.Lifecycle == session.LifecycleStarting {
			count++
		}
	}
	return count
}

// RequestQuit initiates the quit workflow according to ownership rules.
func (m Model) RequestQuit() (tea.Model, tea.Cmd) {
	// 1. Secondary client: quit simply detaches from the TUI.
	if !m.isOwner {
		return m, tea.Quit
	}

	// 2. Foreground owner: check if there are active sessions.
	activeCount := m.ActiveSessionCount()
	if activeCount == 0 {
		return m, tea.Quit
	}

	// Active sessions exist: prompt owner before stopping sessions.
	m.confirmState = ConfirmState{
		Action:      ConfirmOwnerQuit,
		ActiveCount: activeCount,
		Prompt:      fmt.Sprintf("You are the foreground owner with %d active session(s). Stop all and quit, or Cancel?", activeCount),
		Impact:      "Closing the foreground owner will stop all managed sessions.",
	}
	m.mode = ViewConfirm
	return m, nil
}
