package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleKeyMsg routes key presses depending on the active view mode and search state.
func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global exit shortcut
	if msg.Type == tea.KeyCtrlC {
		if m.isOwner && len(m.sessions) > 0 {
			m.confirmState = ConfirmState{
				Action:      ConfirmOwnerQuit,
				ActiveCount: len(m.sessions),
				Prompt:      "You are the foreground owner with active sessions. Stop all and quit, or Cancel?",
			}
			m.mode = ViewConfirm
			return m, nil
		}
		return m, tea.Quit
	}

	switch m.mode {
	case ViewConfirm:
		return m.handleConfirmKey(msg)
	case ViewHelp:
		return m.handleHelpKey(msg)
	case ViewLauncher:
		return m.handleLauncherKey(msg)
	case ViewDashboard:
		return m.handleDashboardKey(msg)
	}

	return m, nil
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		action := m.confirmState.Action
		m.mode = ViewDashboard
		m.confirmState = ConfirmState{}
		return m.executeConfirm(action)
	case "n", "N", "esc", "c", "C":
		m.mode = ViewDashboard
		m.confirmState = ConfirmState{}
		return m, nil
	}
	return m, nil
}

func (m Model) executeConfirm(action ConfirmAction) (tea.Model, tea.Cmd) {
	switch action {
	case ConfirmOwnerQuit:
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) handleHelpKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "?" || msg.String() == "esc" || msg.String() == "q" {
		m.mode = m.returnMode
		return m, nil
	}
	return m, nil
}

func (m Model) handleLauncherKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.mode = ViewDashboard
		m.launcherErr = ""
		return m, nil

	case tea.KeyTab:
		m.launcherFocusedField = (m.launcherFocusedField + 1) % 3
		return m, nil

	case tea.KeyShiftTab:
		m.launcherFocusedField = (m.launcherFocusedField + 2) % 3
		return m, nil

	case tea.KeyEnter:
		if strings.TrimSpace(m.launcherWorkspace) == "" {
			m.launcherErr = "Workspace directory cannot be empty"
			return m, nil
		}
		m.launcherErr = ""
		seq := m.NextSeq()
		return m, m.launchSessionCmd(seq)

	case tea.KeyBackspace:
		if m.launcherFocusedField == 1 && len(m.launcherWorkspace) > 0 {
			m.launcherWorkspace = m.launcherWorkspace[:len(m.launcherWorkspace)-1]
		} else if m.launcherFocusedField == 2 && len(m.launcherName) > 0 {
			m.launcherName = m.launcherName[:len(m.launcherName)-1]
		}
		return m, nil

	case tea.KeyUp:
		if m.launcherFocusedField == 0 {
			if m.launcherSelectedAgent > 0 {
				m.launcherSelectedAgent--
			}
		} else {
			m.launcherFocusedField = (m.launcherFocusedField + 2) % 3
		}
		return m, nil

	case tea.KeyDown:
		if m.launcherFocusedField == 0 {
			if m.launcherSelectedAgent < len(m.agents)-1 {
				m.launcherSelectedAgent++
			}
		} else {
			m.launcherFocusedField = (m.launcherFocusedField + 1) % 3
		}
		return m, nil

	default:
		if msg.Runes != nil {
			if m.launcherFocusedField == 1 {
				m.launcherWorkspace += string(msg.Runes)
			} else if m.launcherFocusedField == 2 {
				m.launcherName += string(msg.Runes)
			}
		}
		return m, nil
	}
}

func (m Model) handleDashboardKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searchActive {
		switch msg.Type {
		case tea.KeyEsc:
			m.searchActive = false
			return m, nil
		case tea.KeyEnter:
			m.searchActive = false
			return m, nil
		case tea.KeyBackspace:
			if len(m.searchQuery) > 0 {
				m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
				m.reapplyFilter()
			}
			return m, nil
		default:
			if msg.Runes != nil {
				m.searchQuery += string(msg.Runes)
				m.reapplyFilter()
			}
			return m, nil
		}
	}

	switch msg.String() {
	case "q":
		if m.isOwner && len(m.sessions) > 0 {
			m.confirmState = ConfirmState{
				Action:      ConfirmOwnerQuit,
				ActiveCount: len(m.sessions),
				Prompt:      "You are the foreground owner with active sessions. Stop all and quit, or Cancel?",
			}
			m.mode = ViewConfirm
			return m, nil
		}
		return m, tea.Quit
	case "?":
		m.returnMode = m.mode
		m.mode = ViewHelp
		return m, nil
	case "d":
		m.showDetail = !m.showDetail
		return m, nil
	case "esc":
		if m.showDetail {
			m.showDetail = false
			return m, nil
		}
		if m.searchQuery != "" {
			m.searchQuery = ""
			m.reapplyFilter()
			return m, nil
		}
		return m, nil
	case "/":
		m.searchActive = true
		return m, nil
	case "n":
		m.mode = ViewLauncher
		return m, nil
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "down", "j":
		if m.cursor < len(m.filteredSessions)-1 {
			m.cursor++
		}
		return m, nil
	case "ctrl+r":
		seq := m.NextSeq()
		m.setStatus("Refreshing...", false, 2*time.Second)
		return m, m.loadSessionsCmd(seq)
	}

	return m, nil
}
