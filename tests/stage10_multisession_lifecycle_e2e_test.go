//go:build linux && integration

package tests

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestMultiSessionFullLifecycleE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	mockDir := t.TempDir()
	shBin := filepath.Join(mockDir, "agent-sh")
	shScript := `#!/bin/sh
echo "STARTED_AGENT"
while true; do
  read -r cmd || exit 0
  echo "ECHO: $cmd"
done
`
	if err := os.WriteFile(shBin, []byte(shScript), 0o755); err != nil {
		t.Fatal(err)
	}

	paths := config.Paths{
		StateDir:   t.TempDir(),
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{ID: "mock-sh", Name: "Mock Shell", Executable: shBin},
	}

	runtime, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("failed to start owner: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	go func() { _ = runtime.Serve(ctx) }()
	client := runtime.Client()

	// 1. Session 1 in a Git workspace
	gitDir := t.TempDir()
	cmdInit := exec.Command("git", "init", "-b", "main", gitDir)
	if err := cmdInit.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	res1, err := client.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("mock-sh"),
		WorkspacePath: gitDir,
		Name:          "session-alpha",
	})
	if err != nil {
		t.Fatalf("create session 1 failed: %v", err)
	}
	id1 := string(res1.Session.ID)

	// Attach & interact with Session 1
	open1, err := client.Open(ctx, app.OpenRequest{Ref: id1, Holder: "tui-client"})
	if err != nil {
		t.Fatalf("open session 1 failed: %v", err)
	}
	if _, err := open1.Terminal.Write([]byte("hello session 1\n")); err != nil {
		t.Fatalf("write to session 1 failed: %v", err)
	}
	_ = open1.Terminal.Close()

	// Detach Session 1
	if _, err := client.Detach(ctx, app.DetachRequest{Ref: id1}); err != nil {
		t.Fatalf("detach session 1 failed: %v", err)
	}

	// 2. Session 2 in a Plain folder (No Git)
	plainDir := t.TempDir()
	res2, err := client.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("mock-sh"),
		WorkspacePath: plainDir,
		Name:          "session-beta",
	})
	if err != nil {
		t.Fatalf("create session 2 failed: %v", err)
	}
	id2 := string(res2.Session.ID)

	// Attach & switch between sessions
	open2, err := client.Open(ctx, app.OpenRequest{Ref: id2, Holder: "tui-client"})
	if err != nil {
		t.Fatalf("open session 2 failed: %v", err)
	}
	_ = open2.Terminal.Close()
	if _, err := client.Detach(ctx, app.DetachRequest{Ref: id2}); err != nil {
		t.Fatalf("detach session 2 failed: %v", err)
	}

	// 3. Rename Session 1
	renameRes, err := client.Rename(ctx, app.RenameRequest{
		Ref:  id1,
		Name: "renamed-alpha",
	})
	if err != nil {
		t.Fatalf("rename session 1 failed: %v", err)
	}
	if renameRes.Session.Name != "renamed-alpha" {
		t.Fatalf("expected new name 'renamed-alpha', got %q", renameRes.Session.Name)
	}

	// 4. Stop Session 1
	stopRes, err := client.Stop(ctx, app.StopRequest{
		Ref:   id1,
		Grace: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("stop session 1 failed: %v", err)
	}
	if stopRes.Session.Lifecycle != session.LifecycleExited && stopRes.Session.Lifecycle != session.LifecycleStopping {
		t.Fatalf("expected session 1 stopped, got state: %s", stopRes.Session.Lifecycle)
	}

	// 5. Restart Session 1
	restartRes, err := client.Restart(ctx, app.RestartRequest{Ref: id1})
	if err != nil {
		t.Fatalf("restart session 1 failed: %v", err)
	}
	if restartRes.Session.Generation <= res1.Session.Generation {
		t.Fatalf("expected generation incremented on restart: %d <= %d", restartRes.Session.Generation, res1.Session.Generation)
	}

	// 6. Explicit Kill on Session 2
	killRes, err := client.Kill(ctx, app.KillRequest{
		Ref:    id2,
		Signal: session.SignalKill,
	})
	if err != nil {
		t.Fatalf("kill session 2 failed: %v", err)
	}
	if killRes.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("expected session 2 exited after kill, got: %s", killRes.Session.Lifecycle)
	}


	// Clean up session 1
	_, _ = client.Kill(context.Background(), app.KillRequest{Ref: id1, Signal: session.SignalKill})
}
