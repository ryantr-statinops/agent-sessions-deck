package tui

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

func (m Model) renderConfirm() string {
	return "CONFIRMATION\nPress Esc to cancel"
}

func (m Model) renderHelp() string {
	return "HELP OVERLAY\nPress Esc or ? to return"
}
