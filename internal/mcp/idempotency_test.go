package mcp

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestIdempotencyManager(t *testing.T) {
	mgr := NewIdempotencyManager(50 * time.Millisecond)

	reqID := "mut-req-123"
	res := NewTextResult("session created: s-123")

	// Initially not found
	if _, ok := mgr.Get(reqID); ok {
		t.Fatalf("expected not found initially")
	}

	// Record result
	mgr.Set(reqID, res)

	// Fetch result immediately
	cached, ok := mgr.Get(reqID)
	if !ok || len(cached.Content) == 0 || cached.Content[0].Text != "session created: s-123" {
		t.Fatalf("expected cached result, got: %+v", cached)
	}

	// Wait for TTL expiration
	time.Sleep(60 * time.Millisecond)
	if _, ok := mgr.Get(reqID); ok {
		t.Fatalf("expected expired entry")
	}
}

func TestAuditLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := NewAuditLogger(&buf)

	logger.Log(AuditEvent{
		Tool:      "start_session",
		RequestID: "req-1",
		SessionID: "s-1",
		Attempt:   1,
		Status:    "SUCCESS",
	})

	output := buf.String()
	if !strings.Contains(output, `"tool":"start_session"`) || !strings.Contains(output, `"status":"SUCCESS"`) {
		t.Fatalf("unexpected audit output: %q", output)
	}
}
