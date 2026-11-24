package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestRenderDetailPane(t *testing.T) {
	theme := NewTheme()
	exitCode := 0
	s := session.SessionSnapshot{
		ID:           session.ID("s-12345"),
		Name:         "kestrel-worker",
		AgentID:      agent.ID("opencode"),
		WorkspaceID:  workspace.ID("/home/dev/kestrel"),
		Lifecycle:    session.LifecycleRunning,
		Attachment:   session.AttachmentAttached,
		Authority:    session.AuthorityLive,
		Generation:   2,
		AttemptCount: 3,
		HasIdentity:  true,
		Identity: session.ProcessIdentity{
			PID:  9988,
			PGID: 9988,
		},
		Reason: session.Reason{
			Kind:     session.ReasonStopped,
			Detail:   "Graceful shutdown requested",
			ExitCode: &exitCode,
		},
	}

	detail := RenderDetailPane(s, theme, 60)

	expectedStrings := []string{
		"SESSION INSPECTOR",
		"s-12345",
		"kestrel-worker",
		"opencode",
		"/home/dev/kestrel",
		"Running",
		"attached",
		"9988",
		"stopped",
	}

	for _, exp := range expectedStrings {
		if !strings.Contains(detail, exp) {
			t.Fatalf("expected detail pane to contain %q, but got:\n%s", exp, detail)
		}
	}
}

func TestRenderDetailPaneWithMetrics(t *testing.T) {
	theme := NewTheme()
	s := session.SessionSnapshot{
		ID:          session.ID("s-metric-test"),
		Name:        "metrics-runner",
		AgentID:     agent.ID("opencode"),
		WorkspaceID: workspace.ID("/home/dev/kestrel"),
		Lifecycle:   session.LifecycleRunning,
		Authority:   session.AuthorityLive,
	}

	m := &metrics.ProcessMetrics{
		PID:            4455,
		CPUPercent:     23.4,
		MemoryRSSBytes: 1024 * 1024 * 64, // 64 MB
		TreeRSSBytes:   1024 * 1024 * 128,
		ProcessCount:   3,
		Uptime:         125 * time.Second,
		SampledAt:      time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC),
	}

	detail := RenderDetailPane(s, theme, 80, m)

	expected := []string{
		"METRICS & RESOURCE USAGE",
		"2m 5s",
		"23.4%",
		"64.0 MB",
		"128.0 MB",
		"3",
		"12:00:00 UTC",
	}

	for _, exp := range expected {
		if !strings.Contains(detail, exp) {
			t.Fatalf("expected detail pane to contain %q, but got:\n%s", exp, detail)
		}
	}
}

func TestRenderDetailPaneCompact(t *testing.T) {
	theme := NewTheme()
	s := session.SessionSnapshot{
		ID:          session.ID("s-compact"),
		Name:        "compact-app",
		AgentID:     agent.ID("opencode"),
		WorkspaceID: workspace.ID("/home/dev/short"),
		Lifecycle:   session.LifecycleRunning,
		HasIdentity: true,
		Identity: session.ProcessIdentity{
			PID: 1234,
		},
	}
	m := &metrics.ProcessMetrics{
		PID:            1234,
		CPUPercent:     5.5,
		MemoryRSSBytes: 1024 * 1024 * 16,
		Uptime:         30 * time.Second,
	}

	compact := RenderDetailPaneCompact(s, theme, 40, m)

	expected := []string{
		"INSPECTOR",
		"ID:",
		"s-compact",
		"PID:",
		"1234",
		"METRICS",
		"CPU:",
		"5.5%",
		"RSS:",
		"16.0 MB",
	}

	for _, exp := range expected {
		if !strings.Contains(compact, exp) {
			t.Fatalf("expected compact detail to contain %q, but got:\n%s", exp, compact)
		}
	}
}
