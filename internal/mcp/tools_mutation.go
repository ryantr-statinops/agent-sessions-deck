package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// RegisterMutationTools registers all Stage 14 controlled mutation tools on the MCP server.
func RegisterMutationTools(s *Server, client ControlPlaneClient) {
	registerStartSession(s, client)
	registerStopSession(s, client)
	registerRestartSession(s, client)
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

func registerStopSession(s *Server, client ControlPlaneClient) {
	s.RegisterTool(Tool{
		Name:        "stop_session",
		Description: "Stop an active coding-agent session gracefully with generation verification and timeout.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"session_id": {
					Type:        "string",
					Description: "Full session ID or unambiguous prefix",
				},
				"grace": {
					Type:        "string",
					Description: "Grace duration to wait before force killing (e.g. '5s')",
				},
				"request_id": {
					Type:        "string",
					Description: "Optional idempotency token",
				},
			},
			Required: []string{"session_id"},
		},
		Annotations: &ToolAnnotation{
			ReadOnly:    false,
			Destructive: true,
		},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		var input struct {
			SessionID string `json:"session_id"`
			Grace     string `json:"grace"`
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil || input.SessionID == "" {
			return NewErrorResult("stop_session requires 'session_id'"), nil
		}

		if s.Idempotency() != nil && input.RequestID != "" {
			if cached, ok := s.Idempotency().Get(input.RequestID); ok {
				return cached, nil
			}
		}

		var grace time.Duration
		if input.Grace != "" {
			d, err := time.ParseDuration(input.Grace)
			if err != nil {
				return NewErrorResult(fmt.Sprintf("invalid grace duration %q: %v", input.Grace, err)), nil
			}
			grace = d
		}

		res, err := client.Stop(ctx, app.StopRequest{
			Ref:   input.SessionID,
			Grace: grace,
		})
		if err != nil {
			if s.Audit() != nil {
				s.Audit().Log(AuditEvent{
					Tool:      "stop_session",
					RequestID: input.RequestID,
					SessionID: input.SessionID,
					Status:    "ERROR",
					Error:     err.Error(),
				})
			}
			return NewErrorResult(fmt.Sprintf("failed to stop session: %v", err)), nil
		}

		type StopReport struct {
			SessionID string `json:"session_id"`
			Stopped   bool   `json:"stopped"`
			TimedOut  bool   `json:"timed_out"`
		}
		report := StopReport{
			SessionID: input.SessionID,
			Stopped:   res.Stopped,
			TimedOut:  res.TimedOut,
		}

		toolRes, err := NewJSONResult(report)
		if err != nil {
			return NewErrorResult(fmt.Sprintf("failed to format stop result: %v", err)), nil
		}

		if s.Idempotency() != nil && input.RequestID != "" {
			s.Idempotency().Set(input.RequestID, toolRes)
		}
		if s.Audit() != nil {
			s.Audit().Log(AuditEvent{
				Tool:      "stop_session",
				RequestID: input.RequestID,
				SessionID: input.SessionID,
				Status:    "SUCCESS",
			})
		}
		return toolRes, nil
	})
}

func registerRestartSession(s *Server, client ControlPlaneClient) {
	s.RegisterTool(Tool{
		Name:        "restart_session",
		Description: "Restart an existing coding-agent session with target generation guard.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"session_id": {
					Type:        "string",
					Description: "Full session ID or unambiguous prefix",
				},
				"force": {
					Type:        "boolean",
					Description: "Force restart even if session attempt is currently running",
				},
				"target_generation": {
					Type:        "integer",
					Description: "Expected generation to restart (prevents duplicate restarts)",
				},
				"request_id": {
					Type:        "string",
					Description: "Optional idempotency token",
				},
			},
			Required: []string{"session_id"},
		},
		Annotations: &ToolAnnotation{
			ReadOnly:    false,
			Destructive: true,
		},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		var input struct {
			SessionID        string `json:"session_id"`
			Force            bool   `json:"force"`
			TargetGeneration int    `json:"target_generation"`
			RequestID        string `json:"request_id"`
		}
		if err := json.Unmarshal(args, &input); err != nil || input.SessionID == "" {
			return NewErrorResult("restart_session requires 'session_id'"), nil
		}

		if s.Idempotency() != nil && input.RequestID != "" {
			if cached, ok := s.Idempotency().Get(input.RequestID); ok {
				return cached, nil
			}
		}

		res, err := client.Restart(ctx, app.RestartRequest{
			Ref:              input.SessionID,
			Force:            input.Force,
			TargetGeneration: session.Generation(input.TargetGeneration),
		})
		if err != nil {
			if s.Audit() != nil {
				s.Audit().Log(AuditEvent{
					Tool:      "restart_session",
					RequestID: input.RequestID,
					SessionID: input.SessionID,
					Status:    "ERROR",
					Error:     err.Error(),
				})
			}
			return NewErrorResult(fmt.Sprintf("failed to restart session: %v", err)), nil
		}

		type RestartReport struct {
			SessionID          string `json:"session_id"`
			Generation         int    `json:"generation"`
			PreviousGeneration int    `json:"previous_generation"`
			Lifecycle          string `json:"lifecycle"`
		}
		report := RestartReport{
			SessionID:          res.Session.ID.String(),
			Generation:         int(res.Session.Generation),
			PreviousGeneration: int(res.PreviousGeneration),
			Lifecycle:          res.Session.Lifecycle.String(),
		}

		toolRes, err := NewJSONResult(report)
		if err != nil {
			return NewErrorResult(fmt.Sprintf("failed to format restart result: %v", err)), nil
		}

		if s.Idempotency() != nil && input.RequestID != "" {
			s.Idempotency().Set(input.RequestID, toolRes)
		}
		if s.Audit() != nil {
			s.Audit().Log(AuditEvent{
				Tool:      "restart_session",
				RequestID: input.RequestID,
				SessionID: res.Session.ID.String(),
				Attempt:   int(res.Session.Generation),
				Status:    "SUCCESS",
			})
		}
		return toolRes, nil
	})
}
