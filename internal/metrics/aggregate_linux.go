//go:build linux

package metrics

import (
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// SampleProcessTree samples the root process and recursively aggregates
// memory RSS and process count across all verified active descendants.
func SampleProcessTree(procRoot string, identity session.ProcessIdentity, attemptStartedAt time.Time, prev *SampleRecord, now time.Time, opts ...SamplerOption) (ProcessMetrics, SampleRecord, error) {
	rootMetric, nextRecord, err := SampleProcess(procRoot, identity, attemptStartedAt, prev, now, opts...)
	if err != nil || rootMetric.Unavailable {
		return rootMetric, nextRecord, err
	}

	descendants, err := DiscoverDescendants(procRoot, identity.PID, identity.StartTicks)
	if err != nil {
		// Descendant discovery failure does not invalidate root metrics
		return rootMetric, nextRecord, nil
	}

	for _, d := range descendants {
		statm, err := ReadProcStatm(procRoot, d.PID)
		if err != nil {
			continue
		}
		rootMetric.TreeRSSBytes += statm.RSSBytes
		rootMetric.ProcessCount++
	}

	return rootMetric, nextRecord, nil
}
