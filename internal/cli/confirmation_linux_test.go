//go:build linux

package cli

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/spf13/cobra"
)

type confirmationClient struct {
	app.Client
	kills atomic.Int32
	stops atomic.Int32
}

func (c *confirmationClient) Kill(context.Context, app.KillRequest) (app.KillResult, error) {
	c.kills.Add(1)
	return app.KillResult{}, nil
}
func (c *confirmationClient) Stop(context.Context, app.StopRequest) (app.StopResult, error) {
	c.stops.Add(1)
	return app.StopResult{Stopped: true, Session: session.SessionSnapshot{ID: "test-session"}}, nil
}

func TestKillRequiresYesWhenNoTTYButStopDoesNot(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	claim, err := ipc.BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	service := &confirmationClient{}
	server, err := ipc.NewServer(claim, service)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()
	newRoot := func(args ...string) (*cobra.Command, *bytes.Buffer) {
		var stdout, stderr bytes.Buffer
		root := NewRoot(Options{Paths: paths, Config: config.Default(), Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, InputFD: ^uintptr(0), OutputFD: ^uintptr(0)})
		root.SetArgs(args)
		return root, &stdout
	}
	kill, _ := newRoot("kill", "test-session")
	if err := kill.Execute(); session.CodeOf(err) != session.CodeInvalidConfiguration {
		cancel()
		t.Fatalf("unconfirmed kill error = %v", err)
	}
	if got := service.kills.Load(); got != 0 {
		cancel()
		t.Fatalf("Kill calls without confirmation = %d", got)
	}
	stop, _ := newRoot("stop", "test-session")
	if err := stop.Execute(); err != nil {
		cancel()
		t.Fatalf("graceful stop without confirmation: %v", err)
	}
	if got := service.stops.Load(); got != 1 {
		cancel()
		t.Fatalf("Stop calls = %d, want 1", got)
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("owner server did not stop")
	}
}
