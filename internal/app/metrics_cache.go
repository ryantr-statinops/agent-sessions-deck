package app

import (
	"sync"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// DefaultMetricsTTL is the freshness window for process metrics.
const DefaultMetricsTTL = 2 * time.Second

// MetricsSamplerFunc samples process metrics given identity and previous tick counters.
type MetricsSamplerFunc func(identity session.ProcessIdentity, attemptStartedAt time.Time, prev *metrics.SampleRecord, now time.Time) (metrics.ProcessMetrics, metrics.SampleRecord, error)

type metricsCacheEntry struct {
	metrics   metrics.ProcessMetrics
	record    metrics.SampleRecord
	fetchedAt time.Time
}

// MetricsCache caches process metrics with TTL and stale fallback.
type MetricsCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	sampler MetricsSamplerFunc
	clock   func() time.Time
	entries map[session.ID]metricsCacheEntry
}

// NewMetricsCache creates a MetricsCache with the given TTL and sampler.
func NewMetricsCache(ttl time.Duration, sampler MetricsSamplerFunc, clock func() time.Time) *MetricsCache {
	if ttl <= 0 {
		ttl = DefaultMetricsTTL
	}
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &MetricsCache{
		ttl:     ttl,
		sampler: sampler,
		clock:   clock,
		entries: make(map[session.ID]metricsCacheEntry),
	}
}

// GetOrSample retrieves metrics for a session, sampling if expired or absent.
// On sample failure, falls back to stale cached metrics if available.
func (c *MetricsCache) GetOrSample(sessionID session.ID, identity session.ProcessIdentity, attemptStartedAt time.Time) metrics.ProcessMetrics {
	if identity.PID <= 0 {
		return metrics.ProcessMetrics{
			Unavailable: true,
			Error:       metrics.ErrPIDNotFound,
		}
	}

	now := c.clock()

	c.mu.RLock()
	entry, exists := c.entries[sessionID]
	if exists && entry.metrics.PID == identity.PID && now.Sub(entry.fetchedAt) < c.ttl {
		c.mu.RUnlock()
		return entry.metrics
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double check under write lock
	entry, exists = c.entries[sessionID]
	if exists && entry.metrics.PID == identity.PID && now.Sub(entry.fetchedAt) < c.ttl {
		return entry.metrics
	}

	if c.sampler == nil {
		return metrics.ProcessMetrics{
			PID:         identity.PID,
			Unavailable: true,
			Error:       "no sampler configured",
		}
	}

	var prev *metrics.SampleRecord
	if exists && entry.metrics.PID == identity.PID {
		prev = &entry.record
	}

	newMetrics, nextRecord, err := c.sampler(identity, attemptStartedAt, prev, now)
	if err != nil {
		if exists && entry.metrics.PID == identity.PID {
			fallback := entry.metrics
			fallback.Stale = true
			return fallback
		}
		return metrics.ProcessMetrics{
			PID:         identity.PID,
			Unavailable: true,
			Error:       err.Error(),
		}
	}

	c.entries[sessionID] = metricsCacheEntry{
		metrics:   newMetrics,
		record:    nextRecord,
		fetchedAt: now,
	}

	return newMetrics
}

// Invalidate removes cached entries for a session.
func (c *MetricsCache) Invalidate(sessionID session.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, sessionID)
}
