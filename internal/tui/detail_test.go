package tui

import (
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
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
