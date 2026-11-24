//go:build linux

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

type cliWorkspacesClient struct {
	app.Client
}

func (cliWorkspacesClient) Workspaces(context.Context, app.WorkspacesRequest) (app.WorkspacesResult, error) {
	ws, _ := workspace.New("/home/tester/project")
	ws, _ = ws.WithGit(workspace.Git{
		Root:   "/home/tester/project",
		Branch: "main",
		Dirty:  true,
	})
	return app.WorkspacesResult{
		Authority:  session.AuthorityLive,
		ObservedAt: time.Now().UTC(),
		Workspaces: []workspace.WorkspaceSummary{
			{
				Workspace:      ws,
				Git:            &workspace.Git{Root: "/home/tester/project", Branch: "main", Dirty: true},
				ActiveCount:    1,
				TotalCount:     2,
				SessionIDs:     []string{"s-1", "s-2"},
				LastObservedAt: time.Now().UTC(),
			},
		},
	}, nil
}

func TestWorkspaceListJSON(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	claim, err := ipc.BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	server, err := ipc.NewServer(claim, cliWorkspacesClient{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	var stdout, stderr bytes.Buffer
	root := NewRoot(Options{
		Paths:  paths,
		Config: config.Default(),
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	root.SetArgs([]string{"workspace", "list", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	var doc workspacesDocument
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal json %s: %v", stdout.String(), err)
	}
	if doc.Command != "workspaces" || doc.Authority != session.AuthorityLive {
		t.Fatalf("unexpected doc header: %+v", doc)
	}
	if len(doc.Workspaces) != 1 || doc.Workspaces[0].Path != "/home/tester/project" {
		t.Fatalf("unexpected workspaces: %+v", doc.Workspaces)
	}
	if doc.Workspaces[0].Git == nil || doc.Workspaces[0].Git.Branch != "main" || !doc.Workspaces[0].Git.Dirty {
		t.Fatalf("unexpected git info: %+v", doc.Workspaces[0].Git)
	}
}

func TestWorkspaceListHumanTable(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	claim, err := ipc.BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	server, err := ipc.NewServer(claim, cliWorkspacesClient{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	var stdout, stderr bytes.Buffer
	root := NewRoot(Options{
		Paths:  paths,
		Config: config.Default(),
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	root.SetArgs([]string{"workspace", "list"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	outStr := stdout.String()
	if !strings.Contains(outStr, "WORKSPACE\tGIT\tACTIVE\tTOTAL\tSESSIONS") {
		t.Fatalf("missing table header in output:\n%s", outStr)
	}
	if !strings.Contains(outStr, "/home/tester/project\tmain*\t1\t2\ts-1, s-2") {
		t.Fatalf("missing expected row in output:\n%s", outStr)
	}
}

func TestWorkspacesAlias(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	claim, err := ipc.BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	server, err := ipc.NewServer(claim, cliWorkspacesClient{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = server.Serve(ctx) }()

	var stdout, stderr bytes.Buffer
	root := NewRoot(Options{
		Paths:  paths,
		Config: config.Default(),
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	root.SetArgs([]string{"workspaces", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}

	var doc workspacesDocument
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("unmarshal json %s: %v", stdout.String(), err)
	}
	if len(doc.Workspaces) != 1 {
		t.Fatalf("expected 1 workspace via alias, got %d", len(doc.Workspaces))
	}
}
