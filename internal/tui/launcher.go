package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

func (m Model) renderLauncher() string {
	theme := NewTheme()
	width := m.width
	if width < 80 {
		width = 80
	}

	var b strings.Builder

	b.WriteString(theme.AppTitle.Render("── NEW SESSION LAUNCHER ──") + "\n\n")

	// Field 0: Agent selection
	agentHeader := theme.FormFieldLabel.Render("1. Agent (↑/↓ to choose)")
	if m.launcherFocusedField == 0 {
		agentHeader = theme.FormFieldFocus.Render("▶ 1. Agent (choose with ↑/↓)")
	}
	b.WriteString(agentHeader + "\n")

	if len(m.agents) == 0 {
		b.WriteString(theme.KeyDesc.Render("   (No agents found via scan, fallback to generic)") + "\n")
	} else {
		for i, ag := range m.agents {
			prefix := "   "
			if i == m.launcherSelectedAgent {
				prefix = " > "
			}
			line := fmt.Sprintf("%s%-16s", prefix, ag.ID)
			if i == m.launcherSelectedAgent {
				b.WriteString(theme.SelectedRow.Render(line) + "\n")
			} else {
				b.WriteString(theme.NormalRow.Render(line) + "\n")
			}
		}
	}
	b.WriteString("\n")

	// Field 1: Workspace input
	wsHeader := theme.FormFieldLabel.Render("2. Workspace Directory")
	if m.launcherFocusedField == 1 {
		wsHeader = theme.FormFieldFocus.Render("▶ 2. Workspace Directory (type path)")
	}
	b.WriteString(wsHeader + "\n")

	wsDisplay := m.launcherWorkspace
	if m.launcherFocusedField == 1 {
		wsDisplay += "█"
	} else if wsDisplay == "" {
		wsDisplay = theme.KeyDesc.Render("(enter absolute or relative path)")
	}
	b.WriteString("   " + wsDisplay + "\n\n")

	// Field 2: Session Name
	nameHeader := theme.FormFieldLabel.Render("3. Session Name (optional)")
	if m.launcherFocusedField == 2 {
		nameHeader = theme.FormFieldFocus.Render("▶ 3. Session Name (optional)")
	}
	b.WriteString(nameHeader + "\n")

	nameDisplay := m.launcherName
	if m.launcherFocusedField == 2 {
		nameDisplay += "█"
	} else if nameDisplay == "" {
		nameDisplay = theme.KeyDesc.Render("(auto-derived if left blank)")
	}
	b.WriteString("   " + nameDisplay + "\n\n")

	// Error message
	if m.launcherErr != "" {
		b.WriteString(theme.FormError.Render("! "+m.launcherErr) + "\n\n")
	}

	// Footer shortcuts
	footer := fmt.Sprintf("%s %s   %s %s   %s %s",
		theme.KeyShortcut.Render("Tab"), theme.KeyDesc.Render("Next Field"),
		theme.KeyShortcut.Render("Enter"), theme.KeyDesc.Render("Run / Launch"),
		theme.KeyShortcut.Render("Esc"), theme.KeyDesc.Render("Cancel"),
	)
	b.WriteString(theme.FooterBar.Render(footer) + "\n")

	boxStyle := theme.ModalBox.Width(min(width-4, 70))
	return lipgloss.Place(width, m.height, lipgloss.Center, lipgloss.Center, boxStyle.Render(b.String()))
}

// launchSessionCmd executes the Create operation through the application client.
func (m Model) launchSessionCmd(seq AsyncSeq) tea.Cmd {
	client := m.client
	var agentID agent.ID
	if len(m.agents) > 0 && m.launcherSelectedAgent >= 0 && m.launcherSelectedAgent < len(m.agents) {
		agentID = m.agents[m.launcherSelectedAgent].ID
	} else {
		agentID = agent.ID("generic")
	}

	wsPath := strings.TrimSpace(m.launcherWorkspace)
	if wsPath == "" {
		wsPath = "."
	}
	sessionName := strings.TrimSpace(m.launcherName)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		res, err := client.Create(ctx, app.CreateRequest{
			Agent:         agentID,
			WorkspacePath: wsPath,
			Name:          sessionName,
		})
		if err != nil {
			return LaunchCompletedMsg{
				Seq: seq,
				Err: err,
			}
		}

		return LaunchCompletedMsg{
			Seq:       seq,
			SessionID: string(res.Session.ID),
		}
	}
}
