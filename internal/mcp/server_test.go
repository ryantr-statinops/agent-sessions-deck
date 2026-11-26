package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

func TestServerHandshakeAndPing(t *testing.T) {
	srv := NewServer(ServerOptions{
		Name:    "test-asd",
		Version: "1.0.0",
	})

	ctx := context.Background()

	// 1. Initialize
	initReq, _ := NewRequest(json.RawMessage("1"), "initialize", InitializeParams{
		ProtocolVersion: ProtocolVersion,
		ClientInfo: ClientInfo{
			Name:    "claude-test",
			Version: "0.1",
		},
	})
	resp, hasResp, err := srv.HandleMessage(ctx, initReq)
	if err != nil || !hasResp {
		t.Fatalf("HandleMessage initialize: err=%v hasResp=%v", err, hasResp)
	}
	if resp.Error != nil {
		t.Fatalf("initialize error: %v", resp.Error)
	}

	var initRes InitializeResult
	if err := json.Unmarshal(resp.Result, &initRes); err != nil {
		t.Fatalf("unmarshal init result: %v", err)
	}
	if initRes.ProtocolVersion != ProtocolVersion || initRes.ServerInfo.Name != "test-asd" {
		t.Fatalf("unexpected init response: %+v", initRes)
	}

	// 2. Notification initialized
	notif, _ := NewNotification("notifications/initialized", nil)
	_, hasResp, err = srv.HandleMessage(ctx, notif)
	if err != nil || hasResp {
		t.Fatalf("notification should have no response: err=%v hasResp=%v", err, hasResp)
	}
	if !srv.IsInitialized() {
		t.Fatalf("expected srv.IsInitialized() to be true")
	}

	// 3. Ping
	pingReq, _ := NewRequest(json.RawMessage("2"), "ping", nil)
	resp, hasResp, err = srv.HandleMessage(ctx, pingReq)
	if err != nil || !hasResp || resp.Error != nil {
		t.Fatalf("ping failed: err=%v resp=%+v", err, resp)
	}
}

func TestServerUnknownMethod(t *testing.T) {
	srv := NewServer(ServerOptions{})
	ctx := context.Background()

	req, _ := NewRequest(json.RawMessage("10"), "non_existent_method", nil)
	resp, hasResp, err := srv.HandleMessage(ctx, req)
	if err != nil || !hasResp {
		t.Fatalf("expected error response")
	}
	if resp.Error == nil || resp.Error.Code != CodeMethodNotFound {
		t.Fatalf("expected CodeMethodNotFound, got: %+v", resp.Error)
	}
}

func TestServerServeLoop(t *testing.T) {
	srv := NewServer(ServerOptions{})

	inBuf := bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	var outBuf bytes.Buffer

	ctx := context.Background()
	if err := srv.Serve(ctx, inBuf, &outBuf); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	reader := NewReader(&outBuf)
	msg, err := reader.ReadMessage()
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if !msg.IsResponse() || string(msg.ID) != "1" {
		t.Fatalf("unexpected response: %+v", msg)
	}
}
