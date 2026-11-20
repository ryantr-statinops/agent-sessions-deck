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

func TestUncooperativeChildSIGTERMTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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

	rt, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("failed to start owner: %v", err)
	}
	defer func() { _ = rt.Close() }()

	c := rt.Client()
	ws := t.TempDir()

	// 1. Launch a child that traps and ignores SIGTERM
	res, err := c.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("sh-agent"),
		WorkspacePath: ws,
		Name:          "uncooperative",
		ExtraArgs:     []string{"-c", "trap '' TERM; exec sleep 60"},
	})
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	pid := res.Session.Identity.PID
	defer func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}()

	// Wait briefly for trap to establish
	time.Sleep(100 * time.Millisecond)

	// 2. Request graceful Stop with a bounded window of 400ms
	stopRes, stopErr := c.Stop(ctx, app.StopRequest{
		Ref:   string(res.Session.ID),
		Grace: 400 * time.Millisecond,
	})

	// 3. Verify truthful reporting: stop timed out, did NOT report stopped
	if stopRes.Stopped {
		t.Fatal("Stop reported Stopped: true, want false for uncooperative child")
	}
	if !stopRes.TimedOut {
		t.Fatal("Stop did not report TimedOut: true")
	}
	if stopErr == nil {
		t.Fatal("Stop returned nil error on timeout, want typed timeout error")
	}
	if code := session.CodeOf(stopErr); code != session.CodeConflict {
		t.Fatalf("Stop error code = %s, want %s: %v", code, session.CodeConflict, stopErr)
	}

	// 4. Verify process is STILL alive — NO silent SIGKILL escalation!
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("process %d was silently killed or died: %v", pid, err)
	}

	// 5. Verify session lifecycle remains running (truthful state)
	current, err := c.Get(ctx, app.GetRequest{Ref: string(res.Session.ID)})
	if err != nil {
		t.Fatalf("failed to get session: %v", err)
	}
	if current.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("session lifecycle = %s, want running: %+v", current.Session.Lifecycle, current.Session)
	}

	// 6. Explicit Force Kill: Only explicit SIGKILL terminates the uncooperative child
	killRes, err := c.Kill(ctx, app.KillRequest{
		Ref:    string(res.Session.ID),
		Signal: session.SignalKill,
	})
	if err != nil {
		t.Fatalf("explicit Kill failed: %v", err)
	}
	if killRes.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("lifecycle after kill = %s, want exited", killRes.Session.Lifecycle)
	}

	// 7. Verify process is now confirmed terminated
	time.Sleep(50 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("process %d still alive after explicit SIGKILL", pid)
	}
}
