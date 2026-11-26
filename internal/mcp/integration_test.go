package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestMCPProtocolFullLifecycleIntegration(t *testing.T) {
	fakeClient := &fakeControlPlaneClient{
		listResult: app.ListResult{
			Snapshot: session.Snapshot{
				Sessions: []session.SessionSnapshot{
					{
						ID:         session.ID("sess-integ-1"),
						AgentID:    agent.ID("opencode"),
						Lifecycle:  session.LifecycleRunning,
						Generation: 1,
					},
				},
			},
		},
		eventsResult: app.EventsResult{
			NextRevision: 100,
			Events:       []events.Event{},
		},
	}

	ro := true
	policy, err := NewPolicyEngine(config.MCP{
		Enabled:  true,
		ReadOnly: &ro,
	})
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	srv := NewServer(ServerOptions{
		Name:    "agent-sessions-deck",
		Version: "v3-test",
		Policy:  policy,
	})
	RegisterReadTools(srv, fakeClient)
	RegisterMutationTools(srv, fakeClient)

	// Set up pipes for client <-> server communication
	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()

	serverDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		serverDone <- srv.Serve(ctx, serverIn, serverOut)
	}()

	clientReader := NewReader(clientIn)

	// Step 1: Send initialize request
	initReq, _ := NewRequest(json.RawMessage("1"), "initialize", InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo: ClientInfo{
			Name:    "claude-desktop",
			Version: "1.0",
		},
	})
	if err := WriteMessage(clientOut, initReq); err != nil {
		t.Fatalf("write initialize: %v", err)
	}

	initResp, err := clientReader.ReadMessage()
	if err != nil {
		t.Fatalf("read initialize response: %v", err)
	}
	if !initResp.IsResponse() || string(initResp.ID) != "1" || initResp.Error != nil {
		t.Fatalf("unexpected init response: %+v", initResp)
	}

	var initRes InitializeResult
	_ = json.Unmarshal(initResp.Result, &initRes)
	if initRes.ProtocolVersion != ProtocolVersion || initRes.ServerInfo.Name != "agent-sessions-deck" {
		t.Fatalf("unexpected serverInfo or protocol: %+v", initRes)
	}

	// Step 2: Send notifications/initialized
	notif, _ := NewNotification("notifications/initialized", nil)
	if err := WriteMessage(clientOut, notif); err != nil {
		t.Fatalf("write notification: %v", err)
	}

	// Step 3: Send tools/list request
	listReq, _ := NewRequest(json.RawMessage("2"), "tools/list", nil)
	if err := WriteMessage(clientOut, listReq); err != nil {
		t.Fatalf("write tools/list: %v", err)
	}

	listResp, err := clientReader.ReadMessage()
	if err != nil {
		t.Fatalf("read tools/list response: %v", err)
	}
	var listRes ListToolsResult
	_ = json.Unmarshal(listResp.Result, &listRes)
	if len(listRes.Tools) == 0 {
		t.Fatalf("expected non-empty tools list")
	}

	// Step 4: Call list_sessions
	callReq, _ := NewRequest(json.RawMessage("3"), "tools/call", CallToolParams{
		Name: "list_sessions",
	})
	if err := WriteMessage(clientOut, callReq); err != nil {
		t.Fatalf("write tools/call: %v", err)
	}

	callResp, err := clientReader.ReadMessage()
	if err != nil {
		t.Fatalf("read tools/call response: %v", err)
	}
	var callRes CallToolResult
	_ = json.Unmarshal(callResp.Result, &callRes)
	if callRes.IsError || len(callRes.Content) == 0 || !strings.Contains(callRes.Content[0].Text, "sess-integ-1") {
		t.Fatalf("unexpected call result: %+v", callRes)
	}

	// Step 5: Send ping
	pingReq, _ := NewRequest(json.RawMessage("4"), "ping", nil)
	if err := WriteMessage(clientOut, pingReq); err != nil {
		t.Fatalf("write ping: %v", err)
	}

	pingResp, err := clientReader.ReadMessage()
	if err != nil {
		t.Fatalf("read ping response: %v", err)
	}
	if !pingResp.IsResponse() || string(pingResp.ID) != "4" {
		t.Fatalf("unexpected ping response: %+v", pingResp)
	}

	// Step 6: Close clientOut (simulate EOF from client process exiting)
	_ = clientOut.Close()

	// Step 7: Wait for server to gracefully terminate
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server terminated with unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not terminate on EOF within timeout")
	}
}
