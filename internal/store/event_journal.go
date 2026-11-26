package store

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
)

// DefaultEventQueryLimit is the fallback result size limit for event queries.
const DefaultEventQueryLimit = 50

// MaxEventQueryLimit is the upper bound on events returned in one query.
const MaxEventQueryLimit = 1000

// ErrNonMonotonicRevision indicates an attempt to append an event whose revision
// does not advance strictly past the journal's last known revision.
var ErrNonMonotonicRevision = errors.New("event revision must be strictly greater than last journal revision")

// EventFilter specifies filtering and pagination parameters for journal queries.
type EventFilter struct {
	SessionID     events.SessionID
	Type          events.Type
	AfterRevision uint64
	Limit         int
}

// EventJournal provides durable, append-only persistence for event streams.
// Revisions are guaranteed to increase monotonically across writes and restarts.
type EventJournal struct {
	path     string
	lockPath string
	lock     *Lock

	mu           sync.RWMutex
	lastRevision uint64
}

// NewEventJournal opens an event journal at path using offline short-locking.
func NewEventJournal(path, lockPath string) (*EventJournal, error) {
	j := &EventJournal{
		path:     path,
		lockPath: lockPath,
	}
	if err := j.init(); err != nil {
		return nil, err
	}
	return j, nil
}

// NewOwnerEventJournal opens an event journal bound to an already-held process lifetime lock.
func NewOwnerEventJournal(path string, lock *Lock) (*EventJournal, error) {
	if lock == nil {
		return nil, errors.New("owner journal requires a non-nil lifetime lock")
	}
	j := &EventJournal{
		path: path,
		lock: lock,
	}
	if err := j.init(); err != nil {
		return nil, err
	}
	return j, nil
}

// init ensures the parent directory exists and discovers the last revision by scanning the file tail.
func (j *EventJournal) init() error {
	dir := filepath.Dir(j.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create journal directory: %w", err)
	}

	f, err := os.Open(j.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			j.lastRevision = 0
			return nil
		}
		return fmt.Errorf("open journal file: %w", err)
	}
	defer f.Close()

	lastRev, err := scanLastRevision(f)
	if err != nil {
		return fmt.Errorf("scan last revision from %s: %w", j.path, err)
	}
	j.lastRevision = lastRev
	return nil
}

// LastRevision returns the highest revision recorded in the journal.
func (j *EventJournal) LastRevision() uint64 {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.lastRevision
}

// Query retrieves events matching filter, ordered by monotonic revision.
func (j *EventJournal) Query(ctx context.Context, filter EventFilter) ([]events.Event, uint64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = DefaultEventQueryLimit
	} else if limit > MaxEventQueryLimit {
		limit = MaxEventQueryLimit
	}

	j.mu.RLock()
	defer j.mu.RUnlock()

	f, err := os.Open(j.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("open journal file for query: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var results []events.Event
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		default:
		}

		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var ev events.Event
		if err := json.Unmarshal(line, &ev); err != nil {
			// Skip corrupted or unparseable lines gracefully
			continue
		}

		rev := uint64(ev.Revision())
		if rev <= filter.AfterRevision {
			continue
		}
		if filter.SessionID != "" && ev.SessionID() != filter.SessionID {
			continue
		}
		if filter.Type != "" && ev.Type() != filter.Type {
			continue
		}

		results = append(results, ev)
		if len(results) >= limit {
			break
		}
	}

	if err := scanner.Err(); err != nil && len(results) == 0 {
		return nil, 0, fmt.Errorf("scan journal events: %w", err)
	}

	nextRevision := filter.AfterRevision
	if len(results) > 0 {
		nextRevision = uint64(results[len(results)-1].Revision())
	}

	return results, nextRevision, nil
}

// Append appends a single event to the journal, ensuring strict monotonic revision ordering.
func (j *EventJournal) Append(ctx context.Context, ev events.Event) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	data = append(data, '\n')

	evRev := uint64(ev.Revision())

	if j.lock != nil {
		// Owner mode: lifetime lock already held by daemon process.
		j.mu.Lock()
		defer j.mu.Unlock()
		return j.appendLocked(evRev, data)
	}

	// Offline mode: take short file lock for each operation.
	held, err := AcquireLock(j.lockPath)
	if err != nil {
		return fmt.Errorf("acquire store lock for append: %w", err)
	}
	defer held.Release()

	j.mu.Lock()
	defer j.mu.Unlock()
	return j.appendLocked(evRev, data)
}

func (j *EventJournal) appendLocked(evRev uint64, data []byte) error {
	if evRev <= j.lastRevision {
		return fmt.Errorf("%w: event rev %d <= last rev %d", ErrNonMonotonicRevision, evRev, j.lastRevision)
	}

	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open journal file for append: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write event to journal: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync journal file: %w", err)
	}

	j.lastRevision = evRev
	return nil
}

// scanLastRevision scans f to find the last recorded revision.
func scanLastRevision(f *os.File) (uint64, error) {
	fi, err := f.Stat()
	if err != nil {
		return 0, err
	}
	if fi.Size() == 0 {
		return 0, nil
	}

	// If file is larger than 64KB, inspect the last 64KB chunk.
	const tailChunk = 64 * 1024
	var offset int64
	if fi.Size() > tailChunk {
		offset = fi.Size() - tailChunk
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return 0, err
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	var lastRev uint64
	var found bool
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var partial struct {
			Revision uint64 `json:"revision"`
		}
		if err := json.Unmarshal(line, &partial); err == nil && partial.Revision > 0 {
			lastRev = partial.Revision
			found = true
		}
	}
	if err := scanner.Err(); err != nil && !found {
		return 0, err
	}

	// If offset > 0 and no revision found in tail, fallback to full scan from start.
	if !found && offset > 0 {
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return 0, err
		}
		fullScanner := bufio.NewScanner(f)
		fullScanner.Buffer(make([]byte, 64*1024), 1024*1024)
		for fullScanner.Scan() {
			line := bytes.TrimSpace(fullScanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var partial struct {
				Revision uint64 `json:"revision"`
			}
			if err := json.Unmarshal(line, &partial); err == nil && partial.Revision > 0 {
				lastRev = partial.Revision
			}
		}
		if err := fullScanner.Err(); err != nil {
			return 0, err
		}
	}

	return lastRev, nil
}
