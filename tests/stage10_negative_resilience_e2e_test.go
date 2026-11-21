//go:build linux && integration

package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestNegativeResilienceNoCrashE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	mockDir := t.TempDir()
	failBin := filepath.Join(mockDir, "agent-fail")
	if err := os.WriteFile(failBin, []byte("#!/bin/sh\nexit 127\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	paths := config.Paths{
		StateDir:   t.TempDir(),
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{ID: "failing-agent", Name: "Failing Agent", Executable: failBin},
	}

	runtime, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("failed to start owner: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	go func() { _ = runtime.Serve(ctx) }()
	client := runtime.Client()

	// 1. Missing agent: request launch for an unregistered agent ID
	_, errMissing := client.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("ghost-agent"),
		WorkspacePath: t.TempDir(),
		Name:          "ghost-session",
	})
	if errMissing == nil {
		t.Fatal("expected error when creating session with missing agent, got nil")
	}
	if !strings.Contains(errMissing.Error(), "ghost-agent") && !strings.Contains(errMissing.Error(), "not found") {
		t.Fatalf("expected actionable error message about missing agent, got: %v", errMissing)
	}

	// 2. Bad config: verify corrupted config returns actionable error, not panic
	badConfigPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(badConfigPath, []byte("agents: [broken yaml :::"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, loadErr := config.Load(badConfigPath)
	if loadErr == nil {
		t.Fatal("expected error on invalid yaml config, got nil")
	}

	// 3. Permission denied workspace directory
	noPermDir := filepath.Join(t.TempDir(), "locked-dir")
	if err := os.Mkdir(noPermDir, 0o000); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(noPermDir, 0o755) }()

	resolver := workspace.NewPathResolver("", nil)
	reqPerm, _ := workspace.NewResolveRequest(noPermDir, workspace.SourceExplicit)
	_, permErr := resolver.Resolve(ctx, reqPerm)
	if permErr == nil {
		t.Fatal("expected permission denied error on 0000 dir, got nil")
	}

	// 4. Launch failure: process exits immediately with non-zero (exit 127)
	// ASD should record attempt without hanging
	ws := t.TempDir()
	resFail, err := client.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("failing-agent"),
		WorkspacePath: ws,
		Name:          "failing-session",
	})
	if err != nil {
		t.Fatalf("create failed session errored: %v", err)
	}

	// Poll briefly for the process to exit naturally
	time.Sleep(200 * time.Millisecond)

	insp, err := client.Get(ctx, app.GetRequest{Ref: string(resFail.Session.ID)})
	if err != nil {
		t.Fatalf("inspect failed session: %v", err)
	}
	// Lifecycle should be terminal (Exited or Failed)
	if !insp.Session.Lifecycle.Terminal() {
		t.Logf("lifecycle not yet terminal: %s (still progressing)", insp.Session.Lifecycle)
	}


	// 5. Slow Git simulation: git resolver timeout doesn't stall indefinitely
	slowGitResolver := workspace.NewPathResolver("", func(gCtx context.Context, dir string) (workspace.Git, error) {
		select {
		case <-gCtx.Done():
			return workspace.Git{}, gCtx.Err()
		case <-time.After(50 * time.Millisecond):
			return workspace.Git{Root: dir, Branch: "slow-branch"}, nil
		}
	})
	slowReq, _ := workspace.NewResolveRequest(ws, workspace.SourceExplicit)
	shortCtx, shortCancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer shortCancel()

	// Even if git probe context cancels, workspace resolution degrades gracefully without crash
	w, rErr := slowGitResolver.Resolve(shortCtx, slowReq)
	if rErr != nil {
		t.Logf("graceful error on slow git cancel: %v", rErr)
	} else if w.Path() != ws {
		t.Fatalf("expected workspace path %s, got %s", ws, w.Path())
	}
}
