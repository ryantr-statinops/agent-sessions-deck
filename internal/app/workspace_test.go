package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestWorkspacesGroupingAndSorting(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	tmp := t.TempDir()
	ws1 := filepath.Clean(filepath.Join(tmp, "ws1"))
	ws2 := filepath.Clean(filepath.Join(tmp, "ws2"))
	_ = os.MkdirAll(ws1, 0755)
	_ = os.MkdirAll(ws2, 0755)
	h.resolver.known[ws1] = ws1
	h.resolver.known[ws2] = ws2
	h.provider.command = mustCommand(t, filepath.Join(tmp, "opencode"))

	// Create session 1 in ws1
	c1, err := h.svc.Create(ctx, CreateRequest{
		Agent:         "opencode",
		WorkspacePath: ws1,
		Name:          "sess-1",
	})
	if err != nil {
		t.Fatalf("Create sess-1: %v", err)
	}

	// Create session 2 in ws1
	c2, err := h.svc.Create(ctx, CreateRequest{
		Agent:         "opencode",
		WorkspacePath: ws1,
		Name:          "sess-2",
	})
	if err != nil {
		t.Fatalf("Create sess-2: %v", err)
	}

	// Create session 3 in ws2
	c3, err := h.svc.Create(ctx, CreateRequest{
		Agent:         "opencode",
		WorkspacePath: ws2,
		Name:          "sess-3",
	})
	if err != nil {
		t.Fatalf("Create sess-3: %v", err)
	}

	// Stop session 2
	h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(0)}
	if _, err := h.svc.Stop(ctx, StopRequest{Ref: string(c2.Session.ID)}); err != nil {
		t.Fatalf("Stop sess-2: %v", err)
	}

	// Query workspaces
	res, err := h.svc.Workspaces(ctx, WorkspacesRequest{})
	if err != nil {
		t.Fatalf("Workspaces: %v", err)
	}

	if len(res.Workspaces) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(res.Workspaces))
	}

	for _, ws := range res.Workspaces {
		if ws.Workspace.Path() == ws1 {
			if ws.TotalCount != 2 {
				t.Fatalf("ws1 total count want 2, got %d", ws.TotalCount)
			}
			if ws.ActiveCount != 1 {
				t.Fatalf("ws1 active count want 1, got %d", ws.ActiveCount)
			}
		} else if ws.Workspace.Path() == ws2 {
			if ws.TotalCount != 1 {
				t.Fatalf("ws2 total count want 1, got %d", ws.TotalCount)
			}
			if ws.ActiveCount != 1 {
				t.Fatalf("ws2 active count want 1, got %d", ws.ActiveCount)
			}
		}
	}

	_ = c1
	_ = c3
}

func TestWorkspacesGitMetadata(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	tmp := t.TempDir()
	ws1 := filepath.Clean(filepath.Join(tmp, "gitws"))
	_ = os.MkdirAll(ws1, 0755)
	h.resolver.known[ws1] = ws1
	h.provider.command = mustCommand(t, filepath.Join(tmp, "opencode"))

	gitProber := func(ctx context.Context, dir string) (workspace.Git, error) {
		return workspace.Git{
			Root:   dir,
			Branch: "feat/observability",
			Dirty:  true,
		}, nil
	}
	h.svc.gitCache = NewGitThrottleCache(10*time.Second, gitProber, h.svc.now)

	_, err := h.svc.Create(ctx, CreateRequest{
		Agent:         "opencode",
		WorkspacePath: ws1,
		Name:          "git-sess",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := h.svc.Workspaces(ctx, WorkspacesRequest{})
	if err != nil {
		t.Fatalf("Workspaces: %v", err)
	}

	if len(res.Workspaces) == 0 {
		t.Fatalf("expected at least 1 workspace")
	}
	wsSummary := res.Workspaces[0]
	if wsSummary.Git == nil {
		t.Fatalf("expected Git metadata attached to summary")
	}
	if wsSummary.Git.Branch != "feat/observability" || !wsSummary.Git.Dirty {
		t.Fatalf("unexpected git metadata: %+v", wsSummary.Git)
	}
}

func TestWorkspacesFilterLifecycles(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	tmp := t.TempDir()
	ws1 := filepath.Clean(filepath.Join(tmp, "filterws"))
	_ = os.MkdirAll(ws1, 0755)
	h.resolver.known[ws1] = ws1
	h.provider.command = mustCommand(t, filepath.Join(tmp, "opencode"))

	c1, err := h.svc.Create(ctx, CreateRequest{
		Agent:         "opencode",
		WorkspacePath: ws1,
		Name:          "filter-sess-1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Stop it so it's exited
	h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(0)}
	if _, err := h.svc.Stop(ctx, StopRequest{Ref: string(c1.Session.ID)}); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// Filter for running lifecycles only
	res, err := h.svc.Workspaces(ctx, WorkspacesRequest{
		Lifecycles: []session.Lifecycle{session.LifecycleRunning},
	})
	if err != nil {
		t.Fatalf("Workspaces: %v", err)
	}
	if len(res.Workspaces) != 0 {
		t.Fatalf("expected 0 running workspaces, got %d", len(res.Workspaces))
	}
}

func TestGetWithMetrics(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	tmp := t.TempDir()
	ws1 := filepath.Clean(filepath.Join(tmp, "metricsws"))
	_ = os.MkdirAll(ws1, 0755)
	h.resolver.known[ws1] = ws1
	h.provider.command = mustCommand(t, filepath.Join(tmp, "opencode"))

	sampler := func(identity session.ProcessIdentity, attemptStartedAt time.Time, prev *metrics.SampleRecord, now time.Time) (metrics.ProcessMetrics, metrics.SampleRecord, error) {
		return metrics.ProcessMetrics{
			PID:            identity.PID,
			CPUPercent:     37.5,
			MemoryRSSBytes: 1024 * 1024 * 80,
			ProcessCount:   3,
			SampledAt:      now,
		}, metrics.SampleRecord{SampledAt: now, TotalTicks: 500}, nil
	}
	h.svc.metricsCache = NewMetricsCache(2*time.Second, sampler, h.svc.now)

	created, err := h.svc.Create(ctx, CreateRequest{
		Agent:         "opencode",
		WorkspacePath: ws1,
		Name:          "metrics-sess",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := h.svc.Get(ctx, GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if res.Metrics.CPUPercent != 37.5 {
		t.Fatalf("expected CPU 37.5, got %f", res.Metrics.CPUPercent)
	}
	if res.Metrics.ProcessCount != 3 {
		t.Fatalf("expected ProcessCount 3, got %d", res.Metrics.ProcessCount)
	}
}
