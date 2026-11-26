//go:build linux

package tests

import (
	"context"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/daemon"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestStage13_MultiAttemptRestartAndHistoryPersistence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	paths, cfg := newStage11TestEnv(t)

	// 1. Start daemon 1
	d1, err := daemon.New(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("start daemon 1: %v", err)
	}
	defer func() { _ = d1.Close() }()

	go func() { _ = d1.Serve(ctx) }()

	socketPath, err := ipc.SocketPath(paths)
	if err != nil {
		t.Fatalf("socket path: %v", err)
	}

	client := ipc.NewClient(socketPath)

	// 2. Create session (Attempt 1)
	wsDir := t.TempDir()
	createRes, err := client.Create(ctx, app.CreateRequest{
		Agent:           agent.ID("fakeagent"),
		WorkspacePath:   wsDir,
		WorkspaceSource: workspace.SourceExplicit,
		Name:            "history-worker",
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	sessID := string(createRes.Session.ID)

	time.Sleep(100 * time.Millisecond)

	// 3. Restart session twice (Attempts 2 and 3)
	restart1, err := client.Restart(ctx, app.RestartRequest{
		Ref:              sessID,
		TargetGeneration: 1,
		Force:            true,
	})
	if err != nil {
		t.Fatalf("Restart 1: %v", err)
	}
	if restart1.Session.Generation != 2 {
		t.Fatalf("expected generation 2, got %d", restart1.Session.Generation)
	}

	time.Sleep(100 * time.Millisecond)

	restart2, err := client.Restart(ctx, app.RestartRequest{
		Ref:              sessID,
		TargetGeneration: 2,
		Force:            true,
	})
	if err != nil {
		t.Fatalf("Restart 2: %v", err)
	}
	if restart2.Session.Generation != 3 {
		t.Fatalf("expected generation 3, got %d", restart2.Session.Generation)
	}

	// 4. Query history via IPC
	histRes, err := client.History(ctx, app.HistoryRequest{
		IncludeAttempts: true,
	})
	if err != nil {
		t.Fatalf("History query: %v", err)
	}
	if len(histRes.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(histRes.Sessions))
	}
	sessHist := histRes.Sessions[0]
	if sessHist.AttemptCount != 3 || len(sessHist.Attempts) != 3 {
		t.Fatalf("expected 3 attempts, got count=%d, slice=%d", sessHist.AttemptCount, len(sessHist.Attempts))
	}
	for i, a := range sessHist.Attempts {
		if a.Generation != uint64(i+1) {
			t.Fatalf("attempt %d generation = %d, want %d", i, a.Generation, i+1)
		}
	}

	// 5. Restart daemon: close d1, start d2 from same paths
	_ = d1.Close()
	time.Sleep(150 * time.Millisecond)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	d2, err := daemon.New(ctx2, paths, cfg)
	if err != nil {
		t.Fatalf("start daemon 2: %v", err)
	}
	defer func() { _ = d2.Close() }()

	go func() { _ = d2.Serve(ctx2) }()

	client2 := ipc.NewClient(socketPath)

	// 6. Verify history persisted completely in d2
	histRes2, err := client2.History(ctx2, app.HistoryRequest{
		IncludeAttempts: true,
	})
	if err != nil {
		t.Fatalf("History query after daemon restart: %v", err)
	}
	if len(histRes2.Sessions) != 1 {
		t.Fatalf("expected 1 session after restart, got %d", len(histRes2.Sessions))
	}
	if histRes2.Sessions[0].AttemptCount != 3 || len(histRes2.Sessions[0].Attempts) != 3 {
		t.Fatalf("expected 3 persisted attempts after restart, got count=%d", histRes2.Sessions[0].AttemptCount)
	}
}
