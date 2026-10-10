//go:build linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestNonTTYNewWithOwnerCreatesScriptableSessionWithoutAttaching(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	workspacePath := t.TempDir()
	cfg := config.Default()
	cfg.Agents = []config.Agent{{ID: "sleep", Name: "Sleep", Executable: "/usr/bin/sleep"}}
	ctx, cancel := context.WithCancel(context.Background())
	runtime, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- runtime.Serve(ctx) }()
	var stdout, stderr bytes.Buffer
	root := NewRoot(Options{Paths: paths, Config: cfg, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, InputFD: ^uintptr(0), OutputFD: ^uintptr(0)})
	root.SetArgs([]string{"new", "sleep", workspacePath, "--json", "--", "30"})
	if err := root.Execute(); err != nil {
		cancel()
		t.Fatal(err)
	}
	var created mutationDocument
	if err := json.Unmarshal(stdout.Bytes(), &created); err != nil {
		cancel()
		t.Fatalf("create JSON %q: %v", stdout.String(), err)
	}
	if created.Command != "new" || created.Session.AgentID != "sleep" || created.Session.WorkspaceID != workspacePath {
		cancel()
		t.Fatalf("create result = %+v", created)
	}
	if created.Session.Authority != session.AuthorityLive {
		cancel()
		t.Fatalf("created authority = %q", created.Session.Authority)
	}
	socketPath, err := ipc.SocketPath(paths)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if _, err := ipc.NewClient(socketPath).Kill(context.Background(), app.KillRequest{Ref: created.Session.ID, Signal: session.SignalKill}); err != nil {
		cancel()
		t.Fatalf("cleanup kill: %v", err)
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner server did not stop")
	}
}

func TestNonTTYNewWithoutOwnerDoesNotBootstrapOrSpawn(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	var stdout, stderr bytes.Buffer
	root := NewRoot(Options{Paths: paths, Config: config.Default(), Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr, InputFD: ^uintptr(0), OutputFD: ^uintptr(0)})
	root.SetArgs([]string{"new", "sleep", t.TempDir(), "--json", "--", "30"})
	err := root.Execute()
	if session.CodeOf(err) != session.CodeNotInteractive {
		t.Fatalf("new error = %v, want NOT_INTERACTIVE", err)
	}
	if _, err := os.Stat(paths.LockPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owner lock exists after refusal: %v", err)
	}
}
