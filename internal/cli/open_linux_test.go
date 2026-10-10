//go:build linux

package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestOpenWithoutOwnerReportsMissingPTYBeforeTTYValidation(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	var stdout, stderr bytes.Buffer
	root := NewRoot(Options{Paths: paths, Config: config.Default(), Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, InputFD: ^uintptr(0), OutputFD: ^uintptr(0)})
	root.SetArgs([]string{"open", "session-id"})
	err := root.Execute()
	if session.CodeOf(err) != session.CodeSessionIOFailed {
		t.Fatalf("open without owner error = %v", err)
	}
}

func TestOpenWithOwnerStillRequiresInteractiveTTY(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	claim, err := ipc.BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	server, err := ipc.NewServer(claim, cliListClient{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()
	var stdout, stderr bytes.Buffer
	root := NewRoot(Options{Paths: paths, Config: config.Default(), Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, InputFD: ^uintptr(0), OutputFD: ^uintptr(0)})
	root.SetArgs([]string{"open", "session-id"})
	err = root.Execute()
	if session.CodeOf(err) != session.CodeNotInteractive {
		cancel()
		t.Fatalf("open with owner and no TTY error = %v", err)
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
