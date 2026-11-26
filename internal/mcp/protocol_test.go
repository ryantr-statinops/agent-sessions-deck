package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestProtocolMessageClassification(t *testing.T) {
	req := Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Method:  "test/method",
	}
	if !req.IsRequest() || req.IsNotification() || req.IsResponse() {
		t.Fatalf("expected request classification: %+v", req)
	}

	notif := Message{
		JSONRPC: "2.0",
		Method:  "notifications/test",
	}
	if notif.IsRequest() || !notif.IsNotification() || notif.IsResponse() {
		t.Fatalf("expected notification classification: %+v", notif)
	}

	resp := Message{
		JSONRPC: "2.0",
		ID:      json.RawMessage("1"),
		Result:  json.RawMessage(`{"status":"ok"}`),
	}
	if resp.IsRequest() || resp.IsNotification() || !resp.IsResponse() {
		t.Fatalf("expected response classification: %+v", resp)
	}
}

func TestReaderAndWriterRoundtrip(t *testing.T) {
	var buf bytes.Buffer

	req, err := NewRequest(json.RawMessage(`"req-1"`), "tools/call", map[string]any{"name": "list_sessions"})
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if err := WriteMessage(&buf, req); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	resp, err := NewResponse(json.RawMessage(`"req-1"`), map[string]any{"content": []string{"session-1"}})
	if err != nil {
		t.Fatalf("NewResponse: %v", err)
	}
	if err := WriteMessage(&buf, resp); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	errResp := NewErrorResponse(json.RawMessage(`"req-2"`), CodeInvalidParams, "bad params", map[string]any{"detail": "missing key"})
	if err := WriteMessage(&buf, errResp); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	reader := NewReader(&buf)

	msg1, err := reader.ReadMessage()
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	if !msg1.IsRequest() || msg1.Method != "tools/call" || string(msg1.ID) != `"req-1"` {
		t.Fatalf("msg 1 mismatch: %+v", msg1)
	}

	msg2, err := reader.ReadMessage()
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if !msg2.IsResponse() || string(msg2.ID) != `"req-1"` {
		t.Fatalf("msg 2 mismatch: %+v", msg2)
	}

	msg3, err := reader.ReadMessage()
	if err != nil {
		t.Fatalf("read 3: %v", err)
	}
	if msg3.Error == nil || msg3.Error.Code != CodeInvalidParams {
		t.Fatalf("msg 3 error mismatch: %+v", msg3)
	}

	// Next read should return io.EOF
	_, err = reader.ReadMessage()
	if err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestReaderRejectsMalformedJSON(t *testing.T) {
	buf := bytes.NewBufferString("{ invalid json }\n")
	reader := NewReader(buf)
	_, err := reader.ReadMessage()
	if err == nil || !IsParseError(err) {
		t.Fatalf("expected parse error, got %v", err)
	}
}

func TestReaderRejectsInvalidVersion(t *testing.T) {
	buf := bytes.NewBufferString(`{"jsonrpc":"1.0","id":1,"method":"ping"}` + "\n")
	reader := NewReader(buf)
	_, err := reader.ReadMessage()
	if err == nil || !strings.Contains(err.Error(), "invalid jsonrpc version") {
		t.Fatalf("expected version error, got %v", err)
	}
}
