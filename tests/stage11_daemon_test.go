//go:build linux

package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/daemon"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func newStage11TestEnv(t *testing.T) (config.Paths, config.Config) {
	root, err := os.MkdirTemp("", "asd-d11-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	paths := config.Paths{
		ConfigDir:  filepath.Join(root, "config"),
		StateDir:   filepath.Join(root, "state"),
		RuntimeDir: filepath.Join(root, "runtime"),
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{
			ID:         "fakeagent",
			Name:       "Fake Agent",
			Executable: "/bin/sh",
			Args:       []string{"-c", "while true; do sleep 1; done"},
		},
	}
	return paths, cfg
}

func TestStage11_SessionPersistenceAcrossClientDisconnect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	paths, cfg := newStage11TestEnv(t)

	// 1. Start daemon
	d, err := daemon.New(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	defer func() { _ = d.Close() }()

	go func() { _ = d.Serve(ctx) }()

	socketPath, err := ipc.SocketPath(paths)
	if err != nil {
		t.Fatalf("socket path: %v", err)
	}

	client1 := ipc.NewClient(socketPath)

	// 2. Launch two sessions
	res1, err := client1.Create(ctx, app.CreateRequest{
		Agent:           agent.ID("fakeagent"),
		WorkspacePath:   t.TempDir(),
		WorkspaceSource: workspace.SourceExplicit,
		Name:            "Session 1",
	})
	if err != nil {
		t.Fatalf("create session 1: %v", err)
	}

	res2, err := client1.Create(ctx, app.CreateRequest{
		Agent:           agent.ID("fakeagent"),
		WorkspacePath:   t.TempDir(),
		WorkspaceSource: workspace.SourceExplicit,
		Name:            "Session 2",
	})
	if err != nil {
		t.Fatalf("create session 2: %v", err)
	}

	// 3. Open session 1 (simulate TUI attaching)
	opened, err := client1.Open(ctx, app.OpenRequest{
		Ref:    string(res1.Session.ID),
		Holder: "tui-client-1",
	})
	if err != nil {
		t.Fatalf("open session 1: %v", err)
	}

	// Write some input
	_, _ = opened.Terminal.Write([]byte("echo hello\n"))

	// 4. Close client connection (simulate TUI closing/detaching)
	_ = opened.Terminal.Close()

	// 5. Connect as a new independent client (simulate reopening ASD or CLI list)
	client2 := ipc.NewClient(socketPath)
	listRes, err := client2.List(ctx, app.ListRequest{})
	if err != nil {
		t.Fatalf("list sessions from new client: %v", err)
	}

	if len(listRes.Snapshot.Sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(listRes.Snapshot.Sessions))
	}

	// Both sessions must still be RUNNING
	for _, s := range listRes.Snapshot.Sessions {
		if s.Lifecycle != session.LifecycleRunning {
			t.Fatalf("session %s lifecycle expected RUNNING, got %s", s.ID, s.Lifecycle)
		}
	}

	// 6. Reopen session 1 from client2: same attempt/generation and valid terminal
	reopened, err := client2.Open(ctx, app.OpenRequest{
		Ref:    string(res1.Session.ID),
		Holder: "tui-client-2",
	})
	if err != nil {
		t.Fatalf("reopen session 1 from client2: %v", err)
	}
	defer reopened.Terminal.Close()

	if reopened.Session.Generation != res1.Session.Generation {
		t.Fatalf("expected same generation %d, got %d", res1.Session.Generation, reopened.Session.Generation)
	}

	// Cleanup
	_, _ = client2.Kill(ctx, app.KillRequest{Ref: string(res1.Session.ID), Signal: session.SignalKill})
	_, _ = client2.Kill(ctx, app.KillRequest{Ref: string(res2.Session.ID), Signal: session.SignalKill})
}

func TestStage11_ConcurrentDaemonStartsOnlyOneOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	paths, cfg := newStage11TestEnv(t)

	d1, err := daemon.New(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("daemon 1 start: %v", err)
	}
	defer func() { _ = d1.Close() }()

	// Second daemon on same paths must fail
	_, err2 := daemon.New(ctx, paths, cfg)
	if err2 == nil {
		t.Fatalf("expected second daemon start to fail due to lock, but succeeded")
	}
}

func TestStage11_SocketAndDirectoryPermissions(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	paths, cfg := newStage11TestEnv(t)

	d, err := daemon.New(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	defer func() { _ = d.Close() }()

	socketPath, err := ipc.SocketPath(paths)
	if err != nil {
		t.Fatalf("socket path: %v", err)
	}

	sockStat, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if sockStat.Mode().Perm() != 0o600 {
		t.Fatalf("expected socket perms 0600, got %o", sockStat.Mode().Perm())
	}

	dirStat, err := os.Stat(filepath.Dir(socketPath))
	if err != nil {
		t.Fatalf("stat socket dir: %v", err)
	}
	if dirStat.Mode().Perm() != 0o700 {
		t.Fatalf("expected socket dir perms 0700, got %o", dirStat.Mode().Perm())
	}
}
