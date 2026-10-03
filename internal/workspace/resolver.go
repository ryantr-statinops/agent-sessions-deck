package workspace

import (
	"context"
	"fmt"
	"sort"
)

// Source records where a workspace candidate came from. The order is normative:
// the resolver tries an explicit CLI path first, then the current working
// directory, then configured workspaces, then recent history, and never scans
// the disk (glossary, "Workspace").
type Source string

const (
	SourceExplicit         Source = "explicit"
	SourceCurrentDirectory Source = "current-directory"
	SourceConfigured       Source = "configured"
	SourceRecent           Source = "recent"
)

// AllSources returns every workspace source in resolution order.
func AllSources() []Source {
	return []Source{SourceExplicit, SourceCurrentDirectory, SourceConfigured, SourceRecent}
}

// String returns the wire name of the source.
func (s Source) String() string { return string(s) }

// Valid reports whether the source is one of the defined ones.
func (s Source) Valid() bool {
	switch s {
	case SourceExplicit, SourceCurrentDirectory, SourceConfigured, SourceRecent:
		return true
	default:
		return false
	}
}

// Priority returns the resolution rank, 0 being the strongest. An unknown source
// ranks last so it never wins a lookup.
func (s Source) Priority() int {
	for i, known := range AllSources() {
		if known == s {
			return i
		}
	}
	return len(AllSources())
}

// ResolveRequest is one immutable workspace candidate.
type ResolveRequest struct {
	path   string
	source Source
}

// NewResolveRequest validates and builds a candidate.
func NewResolveRequest(path string, source Source) (ResolveRequest, error) {
	if !source.Valid() {
		return ResolveRequest{}, &InvalidPathError{Path: path, Problem: fmt.Sprintf("unknown workspace source %q", string(source))}
	}
	if path == "" {
		return ResolveRequest{}, &InvalidPathError{Problem: "workspace path is empty"}
	}
	return ResolveRequest{path: path, source: source}, nil
}

// Path returns the candidate path exactly as supplied, before canonicalization.
func (r ResolveRequest) Path() string { return r.path }

// Source returns where the candidate came from.
func (r ResolveRequest) Source() Source { return r.source }

// Resolver turns a candidate into a verified workspace.
//
// Stage 04 implements it: the resolver owns tilde expansion, relative-path
// resolution, symlink policy, directory validation and the Git probe, and it must
// fail honestly rather than return a directory it did not verify.
type Resolver interface {
	Resolve(ctx context.Context, req ResolveRequest) (Workspace, error)
}

// ByPriority returns the candidates in resolution order without touching the
// input slice. Equal priorities keep their input order, so resolution stays
// deterministic.
func ByPriority(candidates []ResolveRequest) []ResolveRequest {
	ordered := make([]ResolveRequest, len(candidates))
	copy(ordered, candidates)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].source.Priority() < ordered[j].source.Priority()
	})
	return ordered
}
