//go:build linux

package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseProcStatm(t *testing.T) {
	// 10000 pages size, 2500 resident, 500 shared, 200 text, 0 lib, 1800 data, 0 dt
	raw := []byte("10000 2500 500 200 0 1800 0\n")

	statm, err := ParseProcStatm(raw, 4096)
	if err != nil {
		t.Fatalf("ParseProcStatm failed: %v", err)
	}

	if statm.SizePages != 10000 {
		t.Errorf("expected SizePages 10000, got %d", statm.SizePages)
	}
	if statm.ResidentPages != 2500 {
		t.Errorf("expected ResidentPages 2500, got %d", statm.ResidentPages)
	}
	if statm.SharedPages != 500 {
		t.Errorf("expected SharedPages 500, got %d", statm.SharedPages)
	}
	if statm.RSSBytes != 2500*4096 {
		t.Errorf("expected RSSBytes %d, got %d", 2500*4096, statm.RSSBytes)
	}

	// ReadProcStatm from filesystem fixture
	tmp := t.TempDir()
	pidDir := filepath.Join(tmp, "4129")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "statm"), raw, 0o644); err != nil {
		t.Fatalf("write statm: %v", err)
	}

	readStatm, err := ReadProcStatm(tmp, 4129)
	if err != nil {
		t.Fatalf("ReadProcStatm failed: %v", err)
	}
	if readStatm.ResidentPages != 2500 {
		t.Errorf("ReadProcStatm mismatch: %+v", readStatm)
	}

	// Truncated statm
	if _, err := ParseProcStatm([]byte("100 200"), 4096); err == nil {
		t.Errorf("expected error on truncated statm, got nil")
	}
}
