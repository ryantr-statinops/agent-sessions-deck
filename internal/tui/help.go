package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderHelp() string {
	theme := NewTheme()
	width := m.width
	if width < 80 {
		width = 80
	}

	var b strings.Builder

	b.WriteString(theme.AppTitle.Render("── KEYBOARD SHORTCUTS REFERENCE ──") + "\n\n")

	renderSection := func(title string, items [][2]string) {
		b.WriteString(theme.DetailTitle.Render(title) + "\n")
		for _, item := range items {
			k := theme.KeyShortcut.Width(16).Render(item[0])
			d := theme.KeyDesc.Render(item[1])
			b.WriteString(fmt.Sprintf("  %s %s\n", k, d))
		}
		b.WriteString("\n")
	}

	renderSection("Dashboard Navigation", [][2]string{
		{"↑ / k, ↓ / j", "Move selection cursor up / down"},
		{"PgUp / PgDn", "Scroll one page up / down"},
		{"Home / End", "Jump to first / last session"},
		{"Enter", "Run / Attach to selected session"},
		{"d / Tab", "Toggle session inspector detail pane"},
		{"r", "Restart session attempt"},
		{"x / s", "Graceful stop session (SIGTERM)"},
		{"K (Shift+k)", "Force kill session (SIGKILL)"},
		{"Ctrl+R", "Refresh session list from server"},
	})

	renderSection("Search & Filters", [][2]string{
		{"/", "Activate multi-field fuzzy search"},
		{"Esc / Enter", "Close search bar / keep filter"},
		{"Backspace", "Erase search character"},
		{"Esc (empty)", "Clear search query and reset view"},
	})

	renderSection("Launcher (New Session)", [][2]string{
		{"n", "Open new session launcher form"},
		{"Tab / Shift+Tab", "Switch focus between Agent, Workspace, Name"},
		{"↑ / ↓", "Select agent when Agent field focused"},
		{"Enter", "Launch configured session"},
		{"Esc", "Cancel and return to dashboard"},
	})

	renderSection("General", [][2]string{
		{"?", "Toggle this keyboard help cheatsheet"},
		{"q / Ctrl+C", "Quit ASD (detaches secondary client, prompts owner)"},
	})

	b.WriteString(theme.FooterBar.Render("Press Esc, '?', or 'q' to return to previous view") + "\n")

	boxStyle := theme.ModalBox.Width(min(width-4, 75))
	return lipgloss.Place(width, m.height, lipgloss.Center, lipgloss.Center, boxStyle.Render(b.String()))
}
