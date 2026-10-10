package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GitProbe observes repository metadata for one canonical directory. It is
// optional and injected so the resolver stays independent of the git
// client's implementation.
type GitProbe func(ctx context.Context, dir string) (Git, error)

// PathResolver implements Resolver with explicit tilde expansion, cwd
// resolution, directory validation and a canonical symlink policy: the
// workspace identity is always the resolved real directory, and a session
// never has its cwd replaced by a repository root.
type PathResolver struct {
	// Home expands a leading "~" in candidates; empty refuses it.
	Home string
	// GitProbe attaches repository metadata when present; nil skips it.
	GitProbe GitProbe
}

// NewPathResolver builds a resolver with the given home directory and
// optional git probe.
func NewPathResolver(home string, git GitProbe) *PathResolver {
	return &PathResolver{Home: home, GitProbe: git}
}

// Resolve turns one candidate into a verified workspace.
func (r *PathResolver) Resolve(ctx context.Context, req ResolveRequest) (Workspace, error) {
	path, err := r.expand(req.Path())
	if err != nil {
		return Workspace{}, err
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err != nil {
			return Workspace{}, fmt.Errorf("workspace %q: cannot read cwd: %w", req.Path(), err)
		}
		path = filepath.Join(cwd, path)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return Workspace{}, &InvalidPathError{Path: req.Path(), Problem: fmt.Sprintf("cannot resolve: %v", err)}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Workspace{}, &InvalidPathError{Path: req.Path(), Problem: fmt.Sprintf("not accessible: %v", err)}
	}
	if !info.IsDir() {
		return Workspace{}, &InvalidPathError{Path: req.Path(), Problem: "not a directory"}
	}
	f, err := os.Open(resolved)
	if err != nil {
		return Workspace{}, &InvalidPathError{Path: req.Path(), Problem: fmt.Sprintf("not readable/enterable: %v", err)}
	}
	f.Close()
	w, err := New(resolved)
	if err != nil {
		return Workspace{}, err
	}
	if r.GitProbe != nil {
		if g, err := r.GitProbe(ctx, resolved); err == nil {
			if w2, err := w.WithGit(g); err == nil {
				w = w2
			}
		}
	}
	return w, nil
}

// ResolveFirst walks candidates in priority order (explicit, current
// directory, configured, recent) and returns the first workspace that
// verifies, plus every error in order when none verifies.
func (r *PathResolver) ResolveFirst(ctx context.Context, candidates []ResolveRequest) (Workspace, []error) {
	var errs []error
	for _, c := range ByPriority(candidates) {
		w, err := r.Resolve(ctx, c)
		if err == nil {
			return w, errs
		}
		errs = append(errs, fmt.Errorf("%s %q: %w", c.Source(), c.Path(), err))
	}
	return Workspace{}, errs
}

func (r *PathResolver) expand(path string) (string, error) {
	switch {
	case path == "~":
		if r.Home == "" {
			return "", &InvalidPathError{Path: path, Problem: "home directory unknown"}
		}
		return r.Home, nil
	case strings.HasPrefix(path, "~/"):
		if r.Home == "" {
			return "", &InvalidPathError{Path: path, Problem: "home directory unknown"}
		}
		return filepath.Join(r.Home, path[2:]), nil
	case strings.HasPrefix(path, "~"):
		return "", &InvalidPathError{Path: path, Problem: "~user expansion is not supported"}
	}
	return path, nil
}

// ErrNeedsCandidate is returned when no candidate was usable at all.
var ErrNeedsCandidate = errors.New("no workspace candidate could be resolved")
