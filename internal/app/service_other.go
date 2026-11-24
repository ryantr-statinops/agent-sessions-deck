//go:build !linux

package app

import (
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func defaultProcessSampler(identity session.ProcessIdentity, startedAt time.Time, prev *metrics.SampleRecord, now time.Time) (metrics.ProcessMetrics, metrics.SampleRecord, error) {
	return metrics.ProcessMetrics{
		PID:         identity.PID,
		Unavailable: true,
		Error:       "metrics sampling only supported on linux",
	}, metrics.SampleRecord{}, nil
}
