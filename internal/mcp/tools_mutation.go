package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// RegisterMutationTools registers all Stage 14 controlled mutation tools on the MCP server.
func RegisterMutationTools(s *Server, client ControlPlaneClient) {
	registerStartSession(s, client)
}

func registerStartSession(s *Server, client ControlPlaneClient) {
	s.RegisterTool(Tool{
		Name:        "start_session",
		Description: "Launch a new coding-agent session in a verified workspace directory using configured agent templates.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"agent": {
					Type:        "string",
					Description: "Declared agent ID (e.g. 'opencode', 'claude', 'codex')",
				},
				"workspace": {
					Type:        "string",
					Description: "Workspace directory path (must be within allowed workspaces)",
				},
				"name": {
					Type:        "string",
					Description: "Optional session display name",
				},
				"request_id": {
					Type:        "string",
					Description: "Optional idempotency token to prevent duplicate launches upon retry",
				},
			},
			Required: []string{"agent", "workspace"},
		},
		Annotations: &ToolAnnotation{
			ReadOnly:    false,
			Destructive: true,
		},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		var input struct {
			Agent     string `json:"agent"`
			Workspace string `json:"workspace"`
			Name      string `json:"name"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil {
			return NewErrorResult("malformed start_session arguments"), nil
		}
		if input.Agent == "" || input.Workspace == "" {
			return NewErrorResult("start_session requires both 'agent' and 'workspace'"), nil
		}

		// Idempotency check
		if s.Idempotency() != nil && input.RequestID != "" {
			if cached, ok := s.Idempotency().Get(input.RequestID); ok {
				return cached, nil
			}
		}

		// Security policy checks
		if s.Policy() != nil {
			if err := s.Policy().CheckAgentAllowed(input.Agent); err != nil {
				if s.Audit() != nil {
					s.Audit().Log(AuditEvent{
						Tool:      "start_session",
						RequestID: input.RequestID,
						Status:    "ERROR",
						Error:     err.Error(),
					})
				}
				return NewErrorResult(err.Error()), nil
			}
			if err := s.Policy().CheckWorkspaceAllowed(input.Workspace); err != nil {
				if s.Audit() != nil {
					s.Audit().Log(AuditEvent{
						Tool:      "start_session",
						RequestID: input.RequestID,
						Status:    "ERROR",
						Error:     err.Error(),
					})
				}
				return NewErrorResult(err.Error()), nil
			}
		}

		// Execute launch via ControlPlaneClient
		res, err := client.Create(ctx, app.CreateRequest{
			Agent:           agent.ID(input.Agent),
			WorkspacePath:   input.Workspace,
			WorkspaceSource: workspace.SourceExplicit,
			Name:            input.Name,
		})
		if err != nil {
			if s.Audit() != nil {
				s.Audit().Log(AuditEvent{
					Tool:      "start_session",
					RequestID: input.RequestID,
					Status:    "ERROR",
					Error:     err.Error(),
				})
			}
			return NewErrorResult(fmt.Sprintf("failed to start session: %v", err)), nil
		}

		type StartResult struct {
			SessionID  string `json:"session_id"`
			Generation int    `json:"generation"`
			Agent      string `json:"agent"`
			Workspace  string `json:"workspace"`
			Status     string `json:"status"`
		}
		resultDTO := StartResult{
			SessionID:  res.Session.ID.String(),
			Generation: int(res.Session.Generation),
			Agent:      string(res.Session.AgentID),
			Workspace:  input.Workspace,
			Status:     res.Session.Lifecycle.String(),
		}

		toolRes, err := NewJSONResult(resultDTO)
		if err != nil {
			return NewErrorResult(fmt.Sprintf("failed to format result: %v", err)), nil
		}

		// Save in idempotency manager and audit log
		if s.Idempotency() != nil && input.RequestID != "" {
			s.Idempotency().Set(input.RequestID, toolRes)
		}
		if s.Audit() != nil {
			s.Audit().Log(AuditEvent{
				Tool:      "start_session",
				RequestID: input.RequestID,
				SessionID: res.Session.ID.String(),
				Attempt:   int(res.Session.Generation),
				Status:    "SUCCESS",
			})
		}

		return toolRes, nil
	})
}
