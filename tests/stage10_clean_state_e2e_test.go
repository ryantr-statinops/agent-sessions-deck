//go:build linux && integration

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/cli"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/tui"
)

func TestCleanStateFreshInstallE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Clean isolated environment (no prior ~/.config/asd, ~/.local/state/asd, etc.)
	cleanHome := t.TempDir()
	paths := config.Paths{
		ConfigDir:  filepath.Join(cleanHome, ".config", "asd"),
		StateDir:   filepath.Join(cleanHome, ".local", "state", "asd"),
		RuntimeDir: filepath.Join(cleanHome, ".local", "run", "asd"),
	}
	cfg := config.Default()

	// 1. Test `asd scan --json` in clean state
	var scanOut, scanErr bytes.Buffer
	rootCmd := cli.NewRoot(cli.Options{
		Paths:  paths,
		Config: cfg,
		Stdout: &scanOut,
		Stderr: &scanErr,
	})
	rootCmd.SetArgs([]string{"scan", "--json"})

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("asd scan --json failed in clean state: %v\nstderr: %s", err, scanErr.String())
	}

	var scanData map[string]any
	if err := json.Unmarshal(scanOut.Bytes(), &scanData); err != nil {
		t.Fatalf("failed to parse scan json output: %v\nraw: %s", err, scanOut.String())
	}
	if _, ok := scanData["agents"]; !ok {
		t.Fatalf("expected agents field in scan json: %+v", scanData)
	}


	// 2. Test `asd list --json` on clean install (must return empty sessions array)
	var listOut, listErr bytes.Buffer
	listCmd := cli.NewRoot(cli.Options{
		Paths:  paths,
		Config: cfg,
		Stdout: &listOut,
		Stderr: &listErr,
	})

	listCmd.SetArgs([]string{"list", "--json"})

	if err := listCmd.ExecuteContext(ctx); err != nil {
		t.Fatalf("asd list --json failed in clean state: %v\nstderr: %s", err, listErr.String())
	}

	var listData map[string]any
	if err := json.Unmarshal(listOut.Bytes(), &listData); err != nil {
		t.Fatalf("failed to parse list json output: %v\nraw: %s", err, listOut.String())
	}
	sessions, ok := listData["sessions"].([]any)
	if !ok || len(sessions) != 0 {
		t.Fatalf("expected empty sessions list on clean install, got: %+v", listData)
	}

	// 3. Test TUI Model initialization in clean state
	m := tui.NewModel(tui.Options{
		Paths:  paths,
		Config: cfg,
	})

	view := m.View()
	if !strings.Contains(view, "No sessions found") {
		t.Fatalf("expected clean state empty guide in TUI view, got:\n%s", view)
	}
	if !strings.Contains(view, "Press 'n' to launch a new session") {
		t.Fatalf("expected 'n' shortcut instruction in empty state view, got:\n%s", view)
	}
}
