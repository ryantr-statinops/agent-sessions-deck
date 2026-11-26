package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// formatBytes returns a human-friendly size representation.
func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// RenderDetailPane formats detailed inspection information for a session.
func RenderDetailPane(s session.SessionSnapshot, theme Theme, width int, procMetrics ...*metrics.ProcessMetrics) string {
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

	if s.AttemptCount > 1 {
		renderField("Past Attempts", fmt.Sprintf("%d previous execution(s) recorded", s.AttemptCount-1))
	}

	// Terminal status notice
	if s.Lifecycle.Terminal() {
		renderField("Terminal", "Closed: session terminated (no live PTY)")
	} else if s.Lifecycle.Active() {
		renderField("Terminal", "Active live PTY available")
	}

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

	// Metrics & Resource Usage
	var m *metrics.ProcessMetrics
	if len(procMetrics) > 0 {
		m = procMetrics[0]
	}

	b.WriteString("\n" + theme.DetailTitle.Render("── METRICS & RESOURCE USAGE ──") + "\n\n")
	if m != nil && !m.IsZero() {
		if m.Unavailable {
			renderField("Metrics Status", fmt.Sprintf("unavailable (%s)", m.Error))
		} else {
			uptimeStr := fmt.Sprintf("%.1fs", m.Uptime.Seconds())
			if m.Uptime >= time.Minute {
				uptimeStr = fmt.Sprintf("%dm %ds", int(m.Uptime.Minutes()), int(m.Uptime.Seconds())%60)
			}
			renderField("Uptime", uptimeStr)
			renderField("CPU Usage", fmt.Sprintf("%.1f%%", m.CPUPercent))
			renderField("Memory RSS", formatBytes(m.MemoryRSSBytes))
			if m.TreeRSSBytes > 0 {
				renderField("Tree RSS", formatBytes(m.TreeRSSBytes))
			}
			if m.ProcessCount > 0 {
				renderField("Process Count", fmt.Sprintf("%d", m.ProcessCount))
			}
			staleNotice := ""
			if m.Stale {
				staleNotice = " [STALE]"
			}
			renderField("Sampled At", m.SampledAt.Format("15:04:05 UTC")+staleNotice)
		}
	} else if s.Lifecycle.Active() {
		renderField("Metrics", "Pending observation")
	} else {
		renderField("Metrics", "Not running")
	}

	content := b.String()
	if width > 0 {
		return lipgloss.NewStyle().MaxWidth(width).Render(content)
	}
	return content
}

// RenderDetailPaneCompact formats inspection information for constrained viewports.
func RenderDetailPaneCompact(s session.SessionSnapshot, theme Theme, width int, procMetrics ...*metrics.ProcessMetrics) string {
	var b strings.Builder

	title := theme.DetailTitle.Render("── INSPECTOR ──")
	b.WriteString(title + "\n")

	renderField := func(label, value string) {
		l := theme.DetailLabel.Width(10).Render(label + ":")
		v := theme.DetailValue.Render(value)
		b.WriteString(fmt.Sprintf("%s %s\n", l, v))
	}

	renderField("ID", string(s.ID))
	renderField("Name", s.Name)
	renderField("Agent", string(s.AgentID))
	renderField("Workspace", TruncatePath(string(s.WorkspaceID), max(10, width-14)))

	sym, text := StatusIndicator(s.Lifecycle, theme.NoColor)
	renderField("Status", fmt.Sprintf("%s %s", sym, text))

	if s.HasIdentity && s.Identity.PID > 0 {
		renderField("PID", fmt.Sprintf("%d", s.Identity.PID))
	}

	var m *metrics.ProcessMetrics
	if len(procMetrics) > 0 {
		m = procMetrics[0]
	}

	if m != nil && !m.IsZero() && !m.Unavailable {
		b.WriteString(theme.DetailTitle.Render("── METRICS ──") + "\n")
		uptimeStr := fmt.Sprintf("%.0fs", m.Uptime.Seconds())
		if m.Uptime >= time.Minute {
			uptimeStr = fmt.Sprintf("%dm%ds", int(m.Uptime.Minutes()), int(m.Uptime.Seconds())%60)
		}
		renderField("Up", uptimeStr)
		renderField("CPU", fmt.Sprintf("%.1f%%", m.CPUPercent))
		renderField("RSS", formatBytes(m.MemoryRSSBytes))
		if m.TreeRSSBytes > 0 {
			renderField("Tree", formatBytes(m.TreeRSSBytes))
		}
		if m.ProcessCount > 0 {
			renderField("Procs", fmt.Sprintf("%d", m.ProcessCount))
		}
	}

	content := b.String()
	if width > 0 {
		return lipgloss.NewStyle().MaxWidth(width).Render(content)
	}
	return content
}
