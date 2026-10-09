//go:build linux

package process

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

const procRoot = "/proc"

var errMalformedProcStat = errors.New("malformed proc stat")

type procStat struct {
	PID        int
	State      byte
	PGID       int
	StartTicks uint64
}

// Capture reads the kernel identity for a process started by this owner.
func Capture(pid int, ownerInstanceID string) (session.ProcessIdentity, error) {
	return capture(procRoot, pid, ownerInstanceID)
}

func capture(root string, pid int, ownerInstanceID string) (session.ProcessIdentity, error) {
	if pid <= 0 {
		return session.ProcessIdentity{}, fmt.Errorf("capture process identity: pid %d is not positive", pid)
	}
	if strings.TrimSpace(ownerInstanceID) == "" {
		return session.ProcessIdentity{}, errors.New("capture process identity: owner instance id is empty")
	}
	id, err := readIdentity(root, pid, ownerInstanceID)
	if err != nil {
		return session.ProcessIdentity{}, fmt.Errorf("capture process identity for pid %d: %w", pid, err)
	}
	if err := id.Validate(); err != nil {
		return session.ProcessIdentity{}, fmt.Errorf("capture process identity for pid %d: %w", pid, err)
	}
	return id, nil
}

// Observe reads the current kernel identity for a recorded process. It never treats
// a failed or partial procfs read as proof that the process exited.
func Observe(recorded session.ProcessIdentity) session.LivenessObservation {
	return observe(procRoot, recorded)
}

func observe(root string, recorded session.ProcessIdentity) session.LivenessObservation {
	at := time.Now().UTC()
	if err := recorded.Validate(); err != nil {
		return session.LivenessObservation{Outcome: session.ProbeUnverifiable, Detail: "invalid-recorded-identity", At: at}
	}

	stat, err := readProcStat(root, recorded.PID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return session.LivenessObservation{Outcome: session.ProbeGone, At: at}
		}
		return session.LivenessObservation{Outcome: session.ProbeUnverifiable, Detail: probeDetail(err), At: at}
	}
	if stat.PID != recorded.PID {
		return session.LivenessObservation{Outcome: session.ProbeUnverifiable, Detail: "invalid-proc-stat", At: at}
	}
	bootID, err := readBootID(root)
	if err != nil {
		return session.LivenessObservation{Outcome: session.ProbeUnverifiable, Detail: "boot-id-unavailable", At: at}
	}
	return session.LivenessObservation{
		Outcome: session.ProbeAlive,
		Identity: session.ProcessIdentity{
			PID:             stat.PID,
			PGID:            stat.PGID,
			BootID:          bootID,
			StartTicks:      stat.StartTicks,
			OwnerInstanceID: recorded.OwnerInstanceID,
		},
		At: at,
	}
}

func readIdentity(root string, pid int, ownerInstanceID string) (session.ProcessIdentity, error) {
	stat, err := readProcStat(root, pid)
	if err != nil {
		return session.ProcessIdentity{}, err
	}
	bootID, err := readBootID(root)
	if err != nil {
		return session.ProcessIdentity{}, fmt.Errorf("read boot id: %w", err)
	}
	if stat.PID != pid {
		return session.ProcessIdentity{}, fmt.Errorf("%w: requested pid %d, proc stat reported %d", errMalformedProcStat, pid, stat.PID)
	}
	return session.ProcessIdentity{
		PID:             stat.PID,
		PGID:            stat.PGID,
		BootID:          bootID,
		StartTicks:      stat.StartTicks,
		OwnerInstanceID: ownerInstanceID,
	}, nil
}

func readProcStat(root string, pid int) (procStat, error) {
	path := filepath.Join(root, strconv.Itoa(pid), "stat")
	data, err := os.ReadFile(path)
	if err != nil {
		return procStat{}, err
	}
	stat, err := parseProcStat(data)
	if err != nil {
		return procStat{}, fmt.Errorf("%w: %v", errMalformedProcStat, err)
	}
	return stat, nil
}

func readBootID(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "sys", "kernel", "random", "boot_id"))
	if err != nil {
		return "", err
	}
	bootID := string(bytes.TrimSpace(data))
	if bootID == "" {
		return "", errors.New("boot id is empty")
	}
	return bootID, nil
}

func parseProcStat(data []byte) (procStat, error) {
	open := bytes.IndexByte(data, '(')
	close := bytes.LastIndexByte(data, ')')
	if open <= 0 || close <= open {
		return procStat{}, errors.New("missing command delimiters")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data[:open])))
	if err != nil || pid <= 0 {
		return procStat{}, errors.New("invalid pid")
	}
	fields := strings.Fields(string(data[close+1:]))
	const startTicksIndex = 19 // /proc/<pid>/stat field 22; fields start at field 3.
	if len(fields) <= startTicksIndex {
		return procStat{}, errors.New("truncated fields")
	}
	pgid, err := strconv.Atoi(fields[2])
	if err != nil || pgid < 0 {
		return procStat{}, errors.New("invalid process group id")
	}
	startTicks, err := strconv.ParseUint(fields[startTicksIndex], 10, 64)
	if err != nil || startTicks == 0 {
		return procStat{}, errors.New("invalid start ticks")
	}
	return procStat{PID: pid, State: fields[0][0], PGID: pgid, StartTicks: startTicks}, nil
}

func probeDetail(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "permission-denied"
	}
	if errors.Is(err, errMalformedProcStat) {
		return "invalid-proc-stat"
	}
	return "proc-read-error"
}
