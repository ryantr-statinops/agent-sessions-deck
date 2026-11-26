package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
)

func TestPolicyReadOnlyRejection(t *testing.T) {
	ro := true
	cfg := config.MCP{
		Enabled:  true,
		ReadOnly: &ro,
	}
	policy, err := NewPolicyEngine(cfg)
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	readOnlyTool := &ToolAnnotation{ReadOnly: true, Destructive: false}
	mutationTool := &ToolAnnotation{ReadOnly: false, Destructive: true}

	if err := policy.CheckToolExecution("get_session", readOnlyTool); err != nil {
		t.Fatalf("read-only tool should be allowed: %v", err)
	}

	err = policy.CheckToolExecution("stop_session", mutationTool)
	if err == nil || !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("mutation tool should be rejected with ErrApprovalRequired, got %v", err)
	}
}

func TestPolicyToolAllowlist(t *testing.T) {
	ro := false
	cfg := config.MCP{
		Enabled:      true,
		ReadOnly:     &ro,
		AllowedTools: []string{"list_sessions", "get_session"},
	}
	policy, err := NewPolicyEngine(cfg)
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	if err := policy.CheckToolExecution("list_sessions", nil); err != nil {
		t.Fatalf("allowed tool should pass: %v", err)
	}

	err = policy.CheckToolExecution("start_session", nil)
	if err == nil || !errors.Is(err, ErrForbidden) {
		t.Fatalf("unlisted tool should be forbidden, got: %v", err)
	}
}

func TestPolicyWorkspaceContainmentAndSymlink(t *testing.T) {
	tempDir := t.TempDir()
	allowedDir := filepath.Join(tempDir, "workspace")
	if err := os.MkdirAll(allowedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outsideDir := filepath.Join(tempDir, "outside")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create a subfolder inside allowedDir
	subDir := filepath.Join(allowedDir, "project", "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Create symlink pointing outside
	symlinkEscape := filepath.Join(allowedDir, "symlink_escape")
	if err := os.Symlink(outsideDir, symlinkEscape); err != nil {
		t.Fatal(err)
	}

	cfg := config.MCP{
		AllowedWorkspaces: []string{allowedDir},
	}
	policy, err := NewPolicyEngine(cfg)
	if err != nil {
		t.Fatalf("NewPolicyEngine: %v", err)
	}

	// 1. Direct allowed workspace
	if err := policy.CheckWorkspaceAllowed(allowedDir); err != nil {
		t.Fatalf("allowedDir should pass: %v", err)
	}

	// 2. Subdirectory inside allowed workspace
	if err := policy.CheckWorkspaceAllowed(subDir); err != nil {
		t.Fatalf("subDir should pass: %v", err)
	}

	// 3. Traversal escape
	traversal := filepath.Join(allowedDir, "..", "outside")
	if err := policy.CheckWorkspaceAllowed(traversal); err == nil {
		t.Fatalf("traversal escape should fail")
	}

	// 4. Outside directory
	if err := policy.CheckWorkspaceAllowed(outsideDir); err == nil {
		t.Fatalf("outsideDir should fail")
	}

	// 5. Symlink escape
	if err := policy.CheckWorkspaceAllowed(symlinkEscape); err == nil {
		t.Fatalf("symlink escape should fail")
	}
}
