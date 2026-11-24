//go:build linux

package metrics

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func createProcStatmFixture(t *testing.T, procRoot string, pid int, residentPages uint64) {
	t.Helper()
	pidDir := filepath.Join(procRoot, fmt.Sprintf("%d", pid))
	_ = os.MkdirAll(pidDir, 0o755)
	raw := fmt.Sprintf("%d %d 500 200 0 1000 0\n", residentPages*2, residentPages)
	if err := os.WriteFile(filepath.Join(pidDir, "statm"), []byte(raw), 0o644); err != nil {
		t.Fatalf("write statm %d: %v", pid, err)
	}
}

func TestSampleProcessTree(t *testing.T) {
	tmp := t.TempDir()

	// Root: PID 1000, startTicks 500, resident 1000 pages (4MB)
	createProcStatFixture(t, tmp, 1000, 1, "agent", 500)
	createProcStatmFixture(t, tmp, 1000, 1000)

	// Child 1: PID 1001, PPID 1000, startTicks 510, resident 500 pages (2MB)
	createProcStatFixture(t, tmp, 1001, 1000, "bash", 510)
	createProcStatmFixture(t, tmp, 1001, 500)

	// Grandchild 1: PID 1002, PPID 1001, startTicks 520, resident 250 pages (1MB)
	createProcStatFixture(t, tmp, 1002, 1001, "python", 520)
	createProcStatmFixture(t, tmp, 1002, 250)

	identity := session.ProcessIdentity{
		PID:        1000,
		PGID:       1000,
		StartTicks: 500,
	}

	now := time.Now().UTC()
	treeMetric, _, err := SampleProcessTree(tmp, identity, now.Add(-5*time.Minute), nil, now)
	if err != nil {
		t.Fatalf("SampleProcessTree failed: %v", err)
	}

	if treeMetric.PID != 1000 {
		t.Errorf("expected PID 1000, got %d", treeMetric.PID)
	}
	if treeMetric.ProcessCount != 3 {
		t.Errorf("expected ProcessCount 3 (root + 2 children), got %d", treeMetric.ProcessCount)
	}

	expectedRootBytes := uint64(1000 * 4096)
	if treeMetric.MemoryRSSBytes != expectedRootBytes {
		t.Errorf("expected root RSS %d, got %d", expectedRootBytes, treeMetric.MemoryRSSBytes)
	}

	expectedTreeBytes := uint64((1000 + 500 + 250) * 4096)
	if treeMetric.TreeRSSBytes != expectedTreeBytes {
		t.Errorf("expected tree RSS %d, got %d", expectedTreeBytes, treeMetric.TreeRSSBytes)
	}
}
