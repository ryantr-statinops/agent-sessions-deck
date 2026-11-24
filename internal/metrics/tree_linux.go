//go:build linux

package metrics

import (
	"os"
	"strconv"
)

// DiscoverDescendants finds all active descendant processes belonging to rootPID.
//
// It scans procRoot for numeric entries and iteratively identifies direct and
// indirect child processes. To defend against PID reuse, any candidate process
// whose StartTicks is strictly earlier than rootStartTicks is excluded.
func DiscoverDescendants(procRoot string, rootPID int, rootStartTicks uint64) ([]ProcStat, error) {
	if rootPID <= 0 {
		return nil, nil
	}

	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}

	// Read stats for all numeric PIDs in one pass
	allStats := make(map[int]ProcStat, len(entries))
	childrenByParent := make(map[int][]int, len(entries))

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || pid == rootPID {
			continue
		}

		stat, err := ReadProcStat(procRoot, pid)
		if err != nil {
			continue // Process may have exited or permission denied
		}

		allStats[pid] = stat
		childrenByParent[stat.PPID] = append(childrenByParent[stat.PPID], pid)
	}

	// Breadth-first traversal starting from rootPID
	var descendants []ProcStat
	queue := make([]int, 0, len(childrenByParent[rootPID]))
	queue = append(queue, childrenByParent[rootPID]...)

	visited := make(map[int]bool, len(allStats))

	for len(queue) > 0 {
		currentPID := queue[0]
		queue = queue[1:]

		if visited[currentPID] {
			continue
		}
		visited[currentPID] = true

		stat, ok := allStats[currentPID]
		if !ok {
			continue
		}

		// Invariant: descendant must have started at or after the root process
		if rootStartTicks > 0 && stat.StartTicks < rootStartTicks {
			continue
		}

		descendants = append(descendants, stat)

		// Queue transitive children
		if children, exists := childrenByParent[currentPID]; exists {
			for _, childPID := range children {
				if !visited[childPID] {
					queue = append(queue, childPID)
				}
			}
		}
	}

	return descendants, nil
}
