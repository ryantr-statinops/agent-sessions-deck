//go:build linux && integration

package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func writeInteractiveMockAgent(t *testing.T, dir, name, marker string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "` + marker + `"
  exit 0
fi
echo "MOCK_AGENT_STARTED: ` + name + `"
while true; do
  read -r line || exit 0
  echo "ACK: $line"
done
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write interactive mock %s: %v", p, err)
	}
	return p
}

func TestConcreteProviderPTYLaunchesSmoke(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	mockDir := t.TempDir()
	writeInteractiveMockAgent(t, mockDir, "claude", "claude code v1.0.0 (Anthropic)")
	writeInteractiveMockAgent(t, mockDir, "codex", "codex CLI v1.0.0")
	writeInteractiveMockAgent(t, mockDir, "opencode", "opencode version 1.0.0")
	genericBin := writeInteractiveMockAgent(t, mockDir, "custom-sh", "custom v1.0.0")

	t.Setenv("PATH", mockDir+":"+os.Getenv("PATH"))

	paths := config.Paths{
		StateDir:   t.TempDir(),
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}

	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{ID: "custom-sh", Name: "Custom Agent", Executable: genericBin},
	}

	runtime, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("failed to start owner runtime: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	go func() { _ = runtime.Serve(ctx) }()
	client := runtime.Client()

	providersToTest := []struct {
		id   string
		name string
	}{
		{"claude", "Claude Code"},
		{"codex", "Codex CLI"},
		{"opencode", "OpenCode"},
		{"custom-sh", "Custom Agent"},
	}


	for _, p := range providersToTest {
		ws := t.TempDir()
		createRes, err := client.Create(ctx, app.CreateRequest{
			Agent:         agent.ID(p.id),
			WorkspacePath: ws,
			Name:          "smoke-" + p.id,
		})
		if err != nil {
			t.Fatalf("failed to create session for %s: %v", p.id, err)
		}

		sid := string(createRes.Session.ID)

		// Open interactive terminal PTY
		openRes, err := client.Open(ctx, app.OpenRequest{
			Ref:    sid,
			Holder: "smoke-test-" + p.id,
		})
		if err != nil {
			t.Fatalf("failed to open session terminal for %s: %v", p.id, err)
		}

		// Send input to interactive PTY
		if _, err := openRes.Terminal.Write([]byte("ping\n")); err != nil {
			t.Fatalf("failed to send input to %s: %v", p.id, err)
		}

		// Read at least one frame from PTY
		_, rErr := openRes.Terminal.ReadFrame()
		if rErr != nil {
			t.Logf("read frame warning for %s: %v", p.id, rErr)
		}

		_ = openRes.Terminal.Close()

		// Stop session gracefully
		_, sErr := client.Stop(ctx, app.StopRequest{
			Ref:   sid,
			Grace: 2 * time.Second,
		})

		if sErr != nil {
			_, _ = client.Kill(context.Background(), app.KillRequest{Ref: sid, Signal: session.SignalKill})
		}
	}
}
