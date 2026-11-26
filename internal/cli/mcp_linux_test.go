//go:build linux

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestMCPCommandHelp(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	cfg := config.Default()

	var stdout, stderr bytes.Buffer
	root := NewRoot(Options{
		Paths:  paths,
		Config: cfg,
		Stdout: &stdout,
		Stderr: &stderr,
	})

	root.SetArgs([]string{"mcp", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("mcp --help: %v", err)
	}

	help := stdout.String()
	if !strings.Contains(help, "Model Context Protocol") || !strings.Contains(help, "--read-only") || !strings.Contains(help, "--allow-mutations") {
		t.Fatalf("unexpected help output: %s", help)
	}
}

func TestMCPCommandDaemonAbsentReturnsActionableError(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	cfg := config.Default()

	var stdout, stderr bytes.Buffer
	root := NewRoot(Options{
		Paths:  paths,
		Config: cfg,
		Stdout: &stdout,
		Stderr: &stderr,
	})

	root.SetArgs([]string{"mcp", "serve"})
	err := root.ExecuteContext(context.Background())
	if err == nil {
		t.Fatalf("expected error when daemon is absent")
	}

	if session.CodeOf(err) != session.CodeOwnerUnavailable {
		t.Fatalf("expected CodeOwnerUnavailable error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "daemon is not running") {
		t.Fatalf("expected actionable daemon error message, got: %v", err)
	}
}
