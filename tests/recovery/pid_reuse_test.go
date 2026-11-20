//go:build linux

package recovery

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/process"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/terminal"
)

func TestPIDReuseSentinelSafety(t *testing.T) {
	// 1. Spawn an external sentinel process outside of ASD
	sentinel := exec.Command("/bin/sleep", "60")
	if err := sentinel.Start(); err != nil {
		t.Fatalf("failed to spawn external sentinel process: %v", err)
	}
	defer func() {
		_ = sentinel.Process.Kill()
		_, _ = sentinel.Process.Wait()
	}()

	sentinelPID := sentinel.Process.Pid

	// 2. Build a process runtime
	rt, err := process.NewRuntime("owner-sentinel-test", terminal.Options{})
	if err != nil {
		t.Fatalf("failed to create process runtime: %v", err)
	}

	// 3. Construct a fake ProcessIdentity targeting the sentinel's PID but with stale ticks / boot / owner
	staleIdentity := session.ProcessIdentity{
		PID:             sentinelPID,
		PGID:            sentinelPID,
		BootID:          "boot-mismatch-123",
		StartTicks:      999999999, // Stale ticks that cannot match the real sentinel
		OwnerInstanceID: "dead-owner-inst",
	}

	// 4. Test Observe: Must detect mismatch and fail closed (not report as verified alive)
	obs, err := rt.Observe(context.Background(), staleIdentity)
	if err != nil {
		t.Fatalf("Observe returned unexpected error: %v", err)
	}
	if obs.Outcome == session.ProbeAlive && obs.Identity.Equal(staleIdentity) {
		t.Fatalf("Observe falsely verified foreign sentinel identity: %+v", obs)
	}

	// 5. Test Signal: Must refuse to signal foreign PID
	signalErr := rt.Signal(context.Background(), staleIdentity, session.SignalTerm)
	if signalErr == nil {
		t.Fatal("Signal on foreign sentinel succeeded, want refusal")
	}

	// 6. Test ForceKill: Must refuse to kill foreign PID
	_, killErr := rt.ForceKill(context.Background(), staleIdentity, 500*time.Millisecond)
	if killErr == nil {
		t.Fatal("ForceKill on foreign sentinel succeeded, want refusal")
	}

	// 7. Test with matching BootID but mismatched StartTicks
	rawBootID, _ := os.ReadFile("/proc/sys/kernel/random/boot_id")
	realBootID := strings.TrimSpace(string(rawBootID))
	if realBootID != "" {
		staleTicksIdentity := session.ProcessIdentity{
			PID:             sentinelPID,
			PGID:            sentinelPID,
			BootID:          realBootID,
			StartTicks:      1, // Early boot tick, does not match sentinel start time
			OwnerInstanceID: "owner-sentinel-test",
		}

		obs2, _ := rt.Observe(context.Background(), staleTicksIdentity)
		if obs2.Outcome == session.ProbeAlive && obs2.Identity.Equal(staleTicksIdentity) {
			t.Fatalf("Observe falsely verified identity with mismatched StartTicks: %+v", obs2)
		}

		if err := rt.Signal(context.Background(), staleTicksIdentity, session.SignalKill); err == nil {
			t.Fatal("Signal on mismatched StartTicks succeeded, want refusal")
		}
	}

	// 8. Prove sentinel was completely unaffected by all ASD calls
	if err := sentinel.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("sentinel process %d was killed or disturbed: %v", sentinelPID, err)
	}
}
