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
	var result app.ListResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		cancel()
		t.Fatalf("list JSON %q: %v", stdout.String(), err)
	}
	if result.Snapshot.Authority != session.AuthorityLive || result.Snapshot.Revision != 9 {
		cancel()
		t.Fatalf("list result authority/revision = %q/%d", result.Snapshot.Authority, result.Snapshot.Revision)
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
	var result app.ListResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("list JSON %q: %v", stdout.String(), err)
	}
	if result.Snapshot.Authority != session.AuthorityStored {
		t.Fatalf("offline list authority = %q", result.Snapshot.Authority)
	}
}
