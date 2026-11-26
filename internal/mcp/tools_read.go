package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

// RegisterReadTools registers all Stage 14 read-only tools on the MCP server.
func RegisterReadTools(s *Server, client ControlPlaneClient) {
	registerListAgents(s, client)
	registerListWorkspaces(s, client)
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
