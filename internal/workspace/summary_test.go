package workspace

import (
	"testing"
	"time"
)

func TestWorkspaceSummaryValidate(t *testing.T) {
	ws, err := New("/home/alice/project")
	if err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	git := &Git{
		Root:         "/home/alice/project",
		Branch:       "main",
		Dirty:        false,
		ChangedFiles: 0,
	}

	valid := WorkspaceSummary{
		Workspace:      ws,
		Git:            git,
		ActiveCount:    1,
		TotalCount:     2,
		SessionIDs:     []string{"sess-1", "sess-2"},
		LastObservedAt: time.Now().UTC(),
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("expected valid summary, got: %v", err)
	}

	// 1. Missing workspace ID
	invalidWS := valid
	invalidWS.Workspace = Workspace{}
	if err := invalidWS.Validate(); err == nil {
		t.Fatalf("expected error on empty workspace, got nil")
	}

	// 2. ActiveCount > TotalCount
	invalidCounts := valid
	invalidCounts.ActiveCount = 3
	invalidCounts.TotalCount = 2
	if err := invalidCounts.Validate(); err == nil {
		t.Fatalf("expected error when ActiveCount > TotalCount, got nil")
	}

	// 3. Negative count
	invalidNeg := valid
	invalidNeg.ActiveCount = -1
	if err := invalidNeg.Validate(); err == nil {
		t.Fatalf("expected error on negative ActiveCount, got nil")
	}

	// 4. SessionIDs length mismatch
	invalidSessions := valid
	invalidSessions.SessionIDs = []string{"sess-1"}
	if err := invalidSessions.Validate(); err == nil {
		t.Fatalf("expected error on SessionIDs length mismatch, got nil")
	}

	// 5. Invalid Git
	invalidGit := valid
	invalidGit.Git = &Git{Root: ""}
	if err := invalidGit.Validate(); err == nil {
		t.Fatalf("expected error on invalid Git metadata, got nil")
	}
}
