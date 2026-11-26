package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

func TestToolListAgents(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{
		scanResult: app.ScanResult{
			Agents: sampleAgentProbes(),
		},
	}

	srv := NewServer(ServerOptions{})
	RegisterReadTools(srv, fakeClient)

	ctx := context.Background()

	// 1. list_agents without filter
	req, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name: "list_agents",
	})
	resp, _, err := srv.HandleMessage(ctx, req)
	if err != nil || resp.Error != nil {
		t.Fatalf("call failed: err=%v resp=%+v", err, resp)
	}

	var toolRes CallToolResult
	if err := json.Unmarshal(resp.Result, &toolRes); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(toolRes.Content) != 1 || !strings.Contains(toolRes.Content[0].Text, "opencode") {
		t.Fatalf("unexpected content: %+v", toolRes)
	}

	// 2. list_agents with agent_id filter
	args, _ := json.Marshal(map[string]any{"agent_id": "claude"})
	req2, _ := NewRequest(json.RawMessage("2"), "tools/call", CallToolParams{
		Name:      "list_agents",
		Arguments: args,
	})
	resp2, _, err := srv.HandleMessage(ctx, req2)
	if err != nil || resp2.Error != nil {
		t.Fatalf("call 2 failed: err=%v resp=%+v", err, resp2)
	}
	var toolRes2 CallToolResult
	if err := json.Unmarshal(resp2.Result, &toolRes2); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !strings.Contains(toolRes2.Content[0].Text, "claude") || strings.Contains(toolRes2.Content[0].Text, "opencode") {
		t.Fatalf("filter failed: %+v", toolRes2)
	}
}

func TestToolListWorkspaces(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{
		workspacesResult: app.WorkspacesResult{
			Workspaces: sampleWorkspaces(),
		},
	}

	srv := NewServer(ServerOptions{})
	RegisterReadTools(srv, fakeClient)

	ctx := context.Background()
	req, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name: "list_workspaces",
	})
	resp, _, err := srv.HandleMessage(ctx, req)
	if err != nil || resp.Error != nil {
		t.Fatalf("call failed: err=%v resp=%+v", err, resp)
	}

	var toolRes CallToolResult
	if err := json.Unmarshal(resp.Result, &toolRes); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(toolRes.Content) != 1 || !strings.Contains(toolRes.Content[0].Text, "project-a") {
		t.Fatalf("unexpected content: %+v", toolRes)
	}
}
