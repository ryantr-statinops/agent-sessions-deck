//go:build linux

package process

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	domain "github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/terminal"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestRuntimeReportsSameGroupResidualAndLeavesDetachedDescendant(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{name: "same-group", script: "sh -c 'trap \"\" TERM HUP; exec sleep 30' & echo $! > \"$1\"; exec sleep 30"},
		{name: "detached-session", script: "setsid /bin/sh -c 'echo $$ > \"$1\"; exec sleep 30' sh \"$1\" & exec sleep 30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, err := NewRuntime("owner-descendants-"+tc.name, terminal.Options{})
			if err != nil {
				t.Fatal(err)
			}
			ws, err := workspace.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			pidFile := filepath.Join(t.TempDir(), "descendant.pid")
			command, err := agent.NewCommand("/bin/sh", []string{"-c", tc.script, "runtime-test", pidFile})
			if err != nil {
				t.Fatal(err)
			}
			identity, err := runtime.Launch(context.Background(), domain.LaunchRequest{
				SessionID: domain.ID("descendant-" + tc.name), Generation: 1, Command: command, Workspace: ws,
			})
			if err != nil {
				t.Fatalf("launch: %v", err)
			}
			pid := waitDescendantPID(t, pidFile)
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
			outcome, err := runtime.Stop(context.Background(), identity, 2*time.Second)
			if err != nil || !outcome.Exited {
				t.Fatalf("stop outcome = %+v err=%v", outcome, err)
			}
			if tc.name == "same-group" {
				if !strings.Contains(outcome.Evidence, "process-group members remain after leader reap") || !strings.Contains(outcome.Evidence, strconv.Itoa(pid)) {
					t.Fatalf("same-group residual absent from evidence: %q", outcome.Evidence)
				}
			} else {
				members, err := GroupMembers(identity.PGID)
				if err != nil {
					t.Fatalf("scan owned group: %v", err)
				}
				for _, member := range members {
					if member == pid {
						t.Fatalf("setsid descendant %d appeared in owned group %v", pid, members)
					}
				}
				if err := syscall.Kill(pid, 0); err != nil {
					t.Fatalf("owned-group stop affected detached descendant: %v", err)
				}
			}
		})
	}
}

func waitDescendantPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path)
		if err == nil {
			pid, parseErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if parseErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for descendant pid file %s", path)
	return 0
}
