package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
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

func TestToolGetWorkspace(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{
		workspacesResult: app.WorkspacesResult{
			Workspaces: sampleWorkspaces(),
		},
	}

	srv := NewServer(ServerOptions{})
	RegisterReadTools(srv, fakeClient)

	ctx := context.Background()

	// 1. Success matching path
	args, _ := json.Marshal(map[string]any{"path": "/home/user/project-a"})
	req, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name:      "get_workspace",
		Arguments: args,
	})
	resp, _, err := srv.HandleMessage(ctx, req)
	if err != nil || resp.Error != nil {
		t.Fatalf("call failed: err=%v resp=%+v", err, resp)
	}
	var toolRes CallToolResult
	_ = json.Unmarshal(resp.Result, &toolRes)
	if len(toolRes.Content) == 0 || !strings.Contains(toolRes.Content[0].Text, "project-a") {
		t.Fatalf("unexpected content: %+v", toolRes)
	}

	// 2. Not found
	argsNotFound, _ := json.Marshal(map[string]any{"path": "/home/user/unknown"})
	reqNotFound, _ := NewRequest(json.RawMessage("2"), "tools/call", CallToolParams{
		Name:      "get_workspace",
		Arguments: argsNotFound,
	})
	respNotFound, _, err := srv.HandleMessage(ctx, reqNotFound)
	if err != nil || respNotFound.Error != nil {
		t.Fatalf("call failed: %v", err)
	}
	var toolResNotFound CallToolResult
	_ = json.Unmarshal(respNotFound.Result, &toolResNotFound)
	if !toolResNotFound.IsError || !strings.Contains(toolResNotFound.Content[0].Text, "not found") {
		t.Fatalf("expected error result: %+v", toolResNotFound)
	}
}

func TestToolGetProcess(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{
		getResult: app.GetResult{
			Session: session.SessionSnapshot{
				ID:          session.ID("sess-abc12345"),
				Lifecycle:   session.LifecycleRunning,
				Generation:  1,
				HasIdentity: true,
				Identity: session.ProcessIdentity{
					PID: 9876,
				},
			},
		},
	}

	srv := NewServer(ServerOptions{})
	RegisterReadTools(srv, fakeClient)

	ctx := context.Background()
	args, _ := json.Marshal(map[string]any{"session_id": "sess-abc12345"})
	req, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name:      "get_process",
		Arguments: args,
	})
	resp, _, err := srv.HandleMessage(ctx, req)
	if err != nil || resp.Error != nil {
		t.Fatalf("call failed: %v", err)
	}
	var toolRes CallToolResult
	_ = json.Unmarshal(resp.Result, &toolRes)
	if len(toolRes.Content) == 0 || !strings.Contains(toolRes.Content[0].Text, "9876") {
		t.Fatalf("expected PID in process report: %+v", toolRes)
	}
}

func TestToolSessionsOperations(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{
		listResult: app.ListResult{
			Snapshot: session.Snapshot{
				Sessions: []session.SessionSnapshot{
					{
						ID:          session.ID("sess-1111"),
						AgentID:     agent.ID("opencode"),
						Lifecycle:   session.LifecycleRunning,
						Generation:  1,
						HasIdentity: true,
						Identity: session.ProcessIdentity{
							PID: 1111,
						},
					},
					{
						ID:          session.ID("sess-2222"),
						AgentID:     agent.ID("claude"),
						Lifecycle:   session.LifecycleExited,
						Generation:  2,
						HasIdentity: true,
						Identity: session.ProcessIdentity{
							PID: 2222,
						},
					},
				},
			},
		},
		getResult: app.GetResult{
			Session: session.SessionSnapshot{
				ID:         session.ID("sess-1111"),
				AgentID:    agent.ID("opencode"),
				Lifecycle:  session.LifecycleRunning,
				Generation: 1,
			},
		},
	}

	srv := NewServer(ServerOptions{})
	RegisterReadTools(srv, fakeClient)

	ctx := context.Background()

	// 1. list_sessions
	reqList, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name: "list_sessions",
	})
	respList, _, err := srv.HandleMessage(ctx, reqList)
	if err != nil || respList.Error != nil {
		t.Fatalf("list_sessions failed: %v", err)
	}
	var resList CallToolResult
	_ = json.Unmarshal(respList.Result, &resList)
	if len(resList.Content) == 0 || !strings.Contains(resList.Content[0].Text, "sess-1111") || !strings.Contains(resList.Content[0].Text, "sess-2222") {
		t.Fatalf("unexpected list_sessions content: %+v", resList)
	}

	// 2. get_session
	argsGet, _ := json.Marshal(map[string]any{"session_id": "sess-1111"})
	reqGet, _ := NewRequest(json.RawMessage("2"), "tools/call", CallToolParams{
		Name:      "get_session",
		Arguments: argsGet,
	})
	respGet, _, err := srv.HandleMessage(ctx, reqGet)
	if err != nil || respGet.Error != nil {
		t.Fatalf("get_session failed: %v", err)
	}
	var resGet CallToolResult
	_ = json.Unmarshal(respGet.Result, &resGet)
	if len(resGet.Content) == 0 || !strings.Contains(resGet.Content[0].Text, "sess-1111") {
		t.Fatalf("unexpected get_session content: %+v", resGet)
	}

	// 3. find_session
	argsFind, _ := json.Marshal(map[string]any{"agent": "opencode"})
	reqFind, _ := NewRequest(json.RawMessage("3"), "tools/call", CallToolParams{
		Name:      "find_session",
		Arguments: argsFind,
	})
	respFind, _, err := srv.HandleMessage(ctx, reqFind)
	if err != nil || respFind.Error != nil {
		t.Fatalf("find_session failed: %v", err)
	}
	var resFind CallToolResult
	_ = json.Unmarshal(respFind.Result, &resFind)
	if len(resFind.Content) == 0 || !strings.Contains(resFind.Content[0].Text, "sess-1111") {
		t.Fatalf("unexpected find_session content: %+v", resFind)
	}
}

func TestToolGetRecentEvents(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{
		eventsResult: app.EventsResult{
			NextRevision: 42,
			Events:       []events.Event{},
		},
	}

	srv := NewServer(ServerOptions{})
	RegisterReadTools(srv, fakeClient)

	ctx := context.Background()
	args, _ := json.Marshal(map[string]any{
		"session_id":     "sess-1111",
		"after_revision": 10,
		"limit":          50,
	})
	req, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name:      "get_recent_events",
		Arguments: args,
	})
	resp, _, err := srv.HandleMessage(ctx, req)
	if err != nil || resp.Error != nil {
		t.Fatalf("get_recent_events failed: %v", err)
	}
	var res CallToolResult
	_ = json.Unmarshal(resp.Result, &res)
	if len(res.Content) == 0 || !strings.Contains(res.Content[0].Text, "next_revision") {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestReadToolsListContract(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{}
	srv := NewServer(ServerOptions{})
	RegisterReadTools(srv, fakeClient)

	ctx := context.Background()
	req, _ := NewRequest(json.RawMessage("1"), "tools/list", nil)
	resp, _, err := srv.HandleMessage(ctx, req)
	if err != nil || resp.Error != nil {
		t.Fatalf("tools/list failed: %v", err)
	}

	var listRes ListToolsResult
	if err := json.Unmarshal(resp.Result, &listRes); err != nil {
		t.Fatalf("unmarshal tools/list: %v", err)
	}

	expectedTools := []string{
		"list_agents",
		"list_workspaces",
		"get_workspace",
		"get_process",
		"list_sessions",
		"get_session",
		"find_session",
		"get_recent_events",
	}

	toolMap := make(map[string]Tool)
	for _, tool := range listRes.Tools {
		toolMap[tool.Name] = tool
		if tool.Annotations == nil || !tool.Annotations.ReadOnly {
			t.Fatalf("tool %q must have ReadOnly annotation", tool.Name)
		}
		if tool.InputSchema.Type != "object" {
			t.Fatalf("tool %q input schema type must be 'object'", tool.Name)
		}
	}

	for _, expected := range expectedTools {
		if _, ok := toolMap[expected]; !ok {
			t.Fatalf("missing expected tool: %q", expected)
		}
	}
}

func TestReadToolsErrorHandling(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{
		scanErr: context.DeadlineExceeded,
	}
	srv := NewServer(ServerOptions{})
	RegisterReadTools(srv, fakeClient)

	ctx := context.Background()
	req, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name: "list_agents",
	})
	resp, _, err := srv.HandleMessage(ctx, req)
	if err != nil || resp.Error != nil {
		t.Fatalf("call should return successful JSON-RPC message containing isError tool result: %v", err)
	}
	var res CallToolResult
	_ = json.Unmarshal(resp.Result, &res)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "context deadline exceeded") {
		t.Fatalf("expected error result: %+v", res)
	}
}
