package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// RenderDetailPane formats detailed inspection information for a session.
func RenderDetailPane(s session.SessionSnapshot, theme Theme, width int) string {
	var b strings.Builder

	title := theme.DetailTitle.Render("── SESSION INSPECTOR ──")
	b.WriteString(title + "\n\n")

	renderField := func(label, value string) {
		l := theme.DetailLabel.Width(16).Render(label + ":")
		v := theme.DetailValue.Render(value)
		b.WriteString(fmt.Sprintf("%s %s\n", l, v))
	}

	renderField("Session ID", string(s.ID))
	renderField("Name", s.Name)
	renderField("Agent", string(s.AgentID))
	renderField("Workspace ID", string(s.WorkspaceID))

	// Lifecycle & State
	sym, text := StatusIndicator(s.Lifecycle, theme.NoColor)
	renderField("Lifecycle", fmt.Sprintf("%s %s", sym, text))
	renderField("Attachment", string(s.Attachment))
	renderField("Activity", string(s.Activity))
	renderField("Authority", string(s.Authority))

	// Attempt info
	renderField("Attempt Gen", fmt.Sprintf("%d (Total attempts: %d)", s.Generation, s.AttemptCount))

	// Process info
	if s.HasIdentity && s.Identity.PID > 0 {
		renderField("Process PID", fmt.Sprintf("%d", s.Identity.PID))
		renderField("Process PGID", fmt.Sprintf("%d", s.Identity.PGID))
		if s.Identity.StartTicks > 0 {
			renderField("Start Ticks", fmt.Sprintf("%d", s.Identity.StartTicks))
		}
	} else {
		renderField("Process", "No verified live identity")
	}

	// Exit reason
	if s.Reason.Kind != "" {
		renderField("Reason", string(s.Reason.Kind))
		if s.Reason.ExitCode != nil {
			renderField("Exit Code", fmt.Sprintf("%d", *s.Reason.ExitCode))
		}
		if s.Reason.Signal != "" {
			renderField("Signal", s.Reason.Signal)
		}
		if s.Reason.Detail != "" {
			renderField("Detail", s.Reason.Detail)
		}
	}

	// Observation timestamp
	if !s.ObservedAt.IsZero() {
		renderField("Observed At", s.ObservedAt.Format("15:04:05 UTC"))
	}

	content := b.String()
	if width > 0 {
		return lipgloss.NewStyle().MaxWidth(width).Render(content)
	}
	return content
}
