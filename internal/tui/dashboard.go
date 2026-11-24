package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderDashboard() string {
	theme := NewTheme()
	width := m.width
	if width < 80 {
		width = 80
	}
	height := m.height
	if height < 24 {
		height = 24
	}

	var b strings.Builder

	// 1. Header & Title
	titleText := "AGENT SESSION DECK"
	if m.isOwner {
		titleText += " [OWNER]"
	} else {
		titleText += " [CLIENT]"
	}
	title := theme.AppTitle.Render(titleText)

	// Result summary
	resultCount := fmt.Sprintf("Showing %d of %d sessions", len(m.filteredSessions), len(m.sessions))
	headerLine := lipgloss.JoinHorizontal(lipgloss.Top,
		title,
		strings.Repeat(" ", max(2, width-lipgloss.Width(title)-lipgloss.Width(resultCount)-4)),
		theme.KeyDesc.Render(resultCount),
	)
	b.WriteString(headerLine + "\n")

	// 2. Search / Filter Bar
	searchPrompt := theme.SearchPrompt.Render("Search: ")
	searchText := m.searchQuery
	if m.searchActive {
		searchText = theme.SearchText.Render(m.searchQuery + "█")
	} else if searchText == "" {
		searchText = theme.KeyDesc.Render("Press / to search (fuzzy across name, agent, workspace, PID)...")
	}
	b.WriteString(searchPrompt + searchText + "\n\n")

	// 3. Status Bar message (if active)
	if m.statusMessage != "" {
		if m.statusErr {
			b.WriteString(theme.StatusError.Render("! "+m.statusMessage) + "\n\n")
		} else {
			b.WriteString(theme.StatusMessage.Render("✔ "+m.statusMessage) + "\n\n")
		}
	}

	// 4. Main Body: Table & Detail Pane
	availableHeight := height - 9
	if m.statusMessage != "" {
		availableHeight -= 2
	}
	if availableHeight < 5 {
		availableHeight = 5
	}

	tableWidth := width
	var detailView string
	if m.showDetail && width >= 100 {
		tableWidth = width * 55 / 100
		detailWidth := width - tableWidth - 4
		if sel, ok := m.SelectedSession(); ok {
			detailView = RenderDetailPane(sel, theme, detailWidth)
		}
	} else if m.showDetail && width < 100 {
		// Full screen detail view on narrow terminals
		if sel, ok := m.SelectedSession(); ok {
			b.WriteString(RenderDetailPaneCompact(sel, theme, width-2) + "\n\n")
			b.WriteString(theme.FooterBar.Render("Press 'd' or 'Esc' to return to list") + "\n")
			return b.String()
		}
	}

	// Table column widths
	colStatus := 6
	colAgent := 12
	colPID := 8
	colName := 18
	colWorkspace := max(15, tableWidth-colStatus-colAgent-colPID-colName-10)

	// Column Header
	header := fmt.Sprintf("%-6s %-12s %-*s %-18s %-8s",
		"STATUS",
		"AGENT",
		colWorkspace,
		"WORKSPACE",
		"NAME",
		"ATTEMPT",
	)
	b.WriteString(theme.HeaderRow.Width(tableWidth).Render(header) + "\n")

	// Empty state
	if len(m.filteredSessions) == 0 {
		emptyMsg := "No sessions found. Press 'n' to launch a new session."
		if m.searchQuery != "" || m.agentFilter != "" || m.statusFilter != "" {
			emptyMsg = "No matching sessions for query. Press Esc to clear search."
		}
		b.WriteString("\n  " + theme.KeyDesc.Render(emptyMsg) + "\n\n")
	} else if m.groupByWorkspace && len(m.workspaceGroups) > 0 {
		sessionIdx := 0
		for _, group := range m.workspaceGroups {
			gitPart := ""
			if group.Summary.Git != nil {
				branch := group.Summary.Git.Branch
				if group.Summary.Git.Detached {
					branch = "detached"
				}
				if group.Summary.Git.Dirty {
					branch += "*"
				}
				gitPart = fmt.Sprintf(" [%s]", branch)
			}
			wsHeader := fmt.Sprintf("📁 %s%s (%d active, %d total)",
				group.Summary.Workspace.Path(), gitPart, group.Summary.ActiveCount, group.Summary.TotalCount)
			b.WriteString(theme.DetailTitle.Render(wsHeader) + "\n")

			for _, s := range group.Sessions {
				sym, _ := StatusIndicator(s.Lifecycle, theme.NoColor)
				agentStr := string(s.AgentID)
				if len(agentStr) > 12 {
					agentStr = agentStr[:11] + "…"
				}
				nameStr := s.Name
				if nameStr == "" {
					nameStr = string(s.ID)
				}
				if len(nameStr) > 18 {
					nameStr = nameStr[:17] + "…"
				}
				attemptStr := fmt.Sprintf("#%d", s.Generation)
				if s.HasIdentity && s.Identity.PID > 0 {
					attemptStr = fmt.Sprintf("p%d", s.Identity.PID)
				}
				rowText := fmt.Sprintf("  %-4s %-12s %-18s %-8s",
					sym, agentStr, nameStr, attemptStr)

				if sessionIdx == m.cursor {
					b.WriteString(theme.SelectedRow.Width(tableWidth).Render(rowText) + "\n")
				} else {
					b.WriteString(theme.NormalRow.Width(tableWidth).Render(rowText) + "\n")
				}
				sessionIdx++
			}
		}
	} else {
		// Calculate scrolling viewport
		startIdx := 0
		if m.cursor >= availableHeight {
			startIdx = m.cursor - availableHeight + 1
		}
		endIdx := min(len(m.filteredSessions), startIdx+availableHeight)

		for i := startIdx; i < endIdx; i++ {
			s := m.filteredSessions[i]
			sym, _ := StatusIndicator(s.Lifecycle, theme.NoColor)

			agentStr := string(s.AgentID)
			if len(agentStr) > 12 {
				agentStr = agentStr[:11] + "…"
			}

			wsStr := TruncatePath(string(s.WorkspaceID), colWorkspace)
			nameStr := s.Name
			if nameStr == "" {
				nameStr = string(s.ID)
			}
			if len(nameStr) > 18 {
				nameStr = nameStr[:17] + "…"
			}

			attemptStr := fmt.Sprintf("#%d", s.Generation)
			if s.HasIdentity && s.Identity.PID > 0 {
				attemptStr = fmt.Sprintf("p%d", s.Identity.PID)
			}

			rowText := fmt.Sprintf("%-6s %-12s %-*s %-18s %-8s",
				sym,
				agentStr,
				colWorkspace,
				wsStr,
				nameStr,
				attemptStr,
			)

			if i == m.cursor {
				b.WriteString(theme.SelectedRow.Width(tableWidth).Render(rowText) + "\n")
			} else {
				b.WriteString(theme.NormalRow.Width(tableWidth).Render(rowText) + "\n")
			}
		}
	}

	tableView := b.String()
	if detailView != "" {
		// Render side-by-side
		borderStyle := theme.Border
		return lipgloss.JoinHorizontal(lipgloss.Top,
			tableView,
			borderStyle.Render(detailView),
		) + "\n" + m.renderFooter(theme)
	}

	return tableView + "\n" + m.renderFooter(theme)
}

func (m Model) renderFooter(theme Theme) string {
	shortcuts := []struct {
		key  string
		desc string
	}{
		{"Enter", "Run/Open"},
		{"n", "New"},
		{"/", "Search"},
		{"d", "Detail"},
		{"r", "Restart"},
		{"x", "Stop"},
		{"K", "Kill"},
		{"?", "Help"},
		{"q", "Quit"},
	}

	var parts []string
	for _, sc := range shortcuts {
		parts = append(parts, theme.KeyShortcut.Render(sc.key)+" "+theme.KeyDesc.Render(sc.desc))
	}
	return theme.FooterBar.Render(strings.Join(parts, "  "))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
