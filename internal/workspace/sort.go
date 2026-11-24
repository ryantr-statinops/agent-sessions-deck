package workspace

import (
	"path/filepath"
	"sort"
	"strings"
)

// SortSummaries sorts workspace summaries:
// 1. Summaries with active sessions (ActiveCount > 0) come first.
// 2. More recently observed workspaces (LastObservedAt descending) come earlier.
// 3. Alphabetical ordering by Workspace path as tie-breaker.
func SortSummaries(summaries []WorkspaceSummary) {
	sort.SliceStable(summaries, func(i, j int) bool {
		a, b := summaries[i], summaries[j]
		aActive := a.ActiveCount > 0
		bActive := b.ActiveCount > 0

		if aActive != bActive {
			return aActive
		}

		if !a.LastObservedAt.Equal(b.LastObservedAt) {
			return a.LastObservedAt.After(b.LastObservedAt)
		}

		return a.Workspace.Path() < b.Workspace.Path()
	})
}

// IsSubdirectoryOfGitRoot reports whether the workspace path is a subdirectory
// within its Git repository root, rather than the repository root itself.
func IsSubdirectoryOfGitRoot(ws Workspace) bool {
	g, ok := ws.Git()
	if !ok || g.Root == "" {
		return false
	}
	canonicalPath := filepath.Clean(ws.Path())
	canonicalRoot := filepath.Clean(g.Root)
	return canonicalPath != canonicalRoot && strings.HasPrefix(canonicalPath, canonicalRoot+string(filepath.Separator))
}

// RepoRelativePath returns the relative path from the Git repository root to the
// workspace directory. Returns "." if the workspace is the repository root or
// if Git metadata is absent.
func RepoRelativePath(ws Workspace) string {
	g, ok := ws.Git()
	if !ok || g.Root == "" {
		return "."
	}
	rel, err := filepath.Rel(g.Root, ws.Path())
	if err != nil {
		return "."
	}
	return rel
}
