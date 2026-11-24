package metrics

import (
	"time"
)

// ProcessMetrics captures resource usage and execution state for a verified process.
type ProcessMetrics struct {
	SampledAt      time.Time     `json:"sampled_at"`
	PID            int           `json:"pid"`
	PGID           int           `json:"pgid"`
	State          string        `json:"state"`
	Uptime         time.Duration `json:"uptime"`
	CPUPercent     float64       `json:"cpu_percent"`
	MemoryRSSBytes uint64        `json:"memory_rss_bytes"`
	TreeRSSBytes   uint64        `json:"tree_rss_bytes"`
	ProcessCount   int           `json:"process_count"`
	Stale          bool          `json:"stale,omitempty"`
	Unavailable    bool          `json:"unavailable,omitempty"`
	Error          string        `json:"error,omitempty"`
}

// IsZero reports whether the metrics snapshot is uninitialized.
func (m ProcessMetrics) IsZero() bool {
	return m.PID == 0 && m.SampledAt.IsZero()
}

// Active reports whether the process is observed in an active kernel state.
func (m ProcessMetrics) Active() bool {
	return !m.Unavailable && (m.State == "R" || m.State == "S" || m.State == "D")
}
