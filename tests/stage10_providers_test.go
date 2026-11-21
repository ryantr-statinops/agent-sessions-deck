package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/discovery"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/providers/claude"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/providers/codex"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/providers/generic"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/providers/opencode"
)

func writeMockScript(t *testing.T, dir, name, output string, exitCode int) string {
	t.Helper()
	p := filepath.Join(dir, name)
	content := "#!/bin/sh\n"
	if output != "" {
		content += "echo '" + output + "'\n"
	}
	if exitCode != 0 {
		content += "exit " + string(rune('0'+exitCode)) + "\n"
	} else {
		content += "exit 0\n"
	}
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("failed to write mock script %s: %v", p, err)
	}
	return p
}

func TestProviderVersionMarkerSimulation(t *testing.T) {
	temp := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	prober := discovery.NewProber(discovery.ProbeOptions{
		Timeout:   time.Second,
		MaxOutput: 1024,
		TTL:       time.Minute,
	})

	// 1. Claude Code valid mock
	claudePath := writeMockScript(t, temp, "claude-mock", "claude code v1.0.12 (Anthropic)", 0)
	claudeRes := claude.Verify(ctx, prober, claudePath)
	if claudeRes.Status != discovery.StatusAvailable {
		t.Fatalf("expected claude mock available, got: %+v", claudeRes)
	}

	// 2. Codex valid mock
	codexPath := writeMockScript(t, temp, "codex-mock", "codex CLI version 0.8.4", 0)
	codexRes := codex.Verify(ctx, prober, codexPath)
	if codexRes.Status != discovery.StatusAvailable {
		t.Fatalf("expected codex mock available, got: %+v", codexRes)
	}

	// 3. OpenCode valid mock
	opencodePath := writeMockScript(t, temp, "opencode-mock", "opencode engine 1.4.0-rc1", 0)
	opencodeRes := opencode.Verify(ctx, prober, opencodePath)
	if opencodeRes.Status != discovery.StatusAvailable {
		t.Fatalf("expected opencode mock available, got: %+v", opencodeRes)
	}

	// 4. Impostor claude binary (no marker in output)
	impostorPath := writeMockScript(t, temp, "claude-fake", "generic unrelated tool v0.1", 0)
	impostorRes := claude.Verify(ctx, prober, impostorPath)
	if impostorRes.Status != discovery.StatusUncertain {
		t.Fatalf("expected impostor to be uncertain, got: %+v", impostorRes)
	}

	// 5. Lookalike Linux system utilities (orca and omp collision defense)
	orcaPath := writeMockScript(t, temp, "orca", "GNOME Orca Screen Reader 45.0", 0)
	orcaRes := discovery.RequireMarker(prober.Probe(ctx, orcaPath), "opencode")
	if orcaRes.Status != discovery.StatusUncertain {
		t.Fatalf("orca screen reader should not match opencode marker: %+v", orcaRes)
	}

	ompPath := writeMockScript(t, temp, "omp", "Open Media Player v2.1", 0)
	ompRes := discovery.RequireMarker(prober.Probe(ctx, ompPath), "claude")
	if ompRes.Status != discovery.StatusUncertain {
		t.Fatalf("omp media player should not match claude marker: %+v", ompRes)
	}

	// 6. Generic provider accepts explicit binary without probe
	genProv, err := generic.New("custom-tool", "Custom Tool", "/bin/sh", []string{"-c", "echo hello"})
	if err != nil {
		t.Fatalf("generic.New failed: %v", err)
	}
	req, _ := agent.NewResolveRequest(temp, "custom-session", []string{"extra-flag"})
	cmd, err := genProv.Resolve(ctx, req)
	if err != nil {
		t.Fatalf("generic.Resolve failed: %v", err)
	}
	if cmd.Executable() != "/bin/sh" || len(cmd.Args()) != 3 {
		t.Fatalf("unexpected generic command: %+v", cmd)
	}

}
