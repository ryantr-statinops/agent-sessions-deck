//go:build linux

package metrics

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ProcStatm contains memory allocation counters parsed from /proc/[pid]/statm.
type ProcStatm struct {
	SizePages     uint64
	ResidentPages uint64
	SharedPages   uint64
	TextPages     uint64
	DataPages     uint64
	RSSBytes      uint64
}

// ReadProcStatm reads and parses /proc/[pid]/statm using the system page size.
func ReadProcStatm(procRoot string, pid int) (ProcStatm, error) {
	data, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "statm"))
	if err != nil {
		return ProcStatm{}, err
	}
	return ParseProcStatm(data, os.Getpagesize())
}

// ParseProcStatm parses the raw contents of /proc/[pid]/statm.
func ParseProcStatm(data []byte, pageSize int) (ProcStatm, error) {
	if pageSize <= 0 {
		pageSize = 4096
	}

	fields := strings.Fields(string(data))
	if len(fields) < 6 {
		return ProcStatm{}, errors.New("malformed proc statm: truncated fields")
	}

	size, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return ProcStatm{}, fmt.Errorf("malformed proc statm size: %w", err)
	}
	resident, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return ProcStatm{}, fmt.Errorf("malformed proc statm resident: %w", err)
	}
	shared, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return ProcStatm{}, fmt.Errorf("malformed proc statm shared: %w", err)
	}
	text, _ := strconv.ParseUint(fields[3], 10, 64)
	dataPages, _ := strconv.ParseUint(fields[5], 10, 64)

	return ProcStatm{
		SizePages:     size,
		ResidentPages: resident,
		SharedPages:   shared,
		TextPages:     text,
		DataPages:     dataPages,
		RSSBytes:      resident * uint64(pageSize),
	}, nil
}
