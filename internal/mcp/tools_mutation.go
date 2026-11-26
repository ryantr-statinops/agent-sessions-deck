package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
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
	registerInteractSession(s, client)
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

func registerInteractSession(s *Server, client ControlPlaneClient) {
	s.RegisterTool(Tool{
		Name:        "interact_session",
		Description: "Interact with an active session: read bounded screen snapshot or write input text with lease arbitration.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]PropertyDef{
				"session_id": {
					Type:        "string",
					Description: "Full session ID or unambiguous prefix",
				},
				"action": {
					Type:        "string",
					Description: "Action to perform: 'read_screen' or 'write_input'",
					Enum:        []string{"read_screen", "write_input"},
				},
				"input_text": {
					Type:        "string",
					Description: "Text or control sequence to write into the session (required for 'write_input')",
				},
				"expected_generation": {
					Type:        "integer",
					Description: "Optional attempt generation guard to avoid writing to restarted session",
				},
				"holder": {
					Type:        "string",
					Description: "Client holder identifier for lease claim (default 'mcp-client')",
				},
				"max_bytes": {
					Type:        "integer",
					Description: "Max screen content bytes to return (default from policy, e.g. 65536)",
				},
			},
			Required: []string{"session_id", "action"},
		},
		Annotations: &ToolAnnotation{
			ReadOnly:    false,
			Destructive: true,
		},
	}, func(ctx context.Context, args json.RawMessage) (CallToolResult, error) {
		var input struct {
			SessionID          string `json:"session_id"`
			Action             string `json:"action"`
			InputText          string `json:"input_text"`
			ExpectedGeneration int    `json:"expected_generation"`
			Holder             string `json:"holder"`
			MaxBytes           int    `json:"max_bytes"`
		}
		if err := json.Unmarshal(args, &input); err != nil || input.SessionID == "" {
			return NewErrorResult("interact_session requires 'session_id' and 'action'"), nil
		}

		if input.Holder == "" {
			input.Holder = "mcp-client"
		}
		maxBytes := 65536
		if s.Policy() != nil && s.Policy().MaxOutputBytes() > 0 {
			maxBytes = s.Policy().MaxOutputBytes()
		}
		if input.MaxBytes > 0 && input.MaxBytes < maxBytes {
			maxBytes = input.MaxBytes
		}

		switch input.Action {
		case "read_screen":
			// Read screen action is non-destructive
			openRes, err := client.Open(ctx, app.OpenRequest{
				Ref:    input.SessionID,
				Holder: input.Holder,
			})
			if err != nil {
				return NewErrorResult(fmt.Sprintf("failed to read screen for session %q: %v", input.SessionID, err)), nil
			}
			defer func() {
				if openRes.Terminal != nil {
					_ = openRes.Terminal.Close()
				}
			}()

			var snapshot app.TerminalSnapshot
			if openRes.Terminal != nil {
				snapshot = openRes.Terminal.InitialSnapshot()
			}

			screenText := screenToText(snapshot.Screen, maxBytes)
			type ScreenReport struct {
				SessionID  string `json:"session_id"`
				Generation int    `json:"generation"`
				Columns    int    `json:"columns"`
				Rows       int    `json:"rows"`
				CursorX    int    `json:"cursor_x"`
				CursorY    int    `json:"cursor_y"`
				Sequence   uint64 `json:"sequence"`
				Content    string `json:"content"`
			}
			report := ScreenReport{
				SessionID:  openRes.Session.ID.String(),
				Generation: int(openRes.Session.Generation),
				Columns:    snapshot.Screen.Columns,
				Rows:       snapshot.Screen.Rows,
				CursorX:    snapshot.Screen.Cursor.X,
				CursorY:    snapshot.Screen.Cursor.Y,
				Sequence:   snapshot.Sequence,
				Content:    screenText,
			}
			return NewJSONResult(report)

		case "write_input":
			// Write action is a mutation requiring grant
			if s.Policy() != nil && s.Policy().IsReadOnly() {
				return NewErrorResult("approval required: server is in read-only mode; cannot write input"), nil
			}
			if input.InputText == "" {
				return NewErrorResult("write_input requires non-empty 'input_text'"), nil
			}

			openRes, err := client.Open(ctx, app.OpenRequest{
				Ref:    input.SessionID,
				Holder: input.Holder,
			})
			if err != nil {
				if s.Audit() != nil {
					s.Audit().Log(AuditEvent{
						Tool:      "interact_session",
						SessionID: input.SessionID,
						Status:    "ERROR",
						Error:     err.Error(),
					})
				}
				return NewErrorResult(fmt.Sprintf("lease conflict or open failed: %v", err)), nil
			}
			defer func() {
				if openRes.Terminal != nil {
					_ = openRes.Terminal.Close()
				}
			}()

			if input.ExpectedGeneration > 0 && int(openRes.Session.Generation) != input.ExpectedGeneration {
				return NewErrorResult(fmt.Sprintf("attempt generation mismatch: expected %d, got %d", input.ExpectedGeneration, openRes.Session.Generation)), nil
			}

			if openRes.Terminal == nil {
				return NewErrorResult("session terminal subscription is unavailable"), nil
			}

			written, err := openRes.Terminal.Write([]byte(input.InputText))
			if err != nil {
				return NewErrorResult(fmt.Sprintf("failed to write input: %v", err)), nil
			}

			if s.Audit() != nil {
				s.Audit().Log(AuditEvent{
					Tool:      "interact_session",
					SessionID: openRes.Session.ID.String(),
					Attempt:   int(openRes.Session.Generation),
					Status:    "SUCCESS",
				})
			}

			type WriteReport struct {
				SessionID    string `json:"session_id"`
				Generation   int    `json:"generation"`
				BytesWritten int    `json:"bytes_written"`
				Status       string `json:"status"`
			}
			return NewJSONResult(WriteReport{
				SessionID:    openRes.Session.ID.String(),
				Generation:   int(openRes.Session.Generation),
				BytesWritten: written,
				Status:       "INPUT_WRITTEN",
			})

		default:
			return NewErrorResult(fmt.Sprintf("invalid action %q: must be 'read_screen' or 'write_input'", input.Action)), nil
		}
	})
}

func screenToText(screen app.TerminalScreen, maxBytes int) string {
	var sb strings.Builder
	for _, line := range screen.Lines {
		var lineText strings.Builder
		for _, cell := range line.Cells {
			if cell.Text != "" {
				lineText.WriteString(cell.Text)
			} else {
				lineText.WriteByte(' ')
			}
		}
		trimmed := strings.TrimRight(lineText.String(), " ")
		if sb.Len()+len(trimmed)+1 > maxBytes {
			remaining := maxBytes - sb.Len()
			if remaining > 0 && remaining <= len(trimmed) {
				sb.WriteString(trimmed[:remaining])
			}
			break
		}
		sb.WriteString(trimmed)
		sb.WriteByte('\n')
	}
	return sb.String()
}
