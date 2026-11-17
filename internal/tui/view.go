package tui

import "fmt"

// View renders the TUI screen based on the active mode.
func (m Model) View() string {
	switch m.mode {
	case ViewLauncher:
		return m.renderLauncher()
	case ViewConfirm:
		return m.renderConfirm()
	case ViewHelp:
		return m.renderHelp()
	case ViewDashboard:
		fallthrough
	default:
		return m.renderDashboard()
	}
}

func (m Model) renderDashboard() string {
	return fmt.Sprintf("AGENT SESSION DECK - Mode: %s\nSessions: %d", m.mode, len(m.filteredSessions))
}

func (m Model) renderLauncher() string {
	return "NEW SESSION LAUNCHER\nPress Esc to cancel"
}

func (m Model) renderConfirm() string {
	return fmt.Sprintf("CONFIRMATION\n%s\n[y] Yes  [n] No", m.confirmState.Prompt)
}

func (m Model) renderHelp() string {
	return "HELP OVERLAY\nPress Esc or ? to return"
}
