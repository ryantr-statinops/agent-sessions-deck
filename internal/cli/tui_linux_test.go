//go:build linux

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
)

func TestRootNonTTYRunsHeadlessOwner(t *testing.T) {
	tempDir := t.TempDir()
	paths := config.Paths{
		ConfigDir:  tempDir,
		StateDir:   tempDir,
		RuntimeDir: tempDir,
	}

	var stdout, stderr bytes.Buffer
	var stdin bytes.Buffer

	opts := Options{
		Paths:    paths,
		Config:   config.Default(),
		Stdin:    &stdin,
		Stdout:   &stdout,
		Stderr:   &stderr,
		InputFD:  0,
		OutputFD: 0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	root := NewRoot(opts)
	root.SetArgs([]string{})
	execErr := root.ExecuteContext(ctx)
	if execErr != nil {
		t.Logf("execute err: %v", execErr)
	}

	output := stderr.String()
	if !strings.Contains(output, "foreground owner") {
		t.Fatalf("expected non-TTY root to run headless owner message, got:\n%s", output)
	}
}
