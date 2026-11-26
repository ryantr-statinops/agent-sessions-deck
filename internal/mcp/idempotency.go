package mcp

import (
	"sync"
	"time"
)

// IdempotencyEntry stores the result of a previous mutation execution.
type IdempotencyEntry struct {
	Result    CallToolResult
	CreatedAt time.Time
}

// IdempotencyManager tracks mutation request IDs to avoid duplicate execution.
type IdempotencyManager struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]IdempotencyEntry
}

// NewIdempotencyManager creates a manager with specified entry retention TTL.
func NewIdempotencyManager(ttl time.Duration) *IdempotencyManager {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &IdempotencyManager{
		ttl:     ttl,
		entries: make(map[string]IdempotencyEntry),
	}
}

// Get checks if a request ID was already processed and still valid.
func (m *IdempotencyManager) Get(requestID string) (CallToolResult, bool) {
	if requestID == "" {
		return CallToolResult{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.entries[requestID]
	if !ok {
		return CallToolResult{}, false
	}
	if time.Since(entry.CreatedAt) > m.ttl {
		delete(m.entries, requestID)
		return CallToolResult{}, false
	}
	return entry.Result, true
}

// Set records the outcome of a mutation under requestID.
func (m *IdempotencyManager) Set(requestID string, res CallToolResult) {
	if requestID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clean expired entries if map grows large
	if len(m.entries) > 1000 {
		now := time.Now()
		for k, v := range m.entries {
			if now.Sub(v.CreatedAt) > m.ttl {
				delete(m.entries, k)
			}
		}
	}

	m.entries[requestID] = IdempotencyEntry{
		Result:    res,
		CreatedAt: time.Now(),
	}
}
