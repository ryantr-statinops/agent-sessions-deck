//go:build linux

package metrics

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestSampleProcess(t *testing.T) {
	tmp := t.TempDir()
	pidDir := filepath.Join(tmp, "4129")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	rawStat1 := []byte("4129 (agent) S 1000 4129 4129 34816 4129 4194304 123 0 0 0 100 50 10 5 20 0 4 0 50000 1234567 100 18446744073709551615")
	rawStatm := []byte("10000 2000 500 200 0 1800 0\n")

	if err := os.WriteFile(filepath.Join(pidDir, "stat"), rawStat1, 0o644); err != nil {
		t.Fatalf("write stat: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "statm"), rawStatm, 0o644); err != nil {
		t.Fatalf("write statm: %v", err)
	}

	identity := session.ProcessIdentity{
		PID:        4129,
		PGID:       4129,
		StartTicks: 50000,
	}

	startedAt := time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)
	t1 := startedAt.Add(10 * time.Second)

	// 1. Initial sample
	m1, rec1, err := SampleProcess(tmp, identity, startedAt, nil, t1, WithClockTicks(100))
	if err != nil {
		t.Fatalf("initial sample failed: %v", err)
	}
	if m1.PID != 4129 || m1.State != "S" {
		t.Errorf("m1 mismatch: %+v", m1)
	}
	if m1.Uptime != 10*time.Second {
		t.Errorf("expected uptime 10s, got %v", m1.Uptime)
	}
	if m1.CPUPercent != 0.0 {
		t.Errorf("expected initial CPU 0.0, got %f", m1.CPUPercent)
	}
	if m1.MemoryRSSBytes != 2000*4096 {
		t.Errorf("expected memory %d, got %d", 2000*4096, m1.MemoryRSSBytes)
	}

	// 2. Second sample 2 seconds later with 50 tick delta (50 ticks / (100 Hz * 2s) = 25% CPU)
	t2 := t1.Add(2 * time.Second)
	rawStat2 := []byte("4129 (agent) S 1000 4129 4129 34816 4129 4194304 123 0 0 0 130 70 10 5 20 0 4 0 50000 1234567 100 18446744073709551615")
	if err := os.WriteFile(filepath.Join(pidDir, "stat"), rawStat2, 0o644); err != nil {
		t.Fatalf("write stat 2: %v", err)
	}

	m2, _, err := SampleProcess(tmp, identity, startedAt, &rec1, t2, WithClockTicks(100))
	if err != nil {
		t.Fatalf("second sample failed: %v", err)
	}
	// Total ticks went from 150 (100+50) to 200 (130+70). Delta = 50 ticks.
	// 50 / (100 * 2) = 0.25 -> 25.0%
	if m2.CPUPercent < 24.9 || m2.CPUPercent > 25.1 {
		t.Errorf("expected CPU ~25.0%%, got %f", m2.CPUPercent)
	}

	// 3. PID reuse defense test (stat startTicks is 99999, but identity is 50000)
	rawStatReused := []byte("4129 (another) S 1000 4129 4129 34816 4129 4194304 123 0 0 0 130 70 10 5 20 0 4 0 99999 1234567 100 18446744073709551615")
	if err := os.WriteFile(filepath.Join(pidDir, "stat"), rawStatReused, 0o644); err != nil {
		t.Fatalf("write reused stat: %v", err)
	}

	mReused, _, err := SampleProcess(tmp, identity, startedAt, nil, t2)
	if err != nil {
		t.Fatalf("expected nil err on reused pid, got %v", err)
	}
	if !mReused.Unavailable || mReused.Error != "pid-reused" {
		t.Errorf("expected pid-reused unavailable metric, got: %+v", mReused)
	}
}
