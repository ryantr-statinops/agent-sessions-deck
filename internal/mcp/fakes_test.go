package mcp

import (
	"context"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

type fakeControlPlaneClient struct {
	scanResult       app.ScanResult
	workspacesResult app.WorkspacesResult
	listResult       app.ListResult
	getResult        app.GetResult
	eventsResult     app.EventsResult
	historyResult    app.HistoryResult
	createResult     app.CreateResult
	stopResult       app.StopResult
	restartResult    app.RestartResult
	openResult       app.OpenResult

	scanErr       error
	workspacesErr error
	listErr       error
	getErr        error
	eventsErr     error
	historyErr    error
	createErr     error
	stopErr       error
	restartErr    error
	openErr       error

	lastCreateReq  app.CreateRequest
	lastStopReq    app.StopRequest
	lastRestartReq app.RestartRequest
	lastOpenReq    app.OpenRequest
}

func (f *fakeControlPlaneClient) Scan(ctx context.Context, req app.ScanRequest) (app.ScanResult, error) {
	return f.scanResult, f.scanErr
}

func (f *fakeControlPlaneClient) Workspaces(ctx context.Context, req app.WorkspacesRequest) (app.WorkspacesResult, error) {
	return f.workspacesResult, f.workspacesErr
}

func (f *fakeControlPlaneClient) List(ctx context.Context, req app.ListRequest) (app.ListResult, error) {
	return f.listResult, f.listErr
}

func (f *fakeControlPlaneClient) Get(ctx context.Context, req app.GetRequest) (app.GetResult, error) {
	return f.getResult, f.getErr
}

func (f *fakeControlPlaneClient) Events(ctx context.Context, req app.EventsRequest) (app.EventsResult, error) {
	return f.eventsResult, f.eventsErr
}

func (f *fakeControlPlaneClient) History(ctx context.Context, req app.HistoryRequest) (app.HistoryResult, error) {
	return f.historyResult, f.historyErr
}

func (f *fakeControlPlaneClient) Create(ctx context.Context, req app.CreateRequest) (app.CreateResult, error) {
	f.lastCreateReq = req
	return f.createResult, f.createErr
}

func (f *fakeControlPlaneClient) Stop(ctx context.Context, req app.StopRequest) (app.StopResult, error) {
	f.lastStopReq = req
	return f.stopResult, f.stopErr
}

func (f *fakeControlPlaneClient) Restart(ctx context.Context, req app.RestartRequest) (app.RestartResult, error) {
	f.lastRestartReq = req
	return f.restartResult, f.restartErr
}

func (f *fakeControlPlaneClient) Open(ctx context.Context, req app.OpenRequest) (app.OpenResult, error) {
	f.lastOpenReq = req
	return f.openResult, f.openErr
}

// verify compiler checks
var _ ControlPlaneClient = (*fakeControlPlaneClient)(nil)

// helper to make dummy agent probes
func sampleAgentProbes() []app.AgentSummary {
	return []app.AgentSummary{
		{
			ID:         agent.ID("opencode"),
			Launchable: true,
		},
		{
			ID:         agent.ID("claude"),
			Launchable: false,
		},
	}
}

// helper to make dummy workspaces
func sampleWorkspaces() []workspace.WorkspaceSummary {
	ws, _ := workspace.New("/home/user/project-a")
	return []workspace.WorkspaceSummary{
		{
			Workspace:   ws,
			ActiveCount: 2,
			TotalCount:  3,
		},
	}
}
