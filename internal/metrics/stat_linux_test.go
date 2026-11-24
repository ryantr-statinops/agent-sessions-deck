//go:build linux

package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProcStat(t *testing.T) {
	// Standard Linux /proc/[pid]/stat line (pid 4129, comm "codex agent", state S, ppid 1000, pgrp 4129)
	raw := []byte("4129 (codex agent) S 1000 4129 4129 34816 4129 4194304 123 0 0 0 150 75 10 5 20 0 4 0 987654 1234567 100 18446744073709551615")

	stat, err := ParseProcStat(raw)
	if err != nil {
		t.Fatalf("ParseProcStat failed: %v", err)
	}

	if stat.PID != 4129 {
		t.Errorf("expected PID 4129, got %d", stat.PID)
	}
	if stat.Comm != "codex agent" {
		t.Errorf("expected Comm 'codex agent', got %q", stat.Comm)
	}
	if stat.State != "S" {
		t.Errorf("expected State 'S', got %s", stat.State)
	}
	if stat.PPID != 1000 {
		t.Errorf("expected PPID 1000, got %d", stat.PPID)
	}
	if stat.PGID != 4129 {
		t.Errorf("expected PGID 4129, got %d", stat.PGID)
	}
	if stat.UTicks != 150 {
		t.Errorf("expected UTicks 150, got %d", stat.UTicks)
	}
	if stat.STicks != 75 {
		t.Errorf("expected STicks 75, got %d", stat.STicks)
	}
	if stat.TotalTicks() != 225 {
		t.Errorf("expected TotalTicks 225, got %d", stat.TotalTicks())
	}
	if stat.StartTicks != 987654 {
		t.Errorf("expected StartTicks 987654, got %d", stat.StartTicks)
	}

	// ReadProcStat from filesystem fixture
	tmp := t.TempDir()
	pidDir := filepath.Join(tmp, "4129")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "stat"), raw, 0o644); err != nil {
		t.Fatalf("write stat: %v", err)
	}

	readStat, err := ReadProcStat(tmp, 4129)
	if err != nil {
		t.Fatalf("ReadProcStat failed: %v", err)
	}
	if readStat.PID != 4129 || readStat.Comm != "codex agent" {
		t.Errorf("ReadProcStat mismatch: %+v", readStat)
	}

	// Malformed cases
	if _, err := ParseProcStat([]byte("invalid")); err == nil {
		t.Errorf("expected error on malformed stat, got nil")
	}
	if _, err := ParseProcStat([]byte("123 () S")); err == nil {
		t.Errorf("expected error on truncated fields, got nil")
	}
}
