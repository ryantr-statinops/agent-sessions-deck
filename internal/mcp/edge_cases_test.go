package mcp

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
)

func TestMalformedJSONRPCStreamRecovery(t *testing.T) {
	srv := NewServer(ServerOptions{})

	// Stream with invalid line followed by valid ping
	inputData := "{ invalid json }\n" + `{"jsonrpc":"2.0","id":100,"method":"ping"}` + "\n"
	in := bytes.NewBufferString(inputData)
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Serve(ctx, in, &out); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	reader := NewReader(&out)

	// First response should be parse error
	msg1, err := reader.ReadMessage()
	if err != nil {
		t.Fatalf("read msg1: %v", err)
	}
	if msg1.Error == nil || msg1.Error.Code != CodeParseError {
		t.Fatalf("expected CodeParseError, got: %+v", msg1)
	}

	// Second response should be success ping
	msg2, err := reader.ReadMessage()
	if err != nil {
		t.Fatalf("read msg2: %v", err)
	}
	if !msg2.IsResponse() || string(msg2.ID) != "100" {
		t.Fatalf("expected ping response, got: %+v", msg2)
	}
}

func TestServerContextCancellation(t *testing.T) {
	srv := NewServer(ServerOptions{})

	// Create blocked reader
	r, w := io.Pipe()
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ctx, r, io.Discard)
	}()

	// Cancel context after a small delay
	time.Sleep(50 * time.Millisecond)
	cancel()
	_ = r.Close()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatalf("expected non-nil error upon cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Serve did not exit upon context cancellation")
	}
}

func TestPolicySymlinkLoopHandling(t *testing.T) {
	tempDir := t.TempDir()
	loopA := filepath.Join(tempDir, "loopA")
	loopB := filepath.Join(tempDir, "loopB")

	_ = os.Symlink(loopB, loopA)
	_ = os.Symlink(loopA, loopB)

	cfg := config.MCP{
		AllowedWorkspaces: []string{tempDir},
	}
	policy, err := NewPolicyEngine(cfg)
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	// Check on a looping symlink path
	err = policy.CheckWorkspaceAllowed(loopA)
	// Must fail safely without infinite recursion
	if err == nil {
		t.Fatalf("expected error on circular symlink")
	}
}
