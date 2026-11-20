//go:build linux

package owner

import (
	"context"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestOwnerGracefulShutdown(t *testing.T) {
	paths := config.Paths{
		StateDir:   t.TempDir(),
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{
			ID:         "sh",
			Name:       "Shell",
			Executable: "/bin/sh",
		},
	}

	ctx := context.Background()
	rt, err := Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("Start runtime: %v", err)
	}

	ws := t.TempDir()
	// Create two cooperative sessions
	res1, err := rt.Client().Create(ctx, app.CreateRequest{
		Agent:         "sh",
		WorkspacePath: ws,
		Name:          "coop-1",
		ExtraArgs:     []string{"-c", "sleep 30"},
	})
	if err != nil {
		_ = rt.Close()
		t.Fatalf("Create sess 1: %v", err)
	}
	res2, err := rt.Client().Create(ctx, app.CreateRequest{
		Agent:         "sh",
		WorkspacePath: ws,
		Name:          "coop-2",
		ExtraArgs:     []string{"-c", "sleep 30"},
	})
	if err != nil {
		_ = rt.Close()
		t.Fatalf("Create sess 2: %v", err)
	}

	// Graceful shutdown with 2s grace window
	report, err := rt.Shutdown(ctx, 2*time.Second)
	if err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	if report.TotalSessions != 2 {
		t.Fatalf("expected 2 total sessions, got %d", report.TotalSessions)
	}
	if report.StoppedSessions != 2 {
		t.Fatalf("expected 2 stopped sessions, got %d", report.StoppedSessions)
	}
	if len(report.ResidualPIDs) != 0 {
		t.Fatalf("expected 0 residual PIDs, got %v", report.ResidualPIDs)
	}

	// Verify both sessions reached LifecycleExited in store
	off, err := Offline(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("Offline service: %v", err)
	}
	s1, _ := off.Get(ctx, app.GetRequest{Ref: string(res1.Session.ID)})
	if s1.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("session 1 lifecycle = %v, want Exited", s1.Session.Lifecycle)
	}
	s2, _ := off.Get(ctx, app.GetRequest{Ref: string(res2.Session.ID)})
	if s2.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("session 2 lifecycle = %v, want Exited", s2.Session.Lifecycle)
	}
}
