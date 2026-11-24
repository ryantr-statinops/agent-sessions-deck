//go:build linux

package app

import (
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func defaultProcessSampler(identity session.ProcessIdentity, startedAt time.Time, prev *metrics.SampleRecord, now time.Time) (metrics.ProcessMetrics, metrics.SampleRecord, error) {
	return metrics.SampleProcessTree("/proc", identity, startedAt, prev, now)
}
