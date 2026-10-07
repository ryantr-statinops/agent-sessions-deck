package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/discovery"
)

func TestVerifyRequiresMarkerNotBinaryName(t *testing.T) {
	root := t.TempDir()
	impostor := filepath.Join(root, "claude")
	os.WriteFile(impostor, []byte("#!/bin/sh\necho 'orca-screen-reader 2.0'\n"), 0o755)
	real := filepath.Join(root, "notclaude")
	os.WriteFile(real, []byte("#!/bin/sh\necho 'Claude Code 1.0'\n"), 0o755)
	p := discovery.NewProber(discovery.ProbeOptions{Timeout: 500 * time.Millisecond})
	if r := Verify(context.Background(), p, impostor); r.Status == discovery.StatusAvailable {
		t.Errorf("impostor named claude must not be available: %+v", r)
	}
	if r := Verify(context.Background(), p, real); r.Status != discovery.StatusAvailable {
		t.Errorf("real marker output must be available: %+v", r)
	}
}

func TestResolveLiteralArgv(t *testing.T) {
	p, err := New("/usr/bin/claude", nil)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := agent.NewResolveRequest("/tmp/ws", "n", []string{"; echo pwned"})
	cmd, err := p.Resolve(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	a0, ok := cmd.Arg(0)
	if !ok || a0 != "; echo pwned" || len(cmd.Args()) != 1 {
		t.Errorf("args = %#v", cmd.Args())
	}
}
