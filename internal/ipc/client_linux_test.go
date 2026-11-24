//go:build linux

package ipc

import (
	"context"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

type observabilityClient struct {
	app.Client
}

func (observabilityClient) Workspaces(context.Context, app.WorkspacesRequest) (app.WorkspacesResult, error) {
	return app.WorkspacesResult{
		Authority: session.AuthorityLive,
		Workspaces: []workspace.WorkspaceSummary{
			{
				ActiveCount: 2,
				TotalCount:  3,
				SessionIDs:  []string{"s1", "s2", "s3"},
			},
		},
	}, nil
}

func (observabilityClient) Get(context.Context, app.GetRequest) (app.GetResult, error) {
	return app.GetResult{
		ObservedAt: time.Now().UTC(),
		Metrics: metrics.ProcessMetrics{
			PID:            8888,
			CPUPercent:     45.2,
			MemoryRSSBytes: 1024 * 1024 * 128,
			ProcessCount:   4,
		},
	}, nil
}

func TestClientWorkspacesAndMetricsRoundTrip(t *testing.T) {
	paths := config.Paths{StateDir: t.TempDir()}
	owner, err := BootstrapPaths(paths)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(owner, observabilityClient{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()

	client := NewClient(mustSocketPath(t, paths))
	callCtx, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()

	wsRes, err := client.Workspaces(callCtx, app.WorkspacesRequest{})
	if err != nil {
		t.Fatalf("client.Workspaces failed: %v", err)
	}
	if len(wsRes.Workspaces) != 1 || wsRes.Workspaces[0].TotalCount != 3 {
		t.Fatalf("unexpected workspaces result: %+v", wsRes)
	}

	getRes, err := client.Get(callCtx, app.GetRequest{Ref: "s1"})
	if err != nil {
		t.Fatalf("client.Get failed: %v", err)
	}
	if getRes.Metrics.PID != 8888 || getRes.Metrics.CPUPercent != 45.2 || getRes.Metrics.ProcessCount != 4 {
		t.Fatalf("unexpected metrics in GetResult: %+v", getRes.Metrics)
	}
}
