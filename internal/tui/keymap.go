package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	termview "github.com/ryantr-statinops/agent-sessions-deck/internal/tui/terminal"
)

// handleKeyMsg routes key presses depending on the active view mode and search state.
func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global exit shortcut (except in terminal view, where Ctrl+C is forwarded to child PTY)
	if msg.Type == tea.KeyCtrlC && m.mode != ViewTerminal {
		return m.RequestQuit()
	}

	switch m.mode {
	case ViewTerminal:
		return m.handleTerminalKey(msg)
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

func (m Model) handleTerminalKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.terminalPrefix == nil {
		m.terminalPrefix = termview.NewPrefixMachine()
	}

	res := m.terminalPrefix.ProcessKey(msg)
	if res.Handled {
		switch res.Action {
		case termview.ActionDetach:
			if m.activeSub != nil {
				_ = m.activeSub.Close()
				m.activeSub = nil
			}
			sessID := m.activeSessionID
			m.activeSessionID = ""
			m.mode = ViewDashboard
			seq := m.NextSeq()
			return m, tea.Batch(m.detachSessionCmd(sessID), m.loadSessionsCmd(seq))

		case termview.ActionNew:
			if m.activeSub != nil {
				_ = m.activeSub.Close()
				m.activeSub = nil
			}
			sessID := m.activeSessionID
			m.activeSessionID = ""
			m.mode = ViewLauncher
			return m, m.detachSessionCmd(sessID)

		case termview.ActionHelp:
			m.returnMode = ViewTerminal
			m.mode = ViewHelp
			return m, nil

		case termview.ActionScroll:
			if m.terminalScroll != nil {
				m.terminalScroll.ScrollUp(1)
			}
			return m, nil

		case termview.ActionSendLiteral:
			if m.activeSub != nil {
				_, _ = m.activeSub.Write([]byte{res.LiteralByte})
			}
			return m, nil

		case termview.ActionNone:
			return m, nil
		}
	}

	// If currently scrolled, handle history navigation keys
	if m.terminalScroll != nil && m.terminalScroll.IsScrolled() {
		switch msg.Type {
		case tea.KeyEsc, tea.KeyEnter:
			m.terminalScroll.ScrollToBottom()
			return m, nil
		case tea.KeyUp:
			m.terminalScroll.ScrollUp(1)
			return m, nil
		case tea.KeyDown:
			m.terminalScroll.ScrollDown(1)
			return m, nil
		case tea.KeyPgUp:
			m.terminalScroll.ScrollUp(10)
			return m, nil
		case tea.KeyPgDown:
			m.terminalScroll.ScrollDown(10)
			return m, nil
		}
		if msg.String() == "k" {
			m.terminalScroll.ScrollUp(1)
			return m, nil
		}
		if msg.String() == "j" {
			m.terminalScroll.ScrollDown(1)
			return m, nil
		}
		return m, nil
	}

	// Normal terminal interaction: check for PgUp in normal screen mode
	if msg.Type == tea.KeyPgUp && !m.terminalScreen.AlternateScreen {
		if m.terminalScroll != nil {
			m.terminalScroll.ScrollUp(10)
		}
		return m, nil
	}

	// Normal key forwarding to child PTY
	if m.activeSub != nil {
		mode := termview.InputMode{
			AppCursorKeys: m.terminalScreen.AlternateScreen,
		}
		encoded := termview.EncodeKey(msg, mode)
		if len(encoded) > 0 {
			_, _ = m.activeSub.Write(encoded)
		}
	}

	return m, nil
}

func (m Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "s", "S", "enter":
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
	case ConfirmRestartRunning, ConfirmGracefulStop, ConfirmForceKill:
		sessionID := m.confirmState.SessionID
		seq := m.NextSeq()
		m.setStatus("Sending "+m.confirmState.Prompt+"...", false, 3*time.Second)
		return m, m.executeActionCmd(action, sessionID, seq)
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
	case "enter":
		if sel, ok := m.SelectedSession(); ok {
			return m, m.openSessionCmd(string(sel.ID))
		}
		return m, nil
	case "r":
		if sel, ok := m.SelectedSession(); ok {
			if sel.Lifecycle == "running" {
				m.confirmState = ConfirmState{
					Action:      ConfirmRestartRunning,
					SessionID:   string(sel.ID),
					SessionName: sel.Name,
					Prompt:      fmt.Sprintf("Restart running session %s (%s)?", sel.Name, sel.ID),
					Impact:      "The current attempt will be stopped and a new attempt will be launched.",
				}
				m.mode = ViewConfirm
				return m, nil
			}
			seq := m.NextSeq()
			m.setStatus("Restarting "+string(sel.ID)+"...", false, 3*time.Second)
			return m, m.executeActionCmd(ConfirmRestartRunning, string(sel.ID), seq)
		}
		return m, nil
	case "x", "s":
		if sel, ok := m.SelectedSession(); ok {
			m.confirmState = ConfirmState{
				Action:      ConfirmGracefulStop,
				SessionID:   string(sel.ID),
				SessionName: sel.Name,
				Prompt:      fmt.Sprintf("Gracefully stop session %s (%s)?", sel.Name, sel.ID),
				Impact:      "Sends SIGTERM and waits up to grace period for graceful exit.",
			}
			m.mode = ViewConfirm
			return m, nil
		}
		return m, nil
	case "K":
		if sel, ok := m.SelectedSession(); ok {
			m.confirmState = ConfirmState{
				Action:      ConfirmForceKill,
				SessionID:   string(sel.ID),
				SessionName: sel.Name,
				Prompt:      fmt.Sprintf("Force kill session %s (%s)?", sel.Name, sel.ID),
				Impact:      "Sends SIGKILL immediately to the verified owned process group.",
			}
			m.mode = ViewConfirm
			return m, nil
		}
		return m, nil
	case "q":
		return m.RequestQuit()
	case "?":
		m.returnMode = m.mode
		m.mode = ViewHelp
		return m, nil
	case "d":
		m.showDetail = !m.showDetail
		return m, nil
	case "w":
		m.SetGroupByWorkspace(!m.GroupByWorkspace())
		if m.GroupByWorkspace() {
			m.setStatus("Grouped by workspace", false, 2*time.Second)
		} else {
			m.setStatus("Flat session view", false, 2*time.Second)
		}
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
	case "pgup", "b":
		pageSize := max(5, m.height-10)
		m.cursor = max(0, m.cursor-pageSize)
		return m, nil
	case "pgdown", "f":
		pageSize := max(5, m.height-10)
		m.cursor = min(len(m.filteredSessions)-1, m.cursor+pageSize)
		if m.cursor < 0 {
			m.cursor = 0
		}
		return m, nil
	case "home", "g":
		m.cursor = 0
		return m, nil
	case "end", "G":
		m.cursor = max(0, len(m.filteredSessions)-1)
		return m, nil
	case "ctrl+r":
		seq := m.NextSeq()
		m.setStatus("Refreshing...", false, 2*time.Second)
		return m, m.loadSessionsCmd(seq)
	}

	return m, nil
}
