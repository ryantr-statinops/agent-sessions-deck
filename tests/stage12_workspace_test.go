//go:build linux

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/cli"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestStage12_WorkspaceGroupingAcceptance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	mockDir := t.TempDir()
	shBin := filepath.Join(mockDir, "agent-loop")
	shScript := `#!/bin/sh
while true; do
  sleep 1
done
`
	if err := os.WriteFile(shBin, []byte(shScript), 0o755); err != nil {
		t.Fatal(err)
	}

	// Create Git Repo A
	repoA := t.TempDir()
	cmdInit := exec.Command("git", "init", "-b", "feat-stage12", repoA)
	if err := cmdInit.Run(); err != nil {
		t.Fatalf("git init repoA: %v", err)
	}
	repoASubdir := filepath.Join(repoA, "services", "backend")
	if err := os.MkdirAll(repoASubdir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create Non-git Workspace B
	wsB := t.TempDir()

	paths := config.Paths{
		StateDir:   t.TempDir(),
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{ID: "mock-agent", Name: "Mock Agent", Executable: shBin},
	}

	runtime, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("start owner: %v", err)
	}
	defer func() { _ = runtime.Close() }()
	go func() { _ = runtime.Serve(ctx) }()

	client := runtime.Client()

	// Launch session 1 in Repo A
	s1, err := client.Create(ctx, app.CreateRequest{
		Agent:           "mock-agent",
		WorkspacePath:   repoA,
		WorkspaceSource: workspace.SourceExplicit,
		Name:            "sess-repo-a-1",
	})
	if err != nil {
		t.Fatalf("create sess 1: %v", err)
	}

	// Launch session 2 in Repo A
	s2, err := client.Create(ctx, app.CreateRequest{
		Agent:           "mock-agent",
		WorkspacePath:   repoA,
		WorkspaceSource: workspace.SourceExplicit,
		Name:            "sess-repo-a-2",
	})
	if err != nil {
		t.Fatalf("create sess 2: %v", err)
	}

	// Launch session 3 in Repo A Subdir
	s3, err := client.Create(ctx, app.CreateRequest{
		Agent:           "mock-agent",
		WorkspacePath:   repoASubdir,
		WorkspaceSource: workspace.SourceExplicit,
		Name:            "sess-repo-a-sub",
	})
	if err != nil {
		t.Fatalf("create sess 3: %v", err)
	}

	// Launch session 4 in Workspace B
	s4, err := client.Create(ctx, app.CreateRequest{
		Agent:           "mock-agent",
		WorkspacePath:   wsB,
		WorkspaceSource: workspace.SourceExplicit,
		Name:            "sess-ws-b",
	})
	if err != nil {
		t.Fatalf("create sess 4: %v", err)
	}

	// Stop session 2 so it is not active
	if _, err := client.Stop(ctx, app.StopRequest{Ref: string(s2.Session.ID)}); err != nil {
		t.Fatalf("stop sess 2: %v", err)
	}

	_ = s1
	_ = s3
	_ = s4

	// Query workspaces via client
	wsResult, err := client.Workspaces(ctx, app.WorkspacesRequest{})
	if err != nil {
		t.Fatalf("client.Workspaces: %v", err)
	}

	if len(wsResult.Workspaces) != 3 {
		t.Fatalf("expected 3 workspaces, got %d", len(wsResult.Workspaces))
	}

	// Verify details
	foundRepoA := false
	foundRepoASub := false
	foundWsB := false

	for _, ws := range wsResult.Workspaces {
		cleanPath := filepath.Clean(ws.Workspace.Path())
		if cleanPath == filepath.Clean(repoA) {
			foundRepoA = true
			if ws.TotalCount != 2 || ws.ActiveCount != 1 {
				t.Fatalf("repoA counts: total=%d, active=%d (want 2, 1)", ws.TotalCount, ws.ActiveCount)
			}
			if ws.Git == nil || ws.Git.Branch != "feat-stage12" {
				t.Fatalf("repoA git branch mismatch: %+v", ws.Git)
			}
		} else if cleanPath == filepath.Clean(repoASubdir) {
			foundRepoASub = true
			if ws.TotalCount != 1 || ws.ActiveCount != 1 {
				t.Fatalf("repoASubdir counts: total=%d, active=%d (want 1, 1)", ws.TotalCount, ws.ActiveCount)
			}
			if !workspace.IsSubdirectoryOfGitRoot(ws.Workspace) {
				t.Fatalf("expected repoASubdir to be detected as Git subdirectory")
			}
		} else if cleanPath == filepath.Clean(wsB) {
			foundWsB = true
			if ws.TotalCount != 1 || ws.ActiveCount != 1 {
				t.Fatalf("wsB counts: total=%d, active=%d (want 1, 1)", ws.TotalCount, ws.ActiveCount)
			}
			if ws.Git != nil {
				t.Fatalf("expected nil Git for non-git wsB: %+v", ws.Git)
			}
		}
	}

	if !foundRepoA || !foundRepoASub || !foundWsB {
		t.Fatalf("missing expected workspaces: repoA=%v, repoASub=%v, wsB=%v", foundRepoA, foundRepoASub, foundWsB)
	}

	// CLI Verification: asd workspace list --json
	var stdout, stderr bytes.Buffer
	root := cli.NewRoot(cli.Options{
		Paths:  paths,
		Config: cfg,
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	root.SetArgs([]string{"workspace", "list", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatalf("cli workspace list --json failed: %v, stderr=%s", err, stderr.String())
	}

	var jsonOutput struct {
		Command    string                       `json:"command"`
		Authority  session.Authority            `json:"authority"`
		Workspaces []workspace.WorkspaceSummary `json:"workspaces"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &jsonOutput); err != nil {
		t.Fatalf("unmarshal cli output: %v; raw:\n%s", err, stdout.String())
	}
	if jsonOutput.Command != "workspaces" {
		t.Fatalf("expected command 'workspaces', got %q", jsonOutput.Command)
	}
	if len(jsonOutput.Workspaces) != 3 {
		t.Fatalf("expected 3 workspaces in CLI json output, got %d", len(jsonOutput.Workspaces))
	}
}
