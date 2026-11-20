//go:build linux

package owner

import (
	"context"
	"fmt"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

// ShutdownReport summarizes the outcome of owner graceful shutdown.
type ShutdownReport struct {
	TotalSessions   int
	StoppedSessions int
	ResidualPIDs    []int
	TimedOutRef     []string
}

// Shutdown initiates a bounded graceful termination of all active sessions
// before closing the owner IPC server and releasing state locks.
// It never escalates to SIGKILL automatically (no silent escalation).
func (r *Runtime) Shutdown(ctx context.Context, grace time.Duration) (ShutdownReport, error) {
	if r == nil {
		return ShutdownReport{}, nil
	}
	if grace <= 0 {
		grace = 5 * time.Second
	}

	report := ShutdownReport{}
	if r.app == nil {
		return report, r.Close()
	}

	listRes, err := r.app.List(ctx, app.ListRequest{})
	if err != nil {
		_ = r.Close()
		return report, fmt.Errorf("list sessions for shutdown: %w", err)
	}

	for _, sess := range listRes.Snapshot.Sessions {
		if sess.Lifecycle.Terminal() {
			continue
		}
		report.TotalSessions++

		stopCtx, cancel := context.WithTimeout(ctx, grace+2*time.Second)
		stopRes, stopErr := r.app.Stop(stopCtx, app.StopRequest{
			Ref:   string(sess.ID),
			Grace: grace,
		})
		cancel()

		if stopErr == nil && stopRes.Stopped {
			report.StoppedSessions++
		} else {
			// Process ignored SIGTERM or timed out
			report.TimedOutRef = append(report.TimedOutRef, string(sess.ID))
			if sess.HasIdentity && sess.Identity.PID > 0 {
				report.ResidualPIDs = append(report.ResidualPIDs, sess.Identity.PID)
			}
		}
	}

	closeErr := r.Close()
	return report, closeErr
}
