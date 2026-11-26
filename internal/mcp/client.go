package mcp

import (
	"context"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

// ControlPlaneClient defines the operations needed by MCP tools from the ASD control plane.
// *ipc.Client directly satisfies this interface.
type ControlPlaneClient interface {
	Scan(ctx context.Context, req app.ScanRequest) (app.ScanResult, error)
	List(ctx context.Context, req app.ListRequest) (app.ListResult, error)
	Get(ctx context.Context, req app.GetRequest) (app.GetResult, error)
	Workspaces(ctx context.Context, req app.WorkspacesRequest) (app.WorkspacesResult, error)
	Events(ctx context.Context, req app.EventsRequest) (app.EventsResult, error)
	History(ctx context.Context, req app.HistoryRequest) (app.HistoryResult, error)
	Create(ctx context.Context, req app.CreateRequest) (app.CreateResult, error)
	Stop(ctx context.Context, req app.StopRequest) (app.StopResult, error)
	Restart(ctx context.Context, req app.RestartRequest) (app.RestartResult, error)
	Open(ctx context.Context, req app.OpenRequest) (app.OpenResult, error)
}
