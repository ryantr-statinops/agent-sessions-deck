package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// RegisterReadTools registers all Stage 14 read-only tools on the MCP server.
func RegisterReadTools(s *Server, client ControlPlaneClient) {
	registerListAgents(s, client)
	registerListWorkspaces(s, client)
	registerGetWorkspace(s, client)
	registerGetProcess(s, client)
	registerListSessions(s, client)
	registerGetSession(s, client)
	registerFindSession(s, client)
}

func registerListAgents(s *Server, client ControlPlaneClient) {
	s.RegisterTool(Tool{
		Name:        "list_agents",
		Description: "List discovered and configured coding-agent executables, their availability status and paths.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"agent_id": {
					Type:        "string",
					Description: "Optional filter for a specific agent ID (e.g. 'opencode', 'claude', 'codex')",
				},
			},
		},
		Annotations: &ToolAnnotation{ReadOnly: true},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		var input struct {
			AgentID string `json:"agent_id"`
		}
		if len(args) > 0 {
			_ = json.Unmarshal(args, &input)
		}

		res, err := client.Scan(ctx, app.ScanRequest{})
		if err != nil {
			return NewErrorResult(fmt.Sprintf("failed to scan agents: %v", err)), nil
		}

		if input.AgentID != "" {
			var filtered []app.AgentSummary
			for _, item := range res.Agents {
				if string(item.ID) == input.AgentID {
					filtered = append(filtered, item)
				}
			}
			return NewJSONResult(filtered)
		}

		return NewJSONResult(res.Agents)
	})
}

func registerListWorkspaces(s *Server, client ControlPlaneClient) {
	s.RegisterTool(Tool{
		Name:        "list_workspaces",
		Description: "List discovered workspaces, active session counts, git branch/status, and resource metrics.",
		InputSchema: ToolSchema{
			Type:       "object",
			Properties: map[string]PropertyDef{},
		},
		Annotations: &ToolAnnotation{ReadOnly: true},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		res, err := client.Workspaces(ctx, app.WorkspacesRequest{})
		if err != nil {
			return NewErrorResult(fmt.Sprintf("failed to list workspaces: %v", err)), nil
		}
		return NewJSONResult(res.Workspaces)
	})
}

func registerGetWorkspace(s *Server, client ControlPlaneClient) {
	s.RegisterTool(Tool{
		Name:        "get_workspace",
		Description: "Get detailed summary, git metadata, and active session counts for a specific workspace path.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"path": {
					Type:        "string",
					Description: "Absolute or canonical path to the workspace directory",
				},
			},
			Required: []string{"path"},
		},
		Annotations: &ToolAnnotation{ReadOnly: true},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		var input struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(args, &input); err != nil || input.Path == "" {
			return NewErrorResult("get_workspace requires a non-empty 'path'"), nil
		}

		res, err := client.Workspaces(ctx, app.WorkspacesRequest{})
		if err != nil {
			return NewErrorResult(fmt.Sprintf("failed to query workspaces: %v", err)), nil
		}

		targetCanonical, _ := canonicalizePath(input.Path)
		for _, ws := range res.Workspaces {
			wsPath := ws.Workspace.Path()
			wsCanonical, _ := canonicalizePath(wsPath)
			if wsPath == input.Path || wsCanonical == targetCanonical {
				return NewJSONResult(ws)
			}
		}

		return NewErrorResult(fmt.Sprintf("workspace %q not found", input.Path)), nil
	})
}

func registerGetProcess(s *Server, client ControlPlaneClient) {
	s.RegisterTool(Tool{
		Name:        "get_process",
		Description: "Inspect the child process hierarchy, PID, state, and attempt identity for an agent session.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"session_id": {
					Type:        "string",
					Description: "Full session ID or unambiguous prefix",
				},
			},
			Required: []string{"session_id"},
		},
		Annotations: &ToolAnnotation{ReadOnly: true},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		var input struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil || input.SessionID == "" {
			return NewErrorResult("get_process requires a non-empty 'session_id'"), nil
		}

		res, err := client.Get(ctx, app.GetRequest{Ref: input.SessionID})
		if err != nil {
			return NewErrorResult(fmt.Sprintf("failed to get session %q: %v", input.SessionID, err)), nil
		}

		row := res.Session
		type ProcessReport struct {
			SessionID    string `json:"session_id"`
			Generation   int    `json:"generation"`
			Lifecycle    string `json:"lifecycle"`
			HasIdentity  bool   `json:"has_identity"`
			PID          int    `json:"pid,omitempty"`
			PGID         int    `json:"pgid,omitempty"`
			Command      string `json:"command,omitempty"`
			LastSeenAt   string `json:"last_seen_at,omitempty"`
			AttemptCount int    `json:"attempt_count"`
		}

		report := ProcessReport{
			SessionID:    row.ID.String(),
			Generation:   int(row.Generation),
			Lifecycle:    row.Lifecycle.String(),
			HasIdentity:  row.HasIdentity,
			AttemptCount: row.AttemptCount,
		}
		if row.HasIdentity {
			report.PID = row.Identity.PID
			report.PGID = row.Identity.PGID
		}
		if row.Command.Executable() != "" {
			report.Command = row.Command.Executable()
		}
		if !row.LastSeenAt.IsZero() {
			report.LastSeenAt = row.LastSeenAt.Format("2006-01-02T15:04:05Z07:00")
		}

		return NewJSONResult(report)
	})
}

// SessionSummaryDTO represents a clean JSON representation of a session.
type SessionSummaryDTO struct {
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	AgentID    string `json:"agent_id"`
	Lifecycle  string `json:"lifecycle"`
	Generation int    `json:"generation"`
	PID        int    `json:"pid,omitempty"`
	Authority  string `json:"authority"`
	LastSeenAt string `json:"last_seen_at,omitempty"`
}

func toSessionSummaryDTO(row session.SessionSnapshot) SessionSummaryDTO {
	dto := SessionSummaryDTO{
		ID:         row.ID.String(),
		Name:       row.Name,
		AgentID:    string(row.AgentID),
		Lifecycle:  row.Lifecycle.String(),
		Generation: int(row.Generation),
		Authority:  row.Authority.String(),
	}
	if row.HasIdentity {
		dto.PID = row.Identity.PID
	}
	if !row.LastSeenAt.IsZero() {
		dto.LastSeenAt = row.LastSeenAt.Format("2006-01-02T15:04:05Z07:00")
	}
	return dto
}

func registerListSessions(s *Server, client ControlPlaneClient) {
	s.RegisterTool(Tool{
		Name:        "list_sessions",
		Description: "List coding-agent sessions with optional status and agent filters.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"status": {
					Type:        "string",
					Description: "Filter by lifecycle status ('RUNNING', 'STOPPED', 'FAILED', 'CREATED')",
				},
				"agent": {
					Type:        "string",
					Description: "Filter by agent ID (e.g. 'opencode')",
				},
			},
		},
		Annotations: &ToolAnnotation{ReadOnly: true},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		var input struct {
			Status string `json:"status"`
			Agent  string `json:"agent"`
		}
		if len(args) > 0 {
			_ = json.Unmarshal(args, &input)
		}

		req := app.ListRequest{}
		if input.Agent != "" {
			req.AgentID = agent.ID(input.Agent)
		}
		if input.Status != "" {
			req.Lifecycles = []session.Lifecycle{session.Lifecycle(input.Status)}
		}

		res, err := client.List(ctx, req)
		if err != nil {
			return NewErrorResult(fmt.Sprintf("failed to list sessions: %v", err)), nil
		}

		out := make([]SessionSummaryDTO, 0, len(res.Snapshot.Sessions))
		for _, row := range res.Snapshot.Sessions {
			out = append(out, toSessionSummaryDTO(row))
		}
		return NewJSONResult(out)
	})
}

func registerGetSession(s *Server, client ControlPlaneClient) {
	s.RegisterTool(Tool{
		Name:        "get_session",
		Description: "Get complete metadata snapshot for a specific session by full ID or unambiguous prefix.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"session_id": {
					Type:        "string",
					Description: "Full session ID or prefix",
				},
			},
			Required: []string{"session_id"},
		},
		Annotations: &ToolAnnotation{ReadOnly: true},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		var input struct {
			SessionID string `json:"session_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil || input.SessionID == "" {
			return NewErrorResult("get_session requires 'session_id'"), nil
		}

		res, err := client.Get(ctx, app.GetRequest{Ref: input.SessionID})
		if err != nil {
			return NewErrorResult(fmt.Sprintf("failed to get session %q: %v", input.SessionID, err)), nil
		}

		return NewJSONResult(res.Session)
	})
}

func registerFindSession(s *Server, client ControlPlaneClient) {
	s.RegisterTool(Tool{
		Name:        "find_session",
		Description: "Find sessions matching query criteria such as agent ID or lifecycle status.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"agent": {
					Type:        "string",
					Description: "Agent ID to search for",
				},
				"status": {
					Type:        "string",
					Description: "Session lifecycle status ('RUNNING', 'STOPPED', etc.)",
				},
			},
		},
		Annotations: &ToolAnnotation{ReadOnly: true},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		var input struct {
			Agent  string `json:"agent"`
			Status string `json:"status"`
		}
		if len(args) > 0 {
			_ = json.Unmarshal(args, &input)
		}

		req := app.ListRequest{}
		if input.Agent != "" {
			req.AgentID = agent.ID(input.Agent)
		}
		if input.Status != "" {
			req.Lifecycles = []session.Lifecycle{session.Lifecycle(input.Status)}
		}

		res, err := client.List(ctx, req)
		if err != nil {
			return NewErrorResult(fmt.Sprintf("failed to query sessions: %v", err)), nil
		}

		out := make([]SessionSummaryDTO, 0, len(res.Snapshot.Sessions))
		for _, row := range res.Snapshot.Sessions {
			out = append(out, toSessionSummaryDTO(row))
		}
		return NewJSONResult(out)
	})
}
