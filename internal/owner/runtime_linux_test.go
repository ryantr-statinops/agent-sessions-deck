//go:build linux

package owner

import (
	"context"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestForegroundAndOfflineServicesUseTheirAuthorityAndLockModes(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	runtime, err := Start(ctx, paths, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- runtime.Serve(ctx) }()
	path, err := ipc.SocketPath(paths)
	if err != nil {
		t.Fatal(err)
	}
	client := ipc.NewClient(path)
	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	live, err := client.List(callCtx, app.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if live.Snapshot.Authority != session.AuthorityLive {
		t.Fatalf("live authority = %q", live.Snapshot.Authority)
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("foreground server did not release its lock")
	}
	offline, err := Offline(context.Background(), paths, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := offline.List(context.Background(), app.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Snapshot.Authority != session.AuthorityStored {
		t.Fatalf("offline authority = %q", stored.Snapshot.Authority)
	}
}
