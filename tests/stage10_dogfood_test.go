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

// TestTwoAgentDogfoodingWorkflow runs a realistic two-agent concurrent workflow:
// Session 1: Reviewer Agent (interactive feedback loop)
// Session 2: Test Runner Agent (automated test runner)
func TestTwoAgentDogfoodingWorkflow(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	mockDir := t.TempDir()
	reviewerBin := filepath.Join(mockDir, "reviewer-agent")
	reviewerScript := `#!/bin/sh
echo "REVIEWER_READY"
while true; do
  read -r line || exit 0
  echo "REVIEW_COMMENT: $line"
done
`
	if err := os.WriteFile(reviewerBin, []byte(reviewerScript), 0o755); err != nil {
		t.Fatal(err)
	}

	runnerBin := filepath.Join(mockDir, "runner-agent")
	runnerScript := `#!/bin/sh
echo "RUNNER_READY"
while true; do
  read -r cmd || exit 0
  echo "RUN_RESULT_OK: $cmd"
done
`
	if err := os.WriteFile(runnerBin, []byte(runnerScript), 0o755); err != nil {
		t.Fatal(err)
	}

	paths := config.Paths{
		StateDir:   t.TempDir(),
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{ID: "reviewer", Name: "Reviewer Agent", Executable: reviewerBin},
		{ID: "runner", Name: "Test Runner Agent", Executable: runnerBin},
	}

	runtime, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("failed to start owner runtime: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	go func() { _ = runtime.Serve(ctx) }()
	client := runtime.Client()

	ws1 := t.TempDir()
	ws2 := t.TempDir()

	// 1. Launch Reviewer Session
	s1, err := client.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("reviewer"),
		WorkspacePath: ws1,
		Name:          "code-review-task",
	})
	if err != nil {
		t.Fatalf("create reviewer session failed: %v", err)
	}

	// 2. Launch Test Runner Session
	s2, err := client.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("runner"),
		WorkspacePath: ws2,
		Name:          "test-execution-task",
	})
	if err != nil {
		t.Fatalf("create runner session failed: %v", err)
	}

	// 3. Attach and interact with Reviewer
	open1, err := client.Open(ctx, app.OpenRequest{
		Ref:    string(s1.Session.ID),
		Holder: "dogfood-user",
	})
	if err != nil {
		t.Fatalf("open reviewer session failed: %v", err)
	}

	if _, err := open1.Terminal.Write([]byte("LGTM on commit 123\n")); err != nil {
		t.Fatalf("write to reviewer failed: %v", err)
	}

	// Verify Reviewer produces frame
	_, _ = open1.Terminal.ReadFrame()
	_ = open1.Terminal.Close()
	_, _ = client.Detach(ctx, app.DetachRequest{Ref: string(s1.Session.ID)})

	// 4. Switch and interact with Test Runner
	open2, err := client.Open(ctx, app.OpenRequest{
		Ref:    string(s2.Session.ID),
		Holder: "dogfood-user",
	})
	if err != nil {
		t.Fatalf("open runner session failed: %v", err)
	}

	if _, err := open2.Terminal.Write([]byte("run all tests\n")); err != nil {
		t.Fatalf("write to runner failed: %v", err)
	}

	_, _ = open2.Terminal.ReadFrame()
	_ = open2.Terminal.Close()
	_, _ = client.Detach(ctx, app.DetachRequest{Ref: string(s2.Session.ID)})

	// 5. Verify both sessions are healthy and concurrent in List
	listRes, err := client.List(ctx, app.ListRequest{})
	if err != nil {
		t.Fatalf("list sessions failed: %v", err)
	}
	if len(listRes.Snapshot.Sessions) != 2 {
		t.Fatalf("expected 2 concurrent sessions in list, got %d", len(listRes.Snapshot.Sessions))
	}

	// 6. Graceful cleanup of both dogfood sessions
	for _, sess := range listRes.Snapshot.Sessions {
		_, _ = client.Stop(ctx, app.StopRequest{Ref: string(sess.ID), Grace: 2 * time.Second})
		_, _ = client.Kill(context.Background(), app.KillRequest{Ref: string(sess.ID), Signal: session.SignalKill})
	}

}
