package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	termview "github.com/ryantr-statinops/agent-sessions-deck/internal/tui/terminal"
)

// View renders the TUI screen based on the active mode.
func (m Model) View() string {
	switch m.mode {
	case ViewTerminal:
		return m.renderTerminal()
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

func (m Model) renderTerminal() string {
	cols, rows := termview.ViewportDimensions(m.width, m.height)
	var screenRows []string

	if m.terminalScroll != nil && m.terminalScroll.IsScrolled() {
		screenRows = m.terminalScroll.VisibleRows(m.terminalScreen, cols, rows)
	} else {
		rendered := termview.RenderScreen(m.terminalScreen, cols, rows)
		screenRows = strings.Split(rendered, "\n")
	}

	footer := m.renderTerminalFooter(cols)
	allLines := append(screenRows, footer)
	return strings.Join(allLines, "\n")
}

func (m Model) renderTerminalFooter(cols int) string {
	var content string

	prefixStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("208")).
		Padding(0, 1)

	exitStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("255")).
		Background(lipgloss.Color("160")).
		Padding(0, 1)

	scrollStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("255")).
		Background(lipgloss.Color("33")).
		Padding(0, 1)

	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("246")).
		Background(lipgloss.Color("236")).
		Padding(0, 1)

	if m.terminalExited {
		content = exitStyle.Render("● Process exited — Press Ctrl+] s to return to dashboard")
	} else if m.terminalPrefix != nil && m.terminalPrefix.IsPending() {
		content = prefixStyle.Render("[PREFIX Ctrl+] s: list | n: new | ?: help | [: scroll | Ctrl+]: send prefix")
	} else if m.terminalScroll != nil && m.terminalScroll.IsScrolled() {
		total := 0
		if m.terminalScroll != nil {
			total = m.terminalScroll.Offset()
		}
		content = scrollStyle.Render(fmt.Sprintf("[SCROLL Line -%d | Esc/Enter: return to live]", total))
	} else {
		name := m.activeSessionID
		if name == "" {
			name = "session"
		}
		content = normalStyle.Render(fmt.Sprintf("[%s] Ctrl+] s: list | n: new | ?: help | PgUp: scroll", name))
	}

	// Pad full line to match terminal width
	contentLen := lipgloss.Width(content)
	if cols > contentLen {
		fill := lipgloss.NewStyle().Background(lipgloss.Color("236")).Render(strings.Repeat(" ", cols-contentLen))
		return content + fill
	}
	return content
}
