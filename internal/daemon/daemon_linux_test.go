//go:build linux

package daemon

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func testPaths(t *testing.T) (config.Paths, config.Config) {
	root := t.TempDir()
	paths := config.Paths{
		ConfigDir:  filepath.Join(root, "config"),
		StateDir:   filepath.Join(root, "state"),
		RuntimeDir: filepath.Join(root, "runtime"),
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{
			ID:         "testagent",
			Name:       "Test Agent",
			Executable: "/bin/sh",
			Args:       []string{"-c", "sleep 30"},
		},
	}
	return paths, cfg
}

func TestDaemonLifecycleAndStatus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	paths, cfg := testPaths(t)

	// Status before starting
	rep, err := Status(ctx, paths)
	if err != nil {
		t.Fatalf("status before start failed: %v", err)
	}
	if rep.Status != "STOPPED" {
		t.Fatalf("expected STOPPED, got %s", rep.Status)
	}

	d, err := New(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("daemon.New failed: %v", err)
	}
	defer func() { _ = d.Close() }()

	go func() {
		_ = d.Serve(ctx)
	}()

	// Query status while running
	rep, err = Status(ctx, paths)
	if err != nil {
		t.Fatalf("status while running failed: %v", err)
	}
	if rep.Status != "RUNNING" {
		t.Fatalf("expected RUNNING, got %s", rep.Status)
	}
	if rep.InstanceID != d.InstanceID() {
		t.Fatalf("instance ID mismatch: %s vs %s", rep.InstanceID, d.InstanceID())
	}
	if rep.PID <= 0 {
		t.Fatalf("expected positive PID, got %d", rep.PID)
	}
	if rep.ActiveSessions != 0 {
		t.Fatalf("expected 0 active sessions, got %d", rep.ActiveSessions)
	}
}

func TestDaemonConflictWithV1Owner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	paths, cfg := testPaths(t)

	// Start V1 foreground owner
	v1Owner, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("owner.Start failed: %v", err)
	}
	defer func() { _ = v1Owner.Close() }()

	go func() {
		_ = v1Owner.Serve(ctx)
	}()

	// Check status via daemon: must detect CONFLICT
	rep, err := Status(ctx, paths)
	if err != nil {
		t.Fatalf("daemon.Status during V1 owner failed: %v", err)
	}
	if rep.Status != "CONFLICT" {
		t.Fatalf("expected CONFLICT status, got %s", rep.Status)
	}

	// Attempting to start daemon must fail with clear rejection
	_, dErr := New(ctx, paths, cfg)
	if dErr == nil {
		t.Fatalf("expected daemon.New to fail when V1 owner is running, but it succeeded")
	}
	if !strings.Contains(dErr.Error(), "V1 foreground owner") {
		t.Fatalf("expected error mentioning V1 foreground owner, got: %v", dErr)
	}
}

func TestDaemonStopProtectionWithActiveSessions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	paths, cfg := testPaths(t)

	d, err := New(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("daemon.New failed: %v", err)
	}
	defer func() { _ = d.Close() }()

	go func() {
		_ = d.Serve(ctx)
	}()

	socketPath, _ := ipc.SocketPath(paths)
	client := ipc.NewClient(socketPath)

	// Create an active session
	createRes, err := client.Create(ctx, app.CreateRequest{
		Agent:           agent.ID("testagent"),
		WorkspacePath:   t.TempDir(),
		WorkspaceSource: workspace.SourceExplicit,
		Name:            "Active Job",
	})
	if err != nil {
		t.Fatalf("create session failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	rep, err := Status(ctx, paths)
	if err != nil {
		t.Fatalf("status query failed: %v", err)
	}
	if rep.ActiveSessions != 1 {
		t.Fatalf("expected 1 active session, got %d", rep.ActiveSessions)
	}

	// 1. Stop without --force must fail
	stopErr := Stop(ctx, paths, false, 1*time.Second)
	if stopErr == nil {
		t.Fatalf("expected Stop without --force to fail due to active session")
	}
	var domErr *session.Error
	if !errors.As(stopErr, &domErr) || domErr.Code != session.CodeConflict {
		t.Fatalf("expected session.CodeConflict error, got: %v", stopErr)
	}

	// 2. Stop with --force must succeed
	_, _ = client.Kill(ctx, app.KillRequest{Ref: string(createRes.Session.ID), Signal: session.SignalKill})
	forceErr := Stop(ctx, paths, true, 2*time.Second)
	if forceErr != nil {
		t.Fatalf("expected Stop with --force to succeed, got: %v", forceErr)
	}
}
