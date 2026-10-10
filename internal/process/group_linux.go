//go:build linux

package process

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
)

// GroupMembers returns non-zombie process IDs currently observed in a process
// group. A scan error is returned with any members found; callers must not treat
// an incomplete scan as proof that the group is empty.
func GroupMembers(pgid int) ([]int, error) {
	return groupMembers(procRoot, pgid)
}

func groupMembers(root string, pgid int) ([]int, error) {
	if pgid <= 0 {
		return nil, fmt.Errorf("process group id %d is not positive", pgid)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read procfs: %w", err)
	}
	members := make([]int, 0)
	var scanErr error
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || !entry.IsDir() {
			continue
		}
		stat, err := readProcStat(root, pid)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			scanErr = errors.Join(scanErr, fmt.Errorf("read proc stat for pid %d: %w", pid, err))
			continue
		}
		if stat.PGID == pgid && stat.State != 'Z' && stat.State != 'X' {
			members = append(members, pid)
		}
	}
	sort.Ints(members)
	return members, scanErr
}
