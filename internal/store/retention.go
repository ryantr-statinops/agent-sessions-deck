package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
)

// PruneEvents retains at most maxEvents and drops events older than maxAge relative to now.
// It safely writes surviving events to a temporary file before atomically renaming it,
// ensuring the journal is never left in a corrupt state.
func (j *EventJournal) Prune(ctx context.Context, maxEvents int, maxAge time.Duration, now time.Time) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	if maxEvents <= 0 && maxAge <= 0 {
		return 0, nil
	}

	if j.lock != nil {
		j.mu.Lock()
		defer j.mu.Unlock()
		return j.pruneLocked(ctx, maxEvents, maxAge, now)
	}

	held, err := AcquireLock(j.lockPath)
	if err != nil {
		return 0, fmt.Errorf("acquire store lock for prune: %w", err)
	}
	defer held.Release()

	j.mu.Lock()
	defer j.mu.Unlock()
	return j.pruneLocked(ctx, maxEvents, maxAge, now)
}

func (j *EventJournal) pruneLocked(ctx context.Context, maxEvents int, maxAge time.Duration, now time.Time) (int, error) {
	f, err := os.Open(j.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("open journal file for pruning: %w", err)
	}
	defer f.Close()

	var cutoff time.Time
	if maxAge > 0 {
		cutoff = now.Add(-maxAge)
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var survivingLines [][]byte
	var totalRead int

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		totalRead++

		var ev events.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			// Preserve corrupted lines to avoid data loss
			lineCopy := make([]byte, len(line))
			copy(lineCopy, line)
			survivingLines = append(survivingLines, lineCopy)
			continue
		}

		if maxAge > 0 && !cutoff.IsZero() && ev.Timestamp().Time().Before(cutoff) {
			// Older than cutoff -> pruned
			continue
		}

		lineCopy := make([]byte, len(line))
		copy(lineCopy, line)
		survivingLines = append(survivingLines, lineCopy)
	}

	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan journal during pruning: %w", err)
	}

	if maxEvents > 0 && len(survivingLines) > maxEvents {
		survivingLines = survivingLines[len(survivingLines)-maxEvents:]
	}

	prunedCount := totalRead - len(survivingLines)
	if prunedCount <= 0 {
		return 0, nil
	}

	// Write surviving lines to temporary file
	tmpPath := j.path + ".tmp"
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return 0, fmt.Errorf("create temporary prune file: %w", err)
	}

	writer := bufio.NewWriter(tmpFile)
	for _, l := range survivingLines {
		if _, err := writer.Write(l); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return 0, fmt.Errorf("write to temp prune file: %w", err)
		}
		if err := writer.WriteByte('\n'); err != nil {
			tmpFile.Close()
			os.Remove(tmpPath)
			return 0, fmt.Errorf("write newline to temp prune file: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return 0, fmt.Errorf("flush temp prune file: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return 0, fmt.Errorf("sync temp prune file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("close temp prune file: %w", err)
	}

	// Atomic replace
	if err := os.Rename(tmpPath, j.path); err != nil {
		os.Remove(tmpPath)
		return 0, fmt.Errorf("atomic rename pruned journal: %w", err)
	}

	return prunedCount, nil
}
