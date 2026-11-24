package app

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// DefaultGitThrottleTTL is the throttle duration for git status probing.
const DefaultGitThrottleTTL = 10 * time.Second

// GitProberFunc defines the signature for probing repository metadata for a directory.
type GitProberFunc func(ctx context.Context, dir string) (workspace.Git, error)

type gitThrottleEntry struct {
	git       workspace.Git
	err       error
	fetchedAt time.Time
}

// GitThrottleCache limits git status probes to at most once per TTL per workspace directory.
type GitThrottleCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	prober  GitProberFunc
	clock   func() time.Time
	entries map[string]gitThrottleEntry
}

// NewGitThrottleCache creates a GitThrottleCache with specified TTL, prober, and clock.
func NewGitThrottleCache(ttl time.Duration, prober GitProberFunc, clock func() time.Time) *GitThrottleCache {
	if ttl <= 0 {
		ttl = DefaultGitThrottleTTL
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &GitThrottleCache{
		ttl:     ttl,
		prober:  prober,
		clock:   clock,
		entries: make(map[string]gitThrottleEntry),
	}
}

// Probe returns cached git metadata if within TTL; otherwise invokes prober and caches result.
func (c *GitThrottleCache) Probe(ctx context.Context, dir string) (workspace.Git, error) {
	key := filepath.Clean(dir)
	now := c.clock()

	c.mu.RLock()
	entry, exists := c.entries[key]
	if exists && now.Sub(entry.fetchedAt) < c.ttl {
		c.mu.RUnlock()
		return entry.git, entry.err
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check under write lock
	entry, exists = c.entries[key]
	if exists && now.Sub(entry.fetchedAt) < c.ttl {
		return entry.git, entry.err
	}

	if c.prober == nil {
		return workspace.Git{}, nil
	}

	gitMeta, err := c.prober(ctx, dir)
	c.entries[key] = gitThrottleEntry{
		git:       gitMeta,
		err:       err,
		fetchedAt: now,
	}

	return gitMeta, err
}

// Invalidate clears cached metadata for a specific directory.
func (c *GitThrottleCache) Invalidate(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, filepath.Clean(dir))
}
