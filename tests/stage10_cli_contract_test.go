//go:build linux && integration

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/cli"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestCLIContractAllV1Commands(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	mockDir := t.TempDir()
	shBin := filepath.Join(mockDir, "agent-cli-sh")
	shScript := `#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "mock-agent 1.0.0"
  exit 0
fi
while true; do
  read -r line || exit 0
  echo "ACK: $line"
done
`
	if err := os.WriteFile(shBin, []byte(shScript), 0o755); err != nil {
		t.Fatal(err)
	}

	paths := config.Paths{
		StateDir:   t.TempDir(),
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{ID: "cli-agent", Name: "CLI Agent", Executable: shBin},
	}

	runtime, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("failed to start owner: %v", err)
	}
	defer func() { _ = runtime.Close() }()

	go func() { _ = runtime.Serve(ctx) }()

	runCLI := func(args ...string) (string, string, error) {
		var outBuf, errBuf bytes.Buffer
		root := cli.NewRoot(cli.Options{
			Paths:  paths,
			Config: cfg,
			Stdout: &outBuf,
			Stderr: &errBuf,
		})
		root.Version = "1.0.0"
		root.SetVersionTemplate("asd version {{.Version}}\n")
		root.SetArgs(args)

		execErr := root.ExecuteContext(ctx)
		return outBuf.String(), errBuf.String(), execErr
	}

	// 1. asd --help & asd --version
	out, _, err := runCLI("--help")
	if err != nil || len(out) == 0 {
		t.Fatalf("--help failed: %v, out: %s", err, out)
	}

	out, _, err = runCLI("--version")
	if err != nil || len(out) == 0 {
		t.Fatalf("--version failed: %v, out: %s", err, out)
	}

	// 2. asd scan & scan --json
	out, _, err = runCLI("scan", "--json")
	if err != nil {
		t.Fatalf("scan --json failed: %v", err)
	}
	var scanData map[string]any
	if err := json.Unmarshal([]byte(out), &scanData); err != nil {
		t.Fatalf("scan json parse error: %v", err)
	}

	// 3. asd new <agent> <workspace> --json
	ws := t.TempDir()
	out, errOut, err := runCLI("new", "cli-agent", ws, "--name=cli-sess", "--json")

	if err != nil {
		t.Fatalf("new --json failed: %v\nstderr: %s", err, errOut)
	}
	var newRes map[string]any
	if err := json.Unmarshal([]byte(out), &newRes); err != nil {
		t.Fatalf("new json parse failed: %v, out: %s", err, out)
	}
	sessObj, ok := newRes["session"].(map[string]any)
	if !ok {
		t.Fatalf("expected session in new output: %+v", newRes)
	}
	sid, _ := sessObj["id"].(string)
	if sid == "" {
		t.Fatalf("session id empty in new output")
	}

	// 4. asd inspect <id> --json
	out, _, err = runCLI("inspect", sid, "--json")
	if err != nil {
		t.Fatalf("inspect --json failed: %v", err)
	}
	var inspectRes map[string]any
	if err := json.Unmarshal([]byte(out), &inspectRes); err != nil {
		t.Fatalf("inspect json parse failed: %v", err)
	}

	// 5. asd list --json
	out, _, err = runCLI("list", "--json")
	if err != nil {
		t.Fatalf("list --json failed: %v", err)
	}
	var listRes map[string]any
	if err := json.Unmarshal([]byte(out), &listRes); err != nil {
		t.Fatalf("list json parse failed: %v", err)
	}
	sessionsList, _ := listRes["sessions"].([]any)
	if len(sessionsList) != 1 {
		t.Fatalf("expected 1 session in list, got %d", len(sessionsList))
	}

	// 6. asd rename <id> <newname> --json
	out, _, err = runCLI("rename", sid, "renamed-cli-sess", "--json")
	if err != nil {
		t.Fatalf("rename --json failed: %v", err)
	}

	// 7. asd restart <id> --force --yes --json
	out, _, err = runCLI("restart", sid, "--force", "--yes", "--json")
	if err != nil {
		t.Fatalf("restart --json failed: %v", err)
	}

	// 8. asd stop <id> --json
	out, _, err = runCLI("stop", sid, "--json")
	if err != nil {
		t.Fatalf("stop --json failed: %v", err)
	}

	// 9. asd new and asd kill <id> --yes --json
	ws2 := t.TempDir()
	out2, _, err := runCLI("new", "cli-agent", ws2, "--name=to-kill", "--json")
	if err != nil {
		t.Fatalf("new for kill failed: %v", err)
	}
	var newRes2 map[string]any
	_ = json.Unmarshal([]byte(out2), &newRes2)
	sessObj2 := newRes2["session"].(map[string]any)
	sid2 := sessObj2["id"].(string)

	out, _, err = runCLI("kill", sid2, "--yes", "--json")
	if err != nil {
		t.Fatalf("kill --json failed: %v", err)
	}

	// 10. Negative contract: inspect non-existent ID
	_, _, err = runCLI("inspect", "s-nonexistent", "--json")
	if err == nil {
		t.Fatalf("expected error for non-existent session inspect")
	}

	// Clean up
	_, _ = runtime.Client().Kill(context.Background(), app.KillRequest{Ref: sid, Signal: session.SignalKill})
}
