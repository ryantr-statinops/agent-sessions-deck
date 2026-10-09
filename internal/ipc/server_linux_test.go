//go:build linux

package ipc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

type listClient struct{ app.Client }

func (listClient) List(context.Context, app.ListRequest) (app.ListResult, error) {
	return app.ListResult{Snapshot: session.Snapshot{Revision: 12}}, nil
}

func TestServerClientHandshakeDispatchAndOwnerExclusion(t *testing.T) {
	stateDir := t.TempDir()
	paths := config.Paths{StateDir: stateDir}
	owner, err := BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := listClient{}
	server, err := NewServer(owner, service)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()
	client := NewClient(mustSocketPath(t, paths))
	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	result, err := client.List(callCtx, app.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.Revision != 12 {
		t.Fatalf("list revision = %d, want 12", result.Snapshot.Revision)
	}
	if _, err := BootstrapPaths(paths); !errors.Is(err, ErrOwnerLocked) {
		t.Fatalf("competing owner error = %v, want ErrOwnerLocked", err)
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop after context cancellation")
	}
}

func mustSocketPath(t *testing.T, paths config.Paths) string {
	t.Helper()
	path, err := SocketPath(paths)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
