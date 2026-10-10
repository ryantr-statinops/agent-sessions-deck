//go:build linux

package owner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestForegroundAndOfflineServicesUseTheirAuthorityAndLockModes(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	runtime, err := Start(ctx, paths, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- runtime.Serve(ctx) }()
	path, err := ipc.SocketPath(paths)
	if err != nil {
		t.Fatal(err)
	}
	client := ipc.NewClient(path)
	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()
	live, err := client.List(callCtx, app.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if live.Snapshot.Authority != session.AuthorityLive {
		t.Fatalf("live authority = %q", live.Snapshot.Authority)
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("foreground server did not release its lock")
	}
	offline, err := Offline(context.Background(), paths, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := offline.List(context.Background(), app.ListRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if stored.Snapshot.Authority != session.AuthorityStored {
		t.Fatalf("offline authority = %q", stored.Snapshot.Authority)
	}
}

func TestProbeAgentsDistinguishesAvailableMissingAndUncertain(t *testing.T) {
	binDir := t.TempDir()
	for name, output := range map[string]string{"codex": "codex version test\n", "opencode": "unrelated executable\n"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' "+"'"+output+"'"+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir)
	probes, err := ProbeAgents(context.Background(), config.Default())
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]AgentProbe, len(probes))
	for _, probe := range probes {
		byID[string(probe.ID)] = probe
	}
	if byID["codex"].Status != "available" || byID["codex"].Path == "" {
		t.Fatalf("codex probe = %+v", byID["codex"])
	}
	if byID["claude"].Status != "not-found" {
		t.Fatalf("claude probe = %+v", byID["claude"])
	}
	if byID["opencode"].Status != "uncertain" || byID["opencode"].Reason == "" {
		t.Fatalf("opencode probe = %+v", byID["opencode"])
	}
}
