package workspace

import (
	"testing"
	"time"
)

func TestSortSummaries(t *testing.T) {
	wsA, _ := New("/home/alice/a")
	wsB, _ := New("/home/alice/b")
	wsC, _ := New("/home/alice/c")

	tOld := time.Now().UTC().Add(-1 * time.Hour)
	tNew := time.Now().UTC()

	// wsA: inactive, newer
	// wsB: active, older
	// wsC: active, newer
	summaries := []WorkspaceSummary{
		{Workspace: wsA, ActiveCount: 0, TotalCount: 1, LastObservedAt: tNew},
		{Workspace: wsB, ActiveCount: 1, TotalCount: 2, LastObservedAt: tOld},
		{Workspace: wsC, ActiveCount: 2, TotalCount: 3, LastObservedAt: tNew},
	}

	SortSummaries(summaries)

	// Expected order:
	// 1. wsC (active, tNew)
	// 2. wsB (active, tOld)
	// 3. wsA (inactive, tNew)
	if summaries[0].Workspace.Path() != "/home/alice/c" {
		t.Fatalf("expected wsC first, got %s", summaries[0].Workspace.Path())
	}
	if summaries[1].Workspace.Path() != "/home/alice/b" {
		t.Fatalf("expected wsB second, got %s", summaries[1].Workspace.Path())
	}
	if summaries[2].Workspace.Path() != "/home/alice/a" {
		t.Fatalf("expected wsA third, got %s", summaries[2].Workspace.Path())
	}
}

func TestGitRootDistinction(t *testing.T) {
	rootWS, _ := New("/home/alice/repo")
	rootWS, _ = rootWS.WithGit(Git{Root: "/home/alice/repo", Branch: "main"})

	subWS, _ := New("/home/alice/repo/backend")
	subWS, _ = subWS.WithGit(Git{Root: "/home/alice/repo", Branch: "main"})

	plainWS, _ := New("/home/alice/plain")

	if IsSubdirectoryOfGitRoot(rootWS) {
		t.Fatalf("expected rootWS NOT to be subdirectory of git root")
	}
	if !IsSubdirectoryOfGitRoot(subWS) {
		t.Fatalf("expected subWS to be subdirectory of git root")
	}
	if IsSubdirectoryOfGitRoot(plainWS) {
		t.Fatalf("expected plainWS NOT to be subdirectory of git root")
	}

	if rel := RepoRelativePath(rootWS); rel != "." {
		t.Fatalf("expected rel '.' for rootWS, got %q", rel)
	}
	if rel := RepoRelativePath(subWS); rel != "backend" {
		t.Fatalf("expected rel 'backend' for subWS, got %q", rel)
	}
	if rel := RepoRelativePath(plainWS); rel != "." {
		t.Fatalf("expected rel '.' for plainWS, got %q", rel)
	}
}
