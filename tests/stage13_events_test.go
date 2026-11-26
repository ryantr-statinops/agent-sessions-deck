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

func TestStage13_EventJournalPersistenceAndMonotonicResync(t *testing.T) {
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

	// 2. Create and stop a session in daemon 1
	wsDir := t.TempDir()
	createRes, err := client.Create(ctx, app.CreateRequest{
		Agent:           agent.ID("fakeagent"),
		WorkspacePath:   wsDir,
		WorkspaceSource: workspace.SourceExplicit,
		Name:            "event-worker-1",
	})
	if err != nil {
		t.Fatalf("Create session: %v", err)
	}
	sessID := string(createRes.Session.ID)

	time.Sleep(100 * time.Millisecond)

	_, err = client.Stop(ctx, app.StopRequest{Ref: sessID})
	if err != nil {
		t.Fatalf("Stop session: %v", err)
	}

	// 3. Query events from daemon 1
	eventsRes1, err := client.Events(ctx, app.EventsRequest{Limit: 50})
	if err != nil {
		t.Fatalf("Events query daemon 1: %v", err)
	}
	if len(eventsRes1.Events) == 0 {
		t.Fatalf("expected events in daemon 1, got 0")
	}

	var lastRevD1 uint64
	for i, ev := range eventsRes1.Events {
		rev := uint64(ev.Revision())
		if rev <= lastRevD1 {
			t.Fatalf("event %d revision %d did not advance past previous %d", i, rev, lastRevD1)
		}
		lastRevD1 = rev
	}

	// 4. Restart daemon: close d1, start d2
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

	// 5. Create a new session in daemon 2
	wsDir2 := t.TempDir()
	_, err = client2.Create(ctx2, app.CreateRequest{
		Agent:           agent.ID("fakeagent"),
		WorkspacePath:   wsDir2,
		WorkspaceSource: workspace.SourceExplicit,
		Name:            "event-worker-2",
	})
	if err != nil {
		t.Fatalf("Create session in daemon 2: %v", err)
	}

	// 6. Query events strictly after daemon 1's last revision (cursor resync)
	resyncRes, err := client2.Events(ctx2, app.EventsRequest{
		AfterRevision: lastRevD1,
		Limit:         50,
	})
	if err != nil {
		t.Fatalf("resync events query: %v", err)
	}
	if len(resyncRes.Events) == 0 {
		t.Fatalf("expected new events after revision %d, got 0", lastRevD1)
	}

	for _, ev := range resyncRes.Events {
		rev := uint64(ev.Revision())
		if rev <= lastRevD1 {
			t.Fatalf("resynced event revision %d is not strictly greater than %d", rev, lastRevD1)
		}
	}
}
