// Package workspace defines workspace identity and the resolver port.
//
// Workspace identity is the canonical directory: the same directory resolved
// through different paths is one workspace (Stage 04 acceptance), and Git
// metadata never replaces the directory the user selected, because a session may
// legitimately run in a repository subdirectory (docs/architecture/glossary.md,
// "Workspace"). This package holds no filesystem backend: existence, readability
// and symlink resolution are Stage 04's job behind the Resolver port.
package workspace

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ID is a workspace identity: the canonical absolute directory path.
//
// It is stored instead of a generated token so that a stored Session keeps a
// checkable workspace reference across owners and restarts, and so two sessions
// in the same directory compare equal without a lookup.
type ID string

// maxPathLen bounds a canonical path so a pathological input cannot become a
// session record.
const maxPathLen = 4096

// NewID validates a canonical directory path and returns its identity.
func NewID(canonicalPath string) (ID, error) {
	canonical, err := Canonical(canonicalPath)
	if err != nil {
		return "", err
	}
	return ID(canonical), nil
}

// String returns the canonical path.
func (id ID) String() string { return string(id) }

// Path returns the canonical directory path.
func (id ID) Path() string { return string(id) }

// Valid reports whether the identity is a canonical absolute path.
func (id ID) Valid() bool { return id.Validate() == nil }

// Validate checks that the identity is a canonical absolute path.
func (id ID) Validate() error {
	if id == "" {
		return &InvalidPathError{Problem: "workspace id is empty"}
	}
	if len(id) > maxPathLen {
		return &InvalidPathError{Path: string(id), Problem: fmt.Sprintf("path is longer than %d bytes", maxPathLen)}
	}
	if strings.ContainsRune(string(id), 0) {
		return &InvalidPathError{Path: string(id), Problem: "path contains a NUL byte"}
	}
	if !filepath.IsAbs(string(id)) {
		return &InvalidPathError{Path: string(id), Problem: "workspace id is not an absolute path"}
	}
	if cleaned := filepath.Clean(string(id)); cleaned != string(id) {
		return &InvalidPathError{Path: string(id), Problem: "workspace id is not in canonical form (" + cleaned + ")"}
	}
	return nil
}

// Canonical normalizes a caller-supplied path lexically and rejects anything
// Stage 02 cannot promise without touching the filesystem.
//
// The path must already be absolute: tilde expansion and relative-path
// resolution depend on the invocation context, so they belong to the resolver
// (Stage 04) rather than to the identity rule. Symlinks are likewise left to
// the resolver, which reports the canonical directory it actually verified.
func Canonical(path string) (string, error) {
	if path == "" {
		return "", &InvalidPathError{Problem: "workspace path is empty"}
	}
	if len(path) > maxPathLen {
		return "", &InvalidPathError{Path: path, Problem: fmt.Sprintf("path is longer than %d bytes", maxPathLen)}
	}
	if strings.ContainsRune(path, 0) {
		return "", &InvalidPathError{Path: path, Problem: "path contains a NUL byte"}
	}
	if strings.TrimSpace(path) != path {
		return "", &InvalidPathError{Path: path, Problem: "path has leading or trailing whitespace"}
	}
	if !filepath.IsAbs(path) {
		return "", &InvalidPathError{Path: path, Problem: "workspace path must be absolute"}
	}
	return filepath.Clean(path), nil
}

// InvalidPathError reports a path that cannot become a workspace identity.
// The session layer maps it to the INVALID_CONFIGURATION code.
type InvalidPathError struct {
	Path    string
	Problem string
}

func (e *InvalidPathError) Error() string {
	if e.Path == "" {
		return "invalid workspace path: " + e.Problem
	}
	return fmt.Sprintf("invalid workspace path %q: %s", e.Path, e.Problem)
}
