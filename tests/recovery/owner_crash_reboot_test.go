//go:build linux

package recovery

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestOwnerCrashAndRebootReconciliation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	paths := config.Paths{
		StateDir:   t.TempDir(),
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{
			ID:         "sh-agent",
			Name:       "Shell Agent",
			Executable: "/bin/sh",
		},
	}

	// 1. Start Owner 1
	rt1, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("failed to start owner 1: %v", err)
	}

	ws := t.TempDir()
	c1 := rt1.Client()

	// 2. Launch Session 1: long-running process (will survive owner crash as orphan)
	res1, err := c1.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("sh-agent"),
		WorkspacePath: ws,
		Name:          "survivor",
		ExtraArgs:     []string{"-c", "sleep 30"},
	})
	if err != nil {
		_ = rt1.Close()
		t.Fatalf("failed to create session 1: %v", err)
	}
	pid1 := res1.Session.Identity.PID

	// Ensure process 1 is actually alive
	if err := syscall.Kill(pid1, 0); err != nil {
		_ = rt1.Close()
		t.Fatalf("session 1 process %d is not alive: %v", pid1, err)
	}

	// Clean up child process 1 at test end
	defer func() {
		_ = syscall.Kill(pid1, syscall.SIGKILL)
	}()

	// 3. Launch Session 2: fast process that exits immediately
	res2, err := c1.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("sh-agent"),
		WorkspacePath: ws,
		Name:          "quick-exit",
		ExtraArgs:     []string{"-c", "exit 0"},
	})
	if err != nil {
		_ = rt1.Close()
		t.Fatalf("failed to create session 2: %v", err)
	}

	// Wait for session 2 process to terminate
	time.Sleep(200 * time.Millisecond)

	// 4. Simulate Crash of Owner 1 by closing without session cleanup
	if err := rt1.Close(); err != nil {
		t.Fatalf("owner 1 close failed: %v", err)
	}

	// Verify process 1 is still alive in the system (surviving orphan)
	if err := syscall.Kill(pid1, 0); err != nil {
		t.Fatalf("session 1 process %d died prematurely: %v", pid1, err)
	}

	// 5. Reboot Owner 2 on the exact same directory paths
	rt2, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("failed to start owner 2 after crash: %v", err)
	}
	defer func() { _ = rt2.Close() }()

	c2 := rt2.Client()

	// 6. Inspect Session 1: Must be classified as Orphaned
	s1, err := c2.Get(ctx, app.GetRequest{Ref: string(res1.Session.ID)})
	if err != nil {
		t.Fatalf("failed to get session 1 from owner 2: %v", err)
	}
	if !s1.Session.Orphaned() {
		t.Fatalf("session 1 was not marked as Orphaned: %+v", s1.Session)
	}

	// 7. Inspect Session 2: Must be classified as Exited / Dead
	s2, err := c2.Get(ctx, app.GetRequest{Ref: string(res2.Session.ID)})
	if err != nil {
		t.Fatalf("failed to get session 2 from owner 2: %v", err)
	}
	if s2.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("session 2 lifecycle = %s, want exited: %+v", s2.Session.Lifecycle, s2.Session)
	}

	// 8. Attempt to Open Session 1: Must be refused with SESSION_IO_FAILED
	_, openErr := c2.Open(ctx, app.OpenRequest{Ref: string(res1.Session.ID), Holder: "test-cli"})
	if openErr == nil {
		t.Fatal("Open on surviving orphan succeeded, want SESSION_IO_FAILED refusal")
	}
	if code := session.CodeOf(openErr); code != session.CodeSessionIOFailed {
		t.Fatalf("Open error code = %s, want %s: %v", code, session.CodeSessionIOFailed, openErr)
	}

	// 9. Attempt automatic Restart of Session 1: Must be blocked without explicit resolution
	_, restartErr := c2.Restart(ctx, app.RestartRequest{Ref: string(res1.Session.ID)})
	if restartErr == nil {
		t.Fatal("Restart on surviving orphan succeeded, want conflict refusal")
	}
	if code := session.CodeOf(restartErr); code != session.CodeConflict {
		t.Fatalf("Restart error code = %s, want %s: %v", code, session.CodeConflict, restartErr)
	}

	// 10. Kill the surviving process externally (operator intervention)
	_ = syscall.Kill(pid1, syscall.SIGKILL)
	time.Sleep(100 * time.Millisecond)

	// Verify the process is now gone
	if err := syscall.Kill(pid1, 0); err == nil {
		t.Fatalf("process %d is still alive after SIGKILL", pid1)
	}

	// 11. Report reconciled death once the orphan is terminated
	reportRes, err := rt2.Service().Report(ctx, app.ReportRequest{
		Ref:     string(res1.Session.ID),
		Attempt: 1,
		Kind:    app.ReportReconciled,
		Liveness: session.LivenessObservation{
			Outcome: session.ProbeGone,
			Detail:  "operator terminated surviving orphan",
			At:      time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("failed to report reconciled death: %v", err)
	}
	if reportRes.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("session 1 lifecycle after death report = %s, want exited", reportRes.Session.Lifecycle)
	}
}
