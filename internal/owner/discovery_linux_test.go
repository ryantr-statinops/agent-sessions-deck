//go:build linux

package owner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
)

func TestForegroundRegistersOnlyIdentityVerifiedBuiltins(t *testing.T) {
	binDir := t.TempDir()
	writeExecutable(t, filepath.Join(binDir, "codex"), "codex CLI version test\n")
	writeExecutable(t, filepath.Join(binDir, "opencode"), "unrelated executable\n")
	t.Setenv("PATH", binDir)
	runtime, err := Start(context.Background(), config.Paths{StateDir: t.TempDir()}, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	result, err := runtime.Client().Scan(context.Background(), app.ScanRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Agents) != 1 || result.Agents[0].ID != "codex" {
		t.Fatalf("registered agents = %+v, want only verified codex", result.Agents)
	}
}

func writeExecutable(t *testing.T, path, output string) {
	t.Helper()
	script := "#!/bin/sh\nprintf '%s' '" + output + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
