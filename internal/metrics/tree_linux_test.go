//go:build linux

package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func createProcStatFixture(t *testing.T, procRoot string, pid, ppid int, comm string, startTicks uint64) {
	t.Helper()
	pidDir := filepath.Join(procRoot, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", pidDir, err)
	}

	raw := fmt.Sprintf("%d (%s) S %d %d 0 0 0 0 0 0 0 0 10 5 0 0 20 0 1 0 %d 0 0 0",
		pid, comm, ppid, pid, startTicks)
	if err := os.WriteFile(filepath.Join(pidDir, "stat"), []byte(raw), 0o644); err != nil {
		t.Fatalf("write stat %d: %v", pid, err)
	}
}

func TestDiscoverDescendants(t *testing.T) {
	tmp := t.TempDir()

	// Root process: PID 1000, StartTicks 500
	createProcStatFixture(t, tmp, 1000, 1, "agent-root", 500)

	// Child 1: PID 1001, PPID 1000, StartTicks 510
	createProcStatFixture(t, tmp, 1001, 1000, "bash", 510)

	// Grandchild 1: PID 1002, PPID 1001, StartTicks 520
	createProcStatFixture(t, tmp, 1002, 1001, "python", 520)

	// Unrelated process: PID 2000, PPID 1, StartTicks 530
	createProcStatFixture(t, tmp, 2000, 1, "unrelated", 530)

	// Recycled PID: PID 1003, PPID 1000, but StartTicks 400 (< 500, started before root)
	createProcStatFixture(t, tmp, 1003, 1000, "stale-process", 400)

	descendants, err := DiscoverDescendants(tmp, 1000, 500)
	if err != nil {
		t.Fatalf("DiscoverDescendants failed: %v", err)
	}

	if len(descendants) != 2 {
		t.Fatalf("expected 2 descendants, got %d", len(descendants))
	}

	foundPIDs := make(map[int]bool)
	for _, d := range descendants {
		foundPIDs[d.PID] = true
	}

	if !foundPIDs[1001] {
		t.Errorf("expected PID 1001 to be discovered")
	}
	if !foundPIDs[1002] {
		t.Errorf("expected PID 1002 to be discovered")
	}
	if foundPIDs[2000] {
		t.Errorf("unrelated PID 2000 should not be discovered")
	}
	if foundPIDs[1003] {
		t.Errorf("stale PID 1003 with earlier startTicks should be rejected")
	}
}
