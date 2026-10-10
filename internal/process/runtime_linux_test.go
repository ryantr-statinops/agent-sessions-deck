//go:build linux

package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	domain "github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/terminal"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestRuntimeLaunchUsesWorkspaceAndReapsExactlyOnce(t *testing.T) {
	cwd := t.TempDir()
	marker := filepath.Join(cwd, "cwd.txt")
	runtime, err := NewRuntime("owner-test", terminal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	command, err := agent.NewCommand("/bin/sh", []string{"-c", "pwd > \"$1\"; exit 7", "runtime-test", marker})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(cwd)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := runtime.Launch(context.Background(), domain.LaunchRequest{
		SessionID: "runtime-test", Generation: 1, Command: command, Workspace: ws,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if !identity.Valid() || identity.PID != identity.PGID {
		t.Fatalf("launched process identity is invalid: %+v", identity)
	}
	status, err := runtime.WaitExit(context.Background(), domain.ExitWait{
		SessionID: "runtime-test", Generation: 1, Identity: identity, Waiter: "owner-test",
	})
	if err != nil {
		t.Fatalf("wait for child: %v", err)
	}
	if !status.Valid() || status.ExitCode == nil || *status.ExitCode != 7 {
		t.Fatalf("exit status = %+v, want exit code 7 and terminal evidence", status)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != cwd+"\n" {
		t.Fatalf("child cwd marker = %q, err=%v; want %q", got, err, cwd+"\n")
	}
	if _, err := runtime.WaitExit(context.Background(), domain.ExitWait{
		SessionID: "runtime-test", Generation: 1, Identity: identity, Waiter: "owner-test",
	}); !errors.Is(err, domain.ErrExitAlreadyReaped) {
		t.Fatalf("second waiter error = %v, want ErrExitAlreadyReaped", err)
	}
}

func TestRuntimeStopAndForceKillConfirmOwnedProcessExit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		signal domain.SignalKind
	}{
		{name: "graceful", args: []string{"-c", "trap 'exit 0' TERM; while :; do sleep .02; done"}, signal: domain.SignalTerm},
		{name: "force", args: []string{"-c", "exec sleep 30"}, signal: domain.SignalKill},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, err := NewRuntime("owner-"+tc.name, terminal.Options{})
			if err != nil {
				t.Fatal(err)
			}
			ws, err := workspace.New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			command, err := agent.NewCommand("/bin/sh", tc.args)
			if err != nil {
				t.Fatal(err)
			}
			identity, err := runtime.Launch(context.Background(), domain.LaunchRequest{
				SessionID: domain.ID("runtime-" + tc.name), Generation: 1, Command: command, Workspace: ws,
			})
			if err != nil {
				t.Fatalf("launch: %v", err)
			}
			deadline := time.Now().Add(time.Second)
			for {
				observation, err := runtime.Observe(context.Background(), identity)
				if err != nil {
					t.Fatalf("observe: %v", err)
				}
				if observation.Outcome == domain.ProbeAlive {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("child did not become observable: %+v", observation)
				}
				time.Sleep(time.Millisecond)
			}
			if tc.signal == domain.SignalTerm {
				outcome, err := runtime.Stop(context.Background(), identity, 2*time.Second)
				if err != nil || !outcome.Exited || (outcome.ExitCode == nil && outcome.Signal == "" && outcome.Evidence == "") {
					t.Fatalf("stop outcome = %+v, err=%v; want confirmed reap evidence", outcome, err)
				}
			} else {
				outcome, err := runtime.ForceKill(context.Background(), identity, 2*time.Second)
				if err != nil || !outcome.Delivered || outcome.Timeout || outcome.Terminated.Signal != "SIGKILL" {
					t.Fatalf("kill outcome = %+v, err=%v; want confirmed SIGKILL reap", outcome, err)
				}
			}
		})
	}
}

func TestRuntimeSignalsOnlyItsVerifiedOwnedGroup(t *testing.T) {
	runtime, err := NewRuntime("owner-boundary", terminal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command, err := agent.NewCommand("/bin/sh", []string{"-c", "exec sleep 30"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := runtime.Launch(context.Background(), domain.LaunchRequest{
		SessionID: "owned-signal", Generation: 1, Command: command, Workspace: ws,
	})
	if err != nil {
		t.Fatalf("launch owned child: %v", err)
	}
	sentinel := exec.Command("/bin/sleep", "30")
	if err := sentinel.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sentinel.Process.Kill()
		_, _ = sentinel.Process.Wait()
	})
	wrongOwner := identity
	wrongOwner.OwnerInstanceID = "another-owner"
	if err := runtime.Signal(context.Background(), wrongOwner, domain.SignalTerm); err == nil {
		t.Fatal("signal with a mismatched owner identity was accepted")
	}
	outcome, err := runtime.ForceKill(context.Background(), identity, 2*time.Second)
	if err != nil || !outcome.Delivered || outcome.Timeout || outcome.Terminated.Signal != "SIGKILL" {
		t.Fatalf("owned child kill = %+v, err=%v", outcome, err)
	}
	if err := sentinel.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("external sentinel was affected by owned-group kill: %v", err)
	}
}

func TestRuntimeStopTimeoutRequiresExplicitForceKill(t *testing.T) {
	runtime, err := NewRuntime("owner-timeout", terminal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command, err := agent.NewCommand("/bin/sh", []string{"-c", "trap '' TERM; exec sleep 30"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := runtime.Launch(context.Background(), domain.LaunchRequest{
		SessionID: "stop-timeout", Generation: 1, Command: command, Workspace: ws,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	outcome, err := runtime.Stop(context.Background(), identity, 20*time.Millisecond)
	if err != nil || outcome.Exited || outcome.Observation.Outcome != domain.ProbeAlive {
		t.Fatalf("graceful stop = %+v, err=%v; want live timeout without escalation", outcome, err)
	}
	kill, err := runtime.ForceKill(context.Background(), identity, 2*time.Second)
	if err != nil || !kill.Delivered || kill.Timeout || kill.Terminated.Signal != "SIGKILL" {
		t.Fatalf("explicit force kill = %+v, err=%v; want confirmed kill", kill, err)
	}
}

func TestRuntimeOwnsTerminalDrainAcrossDetachAndReattach(t *testing.T) {
	runtime, err := NewRuntime("owner-terminal", terminal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command, err := agent.NewCommand("/bin/sh", []string{"-c", "printf 'RUNTIME_SCREEN_MARKER\n'; exec sleep 30"})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := runtime.Launch(context.Background(), domain.LaunchRequest{
		SessionID: "terminal-runtime", Generation: 1, Command: command, Workspace: ws,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	lease := app.InteractiveLease{SessionID: "terminal-runtime", Generation: 1, Holder: "test-client", AcquiredAt: time.Now().UTC()}
	first, err := runtime.Terminals().Subscribe(context.Background(), lease)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	var screen strings.Builder
	for _, line := range first.InitialSnapshot().Screen.Lines {
		for _, cell := range line.Cells {
			screen.WriteString(cell.Text)
		}
	}
	if !strings.Contains(screen.String(), "RUNTIME_SCREEN_MARKER") {
		t.Fatalf("runtime-owned screen lacks child output: %q", screen.String())
	}
	sequence := first.InitialSnapshot().Sequence
	if err := first.Close(); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if err := syscall.Kill(identity.PID, 0); err != nil {
		t.Fatalf("detach killed child process: %v", err)
	}
	second, err := runtime.Terminals().Subscribe(context.Background(), lease)
	if err != nil {
		t.Fatalf("reattach: %v", err)
	}
	if second.InitialSnapshot().Sequence != sequence {
		t.Fatalf("reattach snapshot sequence = %d, want retained sequence %d", second.InitialSnapshot().Sequence, sequence)
	}
	_ = second.Close()
	if _, err := runtime.ForceKill(context.Background(), identity, 2*time.Second); err != nil {
		t.Fatalf("cleanup owned child: %v", err)
	}
}

func TestRuntimeRestartUsesFreshTerminalGeneration(t *testing.T) {
	runtime, err := NewRuntime("owner-restart", terminal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for generation, marker := range []string{"FIRST_ATTEMPT", "SECOND_ATTEMPT"} {
		generation++
		command, err := agent.NewCommand("/bin/sh", []string{"-c", "printf '%s\n' \"$0\"; sleep 0.05", marker})
		if err != nil {
			t.Fatal(err)
		}
		identity, err := runtime.Launch(context.Background(), domain.LaunchRequest{
			SessionID: "restart-session", Generation: domain.Generation(generation), Command: command, Workspace: ws,
		})
		if err != nil {
			t.Fatalf("launch generation %d: %v", generation, err)
		}
		status, err := runtime.WaitExit(context.Background(), domain.ExitWait{
			SessionID: "restart-session", Generation: domain.Generation(generation), Identity: identity, Waiter: "owner-restart",
		})
		if err != nil || !status.Valid() {
			t.Fatalf("wait generation %d: status=%+v err=%v", generation, status, err)
		}
	}
	lease := app.InteractiveLease{SessionID: "restart-session", Generation: 2, Holder: "restart-client", AcquiredAt: time.Now().UTC()}
	subscription, err := runtime.Terminals().Subscribe(context.Background(), lease)
	if err != nil {
		t.Fatalf("attach to restarted attempt: %v", err)
	}
	defer subscription.Close()
	var screen strings.Builder
	for _, line := range subscription.InitialSnapshot().Screen.Lines {
		for _, cell := range line.Cells {
			screen.WriteString(cell.Text)
		}
	}
	if !strings.Contains(screen.String(), "SECOND_ATTEMPT") || strings.Contains(screen.String(), "FIRST_ATTEMPT") {
		t.Fatalf("restart screen contains stale or missing attempt output: %q", screen.String())
	}
}

func TestRuntimeRefusesAttemptOverlap(t *testing.T) {
	runtime, err := NewRuntime("owner-overlap", terminal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	command, err := agent.NewCommand("/bin/sh", []string{"-c", "exec sleep 30"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := runtime.Launch(context.Background(), domain.LaunchRequest{SessionID: "overlap", Generation: 1, Command: command, Workspace: ws})
	if err != nil {
		t.Fatalf("launch first attempt: %v", err)
	}
	t.Cleanup(func() { _, _ = runtime.ForceKill(context.Background(), first, time.Second) })
	if _, err := runtime.Launch(context.Background(), domain.LaunchRequest{SessionID: "overlap", Generation: 2, Command: command, Workspace: ws}); err == nil {
		t.Fatal("launch overlapping generation while first child runs")
	}
	kill, err := runtime.ForceKill(context.Background(), first, 2*time.Second)
	if err != nil || kill.Timeout || kill.Terminated.Signal != "SIGKILL" {
		t.Fatalf("cleanup first attempt: outcome=%+v err=%v", kill, err)
	}
}
