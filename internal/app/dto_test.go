package app

import (
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestWorkspacesRequestValidate(t *testing.T) {
	valid := WorkspacesRequest{
		Lifecycles: []session.Lifecycle{session.LifecycleRunning, session.LifecycleExited},
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid, got: %v", err)
	}

	invalid := WorkspacesRequest{
		Lifecycles: []session.Lifecycle{session.Lifecycle("invalid-state")},
	}
	if err := invalid.Validate(); err == nil {
		t.Fatalf("expected error for invalid lifecycle, got nil")
	}
}

func TestGetResultWithMetrics(t *testing.T) {
	now := time.Now().UTC()
	m := metrics.ProcessMetrics{
		SampledAt:      now,
		PID:            4129,
		CPUPercent:     12.5,
		MemoryRSSBytes: 1024 * 1024 * 50,
	}

	res := GetResult{
		ObservedAt: now,
		Metrics:    m,
	}

	if res.Metrics.PID != 4129 || res.Metrics.CPUPercent != 12.5 {
		t.Fatalf("mismatch in GetResult.Metrics: %+v", res.Metrics)
	}

	wsRes := WorkspacesResult{
		Authority:  session.AuthorityLive,
		ObservedAt: now,
		Workspaces: []workspace.WorkspaceSummary{},
	}
	if wsRes.Authority != session.AuthorityLive {
		t.Fatalf("authority mismatch in WorkspacesResult")
	}
}
