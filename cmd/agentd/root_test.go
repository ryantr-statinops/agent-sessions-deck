package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/daemon"
)

func TestAgentdHelpAndVersion(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{
		StateDir:   filepath.Join(root, "state"),
		ConfigDir:  filepath.Join(root, "config"),
		RuntimeDir: filepath.Join(root, "runtime"),
	}
	cfg := config.Default()

	// 1. --help
	var helpOut, helpErr bytes.Buffer
	cmd := newRootCommand(paths, cfg, &helpOut, &helpErr)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("--help failed: %v", err)
	}
	if !strings.Contains(helpOut.String(), "agentd") {
		t.Fatalf("expected help output to mention agentd: %s", helpOut.String())
	}

	// 2. --version
	var verOut, verErr bytes.Buffer
	cmdVer := newRootCommand(paths, cfg, &verOut, &verErr)
	cmdVer.SetArgs([]string{"--version"})
	if err := cmdVer.Execute(); err != nil {
		t.Fatalf("--version failed: %v", err)
	}
	if !strings.Contains(verOut.String(), "agentd version") {
		t.Fatalf("expected version output to contain 'agentd version': %s", verOut.String())
	}
}

func TestAgentdStatusStopped(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{
		StateDir:   filepath.Join(root, "state"),
		ConfigDir:  filepath.Join(root, "config"),
		RuntimeDir: filepath.Join(root, "runtime"),
	}
	cfg := config.Default()

	var out, errBuf bytes.Buffer
	cmd := newRootCommand(paths, cfg, &out, &errBuf)
	cmd.SetArgs([]string{"status"})
	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !strings.Contains(out.String(), "STOPPED") {
		t.Fatalf("expected status output to report STOPPED, got: %s", out.String())
	}

	// JSON format
	out.Reset()
	cmdJSON := newRootCommand(paths, cfg, &out, &errBuf)
	cmdJSON.SetArgs([]string{"status", "--json"})
	if err := cmdJSON.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("status --json failed: %v", err)
	}
	var rep daemon.StatusReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("unmarshal status json: %v", err)
	}
	if rep.Status != "STOPPED" {
		t.Fatalf("expected rep.Status == STOPPED, got %s", rep.Status)
	}
}
