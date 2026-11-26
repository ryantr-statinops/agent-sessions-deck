package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestToolStartSession(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{
		createResult: app.CreateResult{
			Session: session.SessionSnapshot{
				ID:         session.ID("sess-new-1"),
				AgentID:    agent.ID("opencode"),
				Lifecycle:  session.LifecycleRunning,
				Generation: 1,
			},
		},
	}

	ro := false
	policy, _ := NewPolicyEngine(config.MCP{
		Enabled:           true,
		ReadOnly:          &ro,
		AllowedWorkspaces: []string{"/work"},
	})
	idempotency := NewIdempotencyManager(0)
	var auditBuf bytes.Buffer
	audit := NewAuditLogger(&auditBuf)

	srv := NewServer(ServerOptions{
		Policy:      policy,
		Idempotency: idempotency,
		Audit:       audit,
	})
	RegisterMutationTools(srv, fakeClient)

	ctx := context.Background()

	// 1. Success launch
	args, _ := json.Marshal(map[string]any{
		"agent":      "opencode",
		"workspace":  "/work/project",
		"name":       "test-launch",
		"request_id": "req-launch-1",
	})
	req, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name:      "start_session",
		Arguments: args,
	})
	resp, _, err := srv.HandleMessage(ctx, req)
	if err != nil || resp.Error != nil {
		t.Fatalf("start_session failed: %v", err)
	}

	var res CallToolResult
	_ = json.Unmarshal(resp.Result, &res)
	if res.IsError || !strings.Contains(res.Content[0].Text, "sess-new-1") {
		t.Fatalf("unexpected start_session result: %+v", res)
	}

	// Verify idempotency cache
	cached, ok := idempotency.Get("req-launch-1")
	if !ok || !strings.Contains(cached.Content[0].Text, "sess-new-1") {
		t.Fatalf("expected cached result in idempotency manager")
	}

	// Verify audit log
	auditOutput := auditBuf.String()
	if !strings.Contains(auditOutput, `"tool":"start_session"`) || !strings.Contains(auditOutput, `"status":"SUCCESS"`) {
		t.Fatalf("audit log missing success entry: %s", auditOutput)
	}
}

func TestToolStartSessionRejectionWhenReadOnly(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{}
	ro := true
	policy, _ := NewPolicyEngine(config.MCP{
		Enabled:  true,
		ReadOnly: &ro,
	})

	srv := NewServer(ServerOptions{
		Policy: policy,
	})
	RegisterMutationTools(srv, fakeClient)

	ctx := context.Background()
	args, _ := json.Marshal(map[string]any{
		"agent":     "opencode",
		"workspace": "/work/project",
	})
	req, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name:      "start_session",
		Arguments: args,
	})
	resp, _, err := srv.HandleMessage(ctx, req)
	if err != nil {
		t.Fatalf("unexpected rpc error: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != CodeApprovalRequired {
		t.Fatalf("expected CodeApprovalRequired, got: %+v", resp.Error)
	}
}

func TestToolStopAndRestartSession(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{
		stopResult: app.StopResult{
			Stopped: true,
		},
		restartResult: app.RestartResult{
			Session: session.SessionSnapshot{
				ID:         session.ID("sess-restarted"),
				Lifecycle:  session.LifecycleRunning,
				Generation: 3,
			},
			PreviousGeneration: 2,
		},
	}

	ro := false
	policy, _ := NewPolicyEngine(config.MCP{
		Enabled:  true,
		ReadOnly: &ro,
	})
	srv := NewServer(ServerOptions{
		Policy: policy,
	})
	RegisterMutationTools(srv, fakeClient)

	ctx := context.Background()

	// 1. stop_session
	stopArgs, _ := json.Marshal(map[string]any{
		"session_id": "sess-target",
		"grace":      "3s",
		"request_id": "req-stop-1",
	})
	reqStop, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name:      "stop_session",
		Arguments: stopArgs,
	})
	respStop, _, err := srv.HandleMessage(ctx, reqStop)
	if err != nil || respStop.Error != nil {
		t.Fatalf("stop_session failed: %v", err)
	}
	var resStop CallToolResult
	_ = json.Unmarshal(respStop.Result, &resStop)
	if !strings.Contains(resStop.Content[0].Text, `"stopped": true`) {
		t.Fatalf("unexpected stop result: %+v", resStop)
	}

	// 2. restart_session
	restartArgs, _ := json.Marshal(map[string]any{
		"session_id":        "sess-target",
		"target_generation": 2,
		"force":             true,
		"request_id":        "req-restart-1",
	})
	reqRestart, _ := NewRequest(json.RawMessage("2"), "tools/call", CallToolParams{
		Name:      "restart_session",
		Arguments: restartArgs,
	})
	respRestart, _, err := srv.HandleMessage(ctx, reqRestart)
	if err != nil || respRestart.Error != nil {
		t.Fatalf("restart_session failed: %v", err)
	}
	var resRestart CallToolResult
	_ = json.Unmarshal(respRestart.Result, &resRestart)
	if !strings.Contains(resRestart.Content[0].Text, "sess-restarted") || !strings.Contains(resRestart.Content[0].Text, `"generation": 3`) {
		t.Fatalf("unexpected restart result: %+v", resRestart)
	}
}

func TestToolInteractSession(t *testing.T) {
	sub := &fakeTerminalSubscription{
		sessionID:  session.ID("sess-interact"),
		generation: 1,
		snapshot: app.TerminalSnapshot{
			Sequence: 10,
			Screen: app.TerminalScreen{
				Columns: 80,
				Rows:    24,
				Lines: []app.TerminalLine{
					{
						Index: 0,
						Cells: []app.TerminalCell{
							{Text: "H"}, {Text: "e"}, {Text: "l"}, {Text: "l"}, {Text: "o"},
						},
					},
				},
			},
		},
	}

	fakeClient := &fakeControlPlaneClient{
		openResult: app.OpenResult{
			Session: session.SessionSnapshot{
				ID:         session.ID("sess-interact"),
				Lifecycle:  session.LifecycleRunning,
				Generation: 1,
			},
			Terminal: sub,
		},
	}

	ro := false
	policy, _ := NewPolicyEngine(config.MCP{
		Enabled:  true,
		ReadOnly: &ro,
	})
	srv := NewServer(ServerOptions{
		Policy: policy,
	})
	RegisterMutationTools(srv, fakeClient)

	ctx := context.Background()

	// 1. read_screen
	readArgs, _ := json.Marshal(map[string]any{
		"session_id": "sess-interact",
		"action":     "read_screen",
	})
	reqRead, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name:      "interact_session",
		Arguments: readArgs,
	})
	respRead, _, err := srv.HandleMessage(ctx, reqRead)
	if err != nil || respRead.Error != nil {
		t.Fatalf("read_screen failed: %v", err)
	}
	var resRead CallToolResult
	_ = json.Unmarshal(respRead.Result, &resRead)
	if !strings.Contains(resRead.Content[0].Text, "Hello") {
		t.Fatalf("expected 'Hello' in screen read, got: %+v", resRead)
	}

	// 2. write_input
	writeArgs, _ := json.Marshal(map[string]any{
		"session_id":          "sess-interact",
		"action":              "write_input",
		"input_text":          "echo hi\n",
		"expected_generation": 1,
	})
	reqWrite, _ := NewRequest(json.RawMessage("2"), "tools/call", CallToolParams{
		Name:      "interact_session",
		Arguments: writeArgs,
	})
	respWrite, _, err := srv.HandleMessage(ctx, reqWrite)
	if err != nil || respWrite.Error != nil {
		t.Fatalf("write_input failed: %v", err)
	}
	var resWrite CallToolResult
	_ = json.Unmarshal(respWrite.Result, &resWrite)
	if !strings.Contains(resWrite.Content[0].Text, "INPUT_WRITTEN") {
		t.Fatalf("expected INPUT_WRITTEN, got: %+v", resWrite)
	}
	if string(sub.written) != "echo hi\n" {
		t.Fatalf("expected written text 'echo hi\\n', got: %q", string(sub.written))
	}
}

func TestToolInteractSessionReadOnlyRejection(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{}
	ro := true
	policy, _ := NewPolicyEngine(config.MCP{
		Enabled:  true,
		ReadOnly: &ro,
	})
	srv := NewServer(ServerOptions{
		Policy: policy,
	})
	RegisterMutationTools(srv, fakeClient)

	ctx := context.Background()
	writeArgs, _ := json.Marshal(map[string]any{
		"session_id": "sess-interact",
		"action":     "write_input",
		"input_text": "ls -la\n",
	})
	req, _ := NewRequest(json.RawMessage("1"), "tools/call", CallToolParams{
		Name:      "interact_session",
		Arguments: writeArgs,
	})
	resp, _, err := srv.HandleMessage(ctx, req)
	if err != nil {
		t.Fatalf("unexpected rpc error: %v", err)
	}
	// Under read-only policy, interact_session tool has Destructive: true annotation, so server rejects it with CodeApprovalRequired!
	if resp.Error == nil || resp.Error.Code != CodeApprovalRequired {
		t.Fatalf("expected CodeApprovalRequired, got: %+v", resp.Error)
	}
}
