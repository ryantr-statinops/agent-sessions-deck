//go:build linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/process"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

type cliListClient struct{ app.Client }

func (cliListClient) List(context.Context, app.ListRequest) (app.ListResult, error) {
	return app.ListResult{Snapshot: session.Snapshot{Authority: session.AuthorityLive, Revision: 9}}, nil
}

func TestListJSONUsesLiveOwnerWhenAvailable(t *testing.T) {
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
	root := NewRoot(Options{Paths: paths, Config: config.Default(), Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr})
	root.SetArgs([]string{"list", "--json"})
	if err := root.Execute(); err != nil {
		cancel()
		t.Fatal(err)
	}
	var result listDocument
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		cancel()
		t.Fatalf("list JSON %q: %v", stdout.String(), err)
	}
	if result.Authority != session.AuthorityLive || result.Revision != 9 || result.Command != "list" {
		cancel()
		t.Fatalf("list authority/revision/command = %q/%d/%q", result.Authority, result.Revision, result.Command)
	}
	if bytes.Contains(stdout.Bytes(), []byte("Snapshot")) {
		cancel()
		t.Fatalf("JSON uses Go field names: %s", stdout.String())
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

func TestListJSONFallsBackToStoredAuthorityWithoutOwner(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	var stdout, stderr bytes.Buffer
	root := NewRoot(Options{Paths: paths, Config: config.Default(), Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr})
	root.SetArgs([]string{"list", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var result listDocument
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("list JSON %q: %v", stdout.String(), err)
	}
	if result.Authority != session.AuthorityStored || result.Command != "list" {
		t.Fatalf("offline list authority/command = %q/%q", result.Authority, result.Command)
	}
}

func TestOfflineRenameReconcilesStoredActiveProcessBeforeMutation(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	cfg := config.Default()
	cfg.Agents = []config.Agent{{ID: "sleep", Name: "Sleep", Executable: "/usr/bin/sleep"}}
	ownerCtx := context.Background()
	first, err := owner.Start(ownerCtx, paths, cfg)
	if err != nil {
		t.Fatal(err)
	}
	serverCtx, stopServer := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- first.Serve(serverCtx) }()
	created, err := first.Client().Create(context.Background(), app.CreateRequest{Agent: "sleep", WorkspacePath: t.TempDir(), ExtraArgs: []string{"5"}})
	if err != nil {
		stopServer()
		<-serveDone
		t.Fatal(err)
	}
	stopServer()
	if err := <-serveDone; err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	root := NewRoot(Options{Paths: paths, Config: cfg, Stdin: bytes.NewReader(nil), Stdout: &stdout, Stderr: &stderr})
	root.SetArgs([]string{"rename", string(created.Session.ID), "offline-renamed", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("offline rename: %v; stderr=%s", err, stderr.String())
	}
	var result mutationDocument
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("rename JSON %q: %v", stdout.String(), err)
	}
	if result.Authority != session.AuthorityStored || result.Session.Name != "offline-renamed" {
		t.Fatalf("offline rename result = authority %q, name %q", result.Authority, result.Session.Name)
	}
	if result.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("stored active process was not reconciled before rename: lifecycle %q", result.Session.Lifecycle)
	}

	deadline := time.Now().Add(6 * time.Second)
	for {
		if observation := process.Observe(created.Session.Identity); observation.Outcome == session.ProbeGone {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture sleep process did not exit naturally")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
