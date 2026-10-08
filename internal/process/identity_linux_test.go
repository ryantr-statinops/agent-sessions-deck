//go:build linux

package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestParseProcStatUsesLastCommandDelimiterAndFields(t *testing.T) {
	got, err := parseProcStat(procStatLine(4242, "worker (pty) alpha", 515, 987654))
	if err != nil {
		t.Fatalf("parseProcStat() error = %v", err)
	}
	want := procStat{PID: 4242, PGID: 515, StartTicks: 987654}
	if got != want {
		t.Fatalf("parseProcStat() = %+v, want %+v", got, want)
	}
}

func TestParseProcStatRejectsIncompleteIdentityFields(t *testing.T) {
	tests := map[string][]byte{
		"missing delimiters": []byte("42 S 1 2"),
		"truncated fields":   []byte("42 (worker) S 1 2"),
		"invalid pid":        []byte("x (worker) S 1 2 2 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1"),
		"invalid pgid":       procStatLine(42, "worker", 0, 9),
		"invalid start":      procStatLine(42, "worker", 7, 0),
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseProcStat(input); err == nil {
				t.Fatal("parseProcStat() succeeded for malformed input")
			}
		})
	}
}

func TestCaptureAndObserveProcIdentity(t *testing.T) {
	root := t.TempDir()
	writeFakeProc(t, root, 4242, procStatLine(4242, "worker", 4242, 17), "boot-a")

	got, err := capture(root, 4242, "owner-a")
	if err != nil {
		t.Fatalf("capture() error = %v", err)
	}
	want := session.ProcessIdentity{PID: 4242, PGID: 4242, BootID: "boot-a", StartTicks: 17, OwnerInstanceID: "owner-a"}
	if got != want {
		t.Fatalf("capture() = %+v, want %+v", got, want)
	}

	observation := observe(root, got)
	if err := observation.Validate(); err != nil {
		t.Fatalf("observation is invalid: %v", err)
	}
	if classification := got.Classify(observation); classification != session.ClassificationVerified {
		t.Fatalf("classification = %q, want verified", classification)
	}
	if observation.At.Location().String() != "UTC" {
		t.Fatalf("observation timestamp location = %v, want UTC", observation.At.Location())
	}
}

func TestObserveFailsClosedOnIdentityDriftAndIncompleteProcfs(t *testing.T) {
	root := t.TempDir()
	recorded := session.ProcessIdentity{PID: 4242, PGID: 4242, BootID: "boot-a", StartTicks: 17, OwnerInstanceID: "owner-a"}
	writeFakeProc(t, root, 4242, procStatLine(4242, "worker", 4242, 18), "boot-a")
	if got := recorded.Classify(observe(root, recorded)); got != session.ClassificationStale {
		t.Fatalf("start-time drift classification = %q, want stale", got)
	}
	writeFakeProcStat(t, root, 4242, procStatLine(9001, "worker", 4242, 17))
	if got := observe(root, recorded); got.Outcome != session.ProbeUnverifiable || got.Detail != "invalid-proc-stat" {
		t.Fatalf("mismatched proc pid observation = %+v, want unverifiable/invalid-proc-stat", got)
	}
	if err := os.Remove(filepath.Join(root, "4242", "stat")); err != nil {
		t.Fatal(err)
	}
	if got := observe(root, recorded); got.Outcome != session.ProbeGone {
		t.Fatalf("missing proc entry outcome = %q, want gone", got.Outcome)
	}

	writeFakeProcStat(t, root, 4242, []byte("malformed"))
	if got := observe(root, recorded); got.Outcome != session.ProbeUnverifiable || got.Detail != "invalid-proc-stat" {
		t.Fatalf("malformed proc entry observation = %+v, want unverifiable/invalid-proc-stat", got)
	}

	if err := os.Remove(filepath.Join(root, "sys", "kernel", "random", "boot_id")); err != nil {
		t.Fatal(err)
	}
	writeFakeProcStat(t, root, 4242, procStatLine(4242, "worker", 4242, 17))
	if got := observe(root, recorded); got.Outcome != session.ProbeUnverifiable || got.Detail != "boot-id-unavailable" {
		t.Fatalf("missing boot id observation = %+v, want unverifiable/boot-id-unavailable", got)
	}
}

func TestCaptureRejectsMissingOwnerAndInvalidPID(t *testing.T) {
	for _, tc := range []struct {
		name  string
		pid   int
		owner string
	}{
		{name: "zero pid", pid: 0, owner: "owner-a"},
		{name: "blank owner", pid: 4242, owner: "  "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := capture(t.TempDir(), tc.pid, tc.owner); err == nil {
				t.Fatal("capture() succeeded for invalid input")
			}
		})
	}
}

func TestObserveInvalidRecordedIdentityIsUnverifiable(t *testing.T) {
	got := observe(t.TempDir(), session.ProcessIdentity{PID: 4242})
	if err := got.Validate(); err != nil {
		t.Fatalf("observation is invalid: %v", err)
	}
	if got.Outcome != session.ProbeUnverifiable || got.Detail != "invalid-recorded-identity" {
		t.Fatalf("observation = %+v, want unverifiable invalid-recorded-identity", got)
	}
}

func TestCaptureCurrentProcess(t *testing.T) {
	got, err := Capture(os.Getpid(), "test-owner")
	if err != nil {
		t.Fatalf("Capture(current pid) error = %v", err)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("captured identity is invalid: %v", err)
	}
	if observation := Observe(got); got.Classify(observation) != session.ClassificationVerified {
		t.Fatalf("current process classification = %q, observation = %+v", got.Classify(observation), observation)
	}
}

func procStatLine(pid int, command string, pgid int, startTicks uint64) []byte {
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "S"
	fields[1] = "1"
	fields[2] = fmt.Sprint(pgid)
	fields[19] = fmt.Sprint(startTicks)
	return []byte(fmt.Sprintf("%d (%s) %s\n", pid, command, strings.Join(fields, " ")))
}

func writeFakeProc(t *testing.T, root string, pid int, stat []byte, bootID string) {
	t.Helper()
	writeFakeProcStat(t, root, pid, stat)
	bootPath := filepath.Join(root, "sys", "kernel", "random", "boot_id")
	if err := os.MkdirAll(filepath.Dir(bootPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bootPath, []byte(bootID+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeFakeProcStat(t *testing.T, root string, pid int, stat []byte) {
	t.Helper()
	path := filepath.Join(root, fmt.Sprint(pid), "stat")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, stat, 0o644); err != nil {
		t.Fatal(err)
	}
}
