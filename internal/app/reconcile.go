package app

import (
	"context"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// ReconciliationSummary summarizes the outcome of the startup reconciliation pass.
type ReconciliationSummary struct {
	TotalChecked      int
	ReconciledDead    int
	ReconciledOrphan  int
	ReconciledStale   int
	ReconciledUnknown int
	Unchanged         int
}

// ReconcileStartup inspects all loaded sessions and reconciles any active attempt
// against the true operating system liveness.
func (s *Service) ReconcileStartup(ctx context.Context) (ReconciliationSummary, error) {
	summary := ReconciliationSummary{}
	if s.authority != session.AuthorityLive {
		return summary, nil
	}

	sessions := s.engine.Sessions()
	for _, sess := range sessions {
		curr, hasCurr := sess.Current()
		if !hasCurr || curr.Lifecycle.Terminal() {
			continue
		}

		summary.TotalChecked++

		// If no valid identity was ever captured, it cannot be probed.
		if !curr.HasIdentity() {
			continue
		}

		// Probe process liveness via runtime port
		obs, err := s.runtime.Observe(ctx, curr.Identity)
		if err != nil {
			obs = session.LivenessObservation{
				Outcome: session.ProbeUnverifiable,
				Detail:  err.Error(),
				At:      s.now(),
			}
		}

		// On owner startup, any surviving process from a previous owner has lost its PTY.
		// Reporting PTYLost classifies it as dead, stale, orphaned, or unknown.
		kind := ReportPTYLost

		repReq := ReportRequest{
			Ref:      string(sess.ID),
			Attempt:  curr.Generation,
			Kind:     kind,
			At:       s.now(),
			Liveness: obs,
		}

		res, err := s.Report(ctx, repReq)
		if err != nil {
			return summary, err
		}

		switch res.Row {
		case session.RowProcessGone, session.RowNaturalExit, session.RowStopped:
			summary.ReconciledDead++
		case session.RowPTYLostAlive:
			summary.ReconciledOrphan++
		case session.RowIdentityMismatch:
			summary.ReconciledStale++
		case session.RowPTYLostUnverifiable, session.RowProbeInconclusive:
			summary.ReconciledUnknown++
		default:
			if res.Session.Orphaned() {
				summary.ReconciledOrphan++
			} else {
				summary.Unchanged++
			}
		}
	}

	return summary, nil
}
