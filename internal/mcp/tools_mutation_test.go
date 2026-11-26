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
