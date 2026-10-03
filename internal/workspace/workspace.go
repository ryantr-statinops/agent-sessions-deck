package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Git is repository metadata observed for a workspace.
//
// It is metadata only, never a replacement for Git (glossary, "Repository"),
// and Root may differ from Workspace.Path when the session runs in a repository
// subdirectory.
type Git struct {
	// Root is the canonical repository root.
	Root string
	// Branch is the checked-out branch, empty for a detached HEAD.
	Branch string
	// Detached reports a detached HEAD.
	Detached bool
	// Dirty reports uncommitted changes.
	Dirty bool
	// ChangedFiles counts changed records, not changed lines.
	ChangedFiles int
}

// Validate checks the invariants a resolver must satisfy before publishing Git
// metadata.
func (g Git) Validate() error {
	if g.Root == "" {
		return &InvalidGitError{Problem: "git root is empty"}
	}
	canonical, err := Canonical(g.Root)
	if err != nil {
		return &InvalidGitError{Root: g.Root, Problem: "git root is not a canonical absolute path"}
	}
	if canonical != g.Root {
		return &InvalidGitError{Root: g.Root, Problem: "git root is not in canonical form (" + canonical + ")"}
	}
	if g.ChangedFiles < 0 {
		return &InvalidGitError{Root: g.Root, Problem: "changed file count is negative"}
	}
	if !g.Detached && g.Branch == "" {
		// A named branch with no changes is normal; an empty branch without a
		// detached HEAD means the probe could not name the branch.
		return &InvalidGitError{Root: g.Root, Problem: "branch is empty for a non-detached HEAD"}
	}
	return nil
}

// String renders the Git metadata for diagnostics.
func (g Git) String() string {
	branch := g.Branch
	if g.Detached {
		branch = "detached"
	}
	if branch == "" {
		branch = "(unknown)"
	}
	state := "clean"
	if g.Dirty {
		state = fmt.Sprintf("%d changed", g.ChangedFiles)
	}
	return fmt.Sprintf("%s @ %s (%s)", g.Root, branch, state)
}

// InvalidGitError reports Git metadata that failed validation.
// The session layer maps it to the INVALID_CONFIGURATION code.
type InvalidGitError struct {
	Root    string
	Problem string
}

func (e *InvalidGitError) Error() string {
	if e.Root == "" {
		return "invalid git metadata: " + e.Problem
	}
	return fmt.Sprintf("invalid git metadata for %q: %s", e.Root, e.Problem)
}

// Workspace is a resolved directory plus optional repository metadata.
//
// The fields are unexported so a stored Workspace is immutable: the resolver
// publishes one value and every later reader shares it safely.
type Workspace struct {
	id   ID
	path string
	git  *Git
}

// New canonicalizes an absolute directory path into a workspace identity.
func New(path string) (Workspace, error) {
	canonical, err := Canonical(path)
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{id: ID(canonical), path: canonical}, nil
}

// ID returns the workspace identity.
func (w Workspace) ID() ID { return w.id }

// Path returns the canonical directory the agent runs in.
func (w Workspace) Path() string { return w.path }

// HasGit reports whether repository metadata is attached.
func (w Workspace) HasGit() bool { return w.git != nil }

// Git returns a copy of the repository metadata, if present.
func (w Workspace) Git() (Git, bool) {
	if w.git == nil {
		return Git{}, false
	}
	return *w.git, true
}

// WithGit returns a copy carrying validated repository metadata. The repository
// root does not have to equal the workspace path.
func (w Workspace) WithGit(g Git) (Workspace, error) {
	if err := g.Validate(); err != nil {
		return Workspace{}, err
	}
	git := g
	return Workspace{id: w.id, path: w.path, git: &git}, nil
}

// DisplayName returns the directory base name, falling back to the path itself
// for a filesystem root.
func (w Workspace) DisplayName() string {
	if w.path == "" {
		return ""
	}
	base := filepath.Base(w.path)
	if base == "/" || base == "." {
		return w.path
	}
	return base
}

// Equal reports whether two workspaces resolve to the same directory.
func (w Workspace) Equal(other Workspace) bool { return w.id == other.id }

// String renders the workspace for diagnostics.
func (w Workspace) String() string {
	var b strings.Builder
	b.WriteString(w.path)
	if w.git != nil {
		b.WriteString(" (")
		b.WriteString(w.git.String())
		b.WriteString(")")
	}
	return b.String()
}
