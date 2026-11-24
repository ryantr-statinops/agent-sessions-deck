//go:build linux

package metrics

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcStat contains execution state and CPU ticks extracted from /proc/[pid]/stat.
type ProcStat struct {
	PID        int
	Comm       string
	State      string
	PPID       int
	PGID       int
	UTicks     uint64
	STicks     uint64
	CUTicks    uint64
	CSTicks    uint64
	StartTicks uint64
}

// TotalTicks returns the combined user and system CPU ticks for the process.
func (s ProcStat) TotalTicks() uint64 {
	return s.UTicks + s.STicks
}

// ReadProcStat reads and parses /proc/[pid]/stat from procRoot.
func ReadProcStat(procRoot string, pid int) (ProcStat, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return ProcStat{}, err
	}
	return ParseProcStat(data)
}

// ParseProcStat parses the contents of a /proc/[pid]/stat file.
func ParseProcStat(data []byte) (ProcStat, error) {
	open := bytes.IndexByte(data, '(')
	close := bytes.LastIndexByte(data, ')')
	if open <= 0 || close <= open {
		return ProcStat{}, errors.New("malformed proc stat: missing comm delimiters")
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data[:open])))
	if err != nil || pid <= 0 {
		return ProcStat{}, errors.New("malformed proc stat: invalid pid")
	}

	comm := string(data[open+1 : close])
	fields := strings.Fields(string(data[close+1:]))
	if len(fields) <= 19 {
		return ProcStat{}, errors.New("malformed proc stat: truncated fields")
	}

	state := fields[0]
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return ProcStat{}, fmt.Errorf("malformed proc stat: invalid ppid: %w", err)
	}
	pgid, err := strconv.Atoi(fields[2])
	if err != nil {
		return ProcStat{}, fmt.Errorf("malformed proc stat: invalid pgid: %w", err)
	}
	uticks, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return ProcStat{}, fmt.Errorf("malformed proc stat: invalid utime: %w", err)
	}
	sticks, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return ProcStat{}, fmt.Errorf("malformed proc stat: invalid stime: %w", err)
	}
	cuticks, _ := strconv.ParseUint(fields[13], 10, 64)
	csticks, _ := strconv.ParseUint(fields[14], 10, 64)
	startTicks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return ProcStat{}, fmt.Errorf("malformed proc stat: invalid starttime: %w", err)
	}

	return ProcStat{
		PID:        pid,
		Comm:       comm,
		State:      state,
		PPID:       ppid,
		PGID:       pgid,
		UTicks:     uticks,
		STicks:     sticks,
		CUTicks:    cuticks,
		CSTicks:    csticks,
		StartTicks: startTicks,
	}, nil
}
