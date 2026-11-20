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

func TestOwnerSignalTraps(t *testing.T) {
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
	created, err := rt.Client().Create(ctx, app.CreateRequest{
		Agent:         "sh",
		WorkspacePath: ws,
		Name:          "sig-test",
		ExtraArgs:     []string{"-c", "sleep 30"},
	})
	if err != nil {
		_ = rt.Close()
		t.Fatalf("Create session: %v", err)
	}

	// Install signal handler and then cancel it to verify clean teardown
	doneCalled := make(chan ShutdownReport, 1)
	cancel := HandleSignals(ctx, rt, 1*time.Second, func(rep ShutdownReport, err error) {
		doneCalled <- rep
	})
	cancel() // teardown listener cleanly

	// Trigger simulated signal shutdown
	triggerSignalShutdown(rt, 1*time.Second, func(rep ShutdownReport, err error) {
		doneCalled <- rep
	})

	select {
	case rep := <-doneCalled:
		if rep.StoppedSessions != 1 {
			t.Fatalf("expected 1 stopped session, got %d", rep.StoppedSessions)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for signal shutdown completion")
	}

	// Verify session exited in store
	off, err := Offline(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("Offline service: %v", err)
	}
	s, err := off.Get(ctx, app.GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("expected LifecycleExited, got %v", s.Session.Lifecycle)
	}
}
