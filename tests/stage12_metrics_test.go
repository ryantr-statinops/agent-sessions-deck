//go:build linux

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func createFakeProcFS(t *testing.T, pid int, comm string, state string, ppid int, utime, stime, starttime uint64, rssPages uint64) string {
	t.Helper()
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// 52 fields in Linux /proc/[pid]/stat
	// field 1: pid, field 2: comm, field 3: state, field 4: ppid
	// field 14: utime, field 15: stime, field 22: starttime
	fields := make([]string, 52)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = fmt.Sprintf("%d", pid)
	fields[1] = fmt.Sprintf("(%s)", comm)
	fields[2] = state
	fields[3] = fmt.Sprintf("%d", ppid)
	fields[13] = fmt.Sprintf("%d", utime)
	fields[14] = fmt.Sprintf("%d", stime)
	fields[21] = fmt.Sprintf("%d", starttime)

	statContent := fmt.Sprintf("%s\n", joinWithSpaces(fields))
	if err := os.WriteFile(filepath.Join(pidDir, "stat"), []byte(statContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// /proc/[pid]/statm
	statmContent := fmt.Sprintf("%d %d 0 0 0 0 0\n", rssPages*2, rssPages)
	if err := os.WriteFile(filepath.Join(pidDir, "statm"), []byte(statmContent), 0o644); err != nil {
		t.Fatal(err)
	}

	return procRoot
}

func joinWithSpaces(items []string) string {
	result := ""
	for i, item := range items {
		if i > 0 {
			result += " "
		}
		result += item
	}
	return result
}

func TestStage12_PIDReuseDefense(t *testing.T) {
	pid := 4129
	originalStartTicks := uint64(10000)
	recycledStartTicks := uint64(25000)

	// Kernel PID was reused for a new, different process
	procRoot := createFakeProcFS(t, pid, "foreign-worker", "R", 1, 10, 10, recycledStartTicks, 200)

	identity := session.ProcessIdentity{
		PID:        pid,
		StartTicks: originalStartTicks,
	}

	now := time.Now().UTC()
	m, _, err := metrics.SampleProcess(procRoot, identity, now.Add(-10*time.Second), nil, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !m.Unavailable {
		t.Fatalf("expected Unavailable=true when PID is reused by kernel")
	}
	if m.Error != metrics.ErrPIDReused {
		t.Fatalf("expected Error %q, got %q", metrics.ErrPIDReused, m.Error)
	}
}

func TestStage12_ProcessExitedResilience(t *testing.T) {
	procRoot := t.TempDir() // empty procfs, PID does not exist

	identity := session.ProcessIdentity{
		PID:        9999,
		StartTicks: 5000,
	}

	now := time.Now().UTC()
	m, _, err := metrics.SampleProcess(procRoot, identity, now.Add(-5*time.Second), nil, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !m.Unavailable {
		t.Fatalf("expected Unavailable=true for missing PID")
	}
	if m.Error != metrics.ErrProcessExited {
		t.Fatalf("expected Error %q, got %q", metrics.ErrProcessExited, m.Error)
	}
}

func TestStage12_DescendantAttributionBoundaries(t *testing.T) {
	rootPID := 5000
	rootTicks := uint64(1000)

	procRoot := t.TempDir()

	// 1. Root process (pid 5000, ppid 1, starttime 1000)
	createProcInRoot(t, procRoot, rootPID, "root-agent", "S", 1, 5, 5, rootTicks, 100)

	// 2. Legitimate child (pid 5001, ppid 5000, starttime 1050)
	createProcInRoot(t, procRoot, 5001, "child-compiler", "R", rootPID, 20, 10, 1050, 200)

	// 3. Legitimate grandchild (pid 5002, ppid 5001, starttime 1100)
	createProcInRoot(t, procRoot, 5002, "grandchild-link", "S", 5001, 5, 5, 1100, 300)

	// 4. Foreign process with same name but started before root (pid 5003, ppid 5000, starttime 900)
	createProcInRoot(t, procRoot, 5003, "imposter", "S", rootPID, 1, 1, 900, 50)

	// 5. Unrelated process (pid 6000, ppid 1, starttime 1200)
	createProcInRoot(t, procRoot, 6000, "daemon", "S", 1, 10, 10, 1200, 500)

	identity := session.ProcessIdentity{
		PID:        rootPID,
		StartTicks: rootTicks,
	}

	now := time.Now().UTC()
	treeMetrics, _, err := metrics.SampleProcessTree(procRoot, identity, now.Add(-10*time.Second), nil, now)
	if err != nil {
		t.Fatalf("SampleProcessTree: %v", err)
	}

	if treeMetrics.Unavailable {
		t.Fatalf("expected tree metrics available, got error: %s", treeMetrics.Error)
	}

	// Total process count should be 3 (root + child 5001 + grandchild 5002)
	// imposter 5003 is excluded because starttime < rootTicks
	// unrelated 6000 is excluded because PPID is not in tree
	if treeMetrics.ProcessCount != 3 {
		t.Fatalf("expected process count 3, got %d", treeMetrics.ProcessCount)
	}

	pageSize := uint64(os.Getpagesize())
	expectedTreeRSS := (200 + 300) * pageSize
	if treeMetrics.TreeRSSBytes != expectedTreeRSS {
		t.Fatalf("expected TreeRSSBytes %d, got %d", expectedTreeRSS, treeMetrics.TreeRSSBytes)
	}
}

func createProcInRoot(t *testing.T, procRoot string, pid int, comm string, state string, ppid int, utime, stime, starttime uint64, rssPages uint64) {
	if t != nil {
		t.Helper()
	}
	pidDir := filepath.Join(procRoot, fmt.Sprintf("%d", pid))
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		if t != nil {
			t.Fatal(err)
		}
		panic(err)
	}

	fields := make([]string, 52)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = fmt.Sprintf("%d", pid)
	fields[1] = fmt.Sprintf("(%s)", comm)
	fields[2] = state
	fields[3] = fmt.Sprintf("%d", ppid)
	fields[13] = fmt.Sprintf("%d", utime)
	fields[14] = fmt.Sprintf("%d", stime)
	fields[21] = fmt.Sprintf("%d", starttime)

	statContent := fmt.Sprintf("%s\n", joinWithSpaces(fields))
	if err := os.WriteFile(filepath.Join(pidDir, "stat"), []byte(statContent), 0o644); err != nil {
		if t != nil {
			t.Fatal(err)
		}
		panic(err)
	}

	statmContent := fmt.Sprintf("%d %d 0 0 0 0 0\n", rssPages*2, rssPages)
	if err := os.WriteFile(filepath.Join(pidDir, "statm"), []byte(statmContent), 0o644); err != nil {
		if t != nil {
			t.Fatal(err)
		}
		panic(err)
	}
}
