//go:build linux

package metrics

import (
	"errors"
	"io/fs"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

const defaultClockTicksPerSec = 100 // Default Linux user HZ

// SamplerOption configures the process metrics sampler.
type SamplerOption func(*samplerConfig)

type samplerConfig struct {
	clockTicks uint64
}

// WithClockTicks sets the clock tick rate (default: 100 Hz).
func WithClockTicks(ticks uint64) SamplerOption {
	return func(c *samplerConfig) {
		if ticks > 0 {
			c.clockTicks = ticks
		}
	}
}

// SampleProcess reads metrics for a single verified process from procRoot.
func SampleProcess(procRoot string, identity session.ProcessIdentity, attemptStartedAt time.Time, prev *SampleRecord, now time.Time, opts ...SamplerOption) (ProcessMetrics, SampleRecord, error) {
	cfg := samplerConfig{clockTicks: defaultClockTicksPerSec}
	for _, opt := range opts {
		opt(&cfg)
	}

	if now.IsZero() {
		now = time.Now().UTC()
	}

	stat, err := ReadProcStat(procRoot, identity.PID)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ProcessMetrics{
				SampledAt:   now,
				PID:         identity.PID,
				Unavailable: true,
				Error:       "process-exited",
			}, SampleRecord{}, nil
		}
		if errors.Is(err, fs.ErrPermission) {
			return ProcessMetrics{
				SampledAt:   now,
				PID:         identity.PID,
				Unavailable: true,
				Error:       "permission-denied",
			}, SampleRecord{}, nil
		}
		return ProcessMetrics{
			SampledAt:   now,
			PID:         identity.PID,
			Unavailable: true,
			Error:       err.Error(),
		}, SampleRecord{}, nil
	}

	// PID reuse verification: start ticks must strictly match recorded process identity
	if identity.StartTicks > 0 && stat.StartTicks != identity.StartTicks {
		return ProcessMetrics{
			SampledAt:   now,
			PID:         identity.PID,
			Unavailable: true,
			Error:       "pid-reused",
		}, SampleRecord{}, nil
	}

	statm, err := ReadProcStatm(procRoot, identity.PID)
	if err != nil {
		statm = ProcStatm{}
	}

	// Calculate CPU percent delta
	var cpuPercent float64
	currTotalTicks := stat.TotalTicks()

	if prev != nil && !prev.SampledAt.IsZero() && now.After(prev.SampledAt) {
		deltaSeconds := now.Sub(prev.SampledAt).Seconds()
		if deltaSeconds > 0.05 && currTotalTicks >= prev.TotalTicks {
			deltaTicks := currTotalTicks - prev.TotalTicks
			cpuPercent = (float64(deltaTicks) / (float64(cfg.clockTicks) * deltaSeconds)) * 100.0
			if cpuPercent < 0 {
				cpuPercent = 0
			}
		}
	}

	var uptime time.Duration
	if !attemptStartedAt.IsZero() && now.After(attemptStartedAt) {
		uptime = now.Sub(attemptStartedAt)
	}

	res := ProcessMetrics{
		SampledAt:      now,
		PID:            stat.PID,
		PGID:           stat.PGID,
		State:          stat.State,
		Uptime:         uptime,
		CPUPercent:     cpuPercent,
		MemoryRSSBytes: statm.RSSBytes,
		TreeRSSBytes:   statm.RSSBytes,
		ProcessCount:   1,
	}

	nextRecord := SampleRecord{
		SampledAt:  now,
		TotalTicks: currTotalTicks,
	}

	return res, nextRecord, nil
}
