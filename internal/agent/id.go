// Package agent defines the launchable agent identity, its capability set and
// the immutable resolved command the runtime launches.
//
// An Agent is a command definition, never a running instance
// (docs/architecture/glossary.md, "Agent"). This package resolves nothing from
// the filesystem and spawns nothing: PATH probing, symlink canonicalization and
// vendor adapters are Stage 04 work behind the Provider and Registry ports
// declared here.
package agent

import "fmt"

// maxIDLen bounds a configured agent id so session records stay readable.
const maxIDLen = 64

// ID is the stable identity of an Agent definition. It is the key used by
// Session.AgentID, by configured overrides and by the provider registry, so it
// must not encode a version, an executable path or a display name.
type ID string

// String returns the raw id.
func (id ID) String() string { return string(id) }

// Valid reports whether the id satisfies the agent-id grammar.
func (id ID) Valid() bool { return id.Validate() == nil }

// Validate checks the agent-id grammar: non-empty, at most maxIDLen bytes,
// lowercase ASCII start, then lowercase ASCII, digits, '-', '_' or '.'. Uppercase
// is rejected so a configured agent cannot silently differ from a built-in id
// only by case.
func (id ID) Validate() error {
	if id == "" {
		return &InvalidIDError{ID: id, Problem: "agent id is empty"}
	}
	if len(id) > maxIDLen {
		return &InvalidIDError{ID: id, Problem: fmt.Sprintf("agent id is longer than %d bytes", maxIDLen)}
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case (r == '-' || r == '_' || r == '.') && i > 0:
		default:
			return &InvalidIDError{ID: id, Problem: fmt.Sprintf("byte %d is not allowed in an agent id", i)}
		}
	}
	return nil
}

// InvalidIDError reports an agent id that does not satisfy the grammar.
// The session layer maps it to the INVALID_CONFIGURATION code.
type InvalidIDError struct {
	ID      ID
	Problem string
}

func (e *InvalidIDError) Error() string {
	return fmt.Sprintf("invalid agent id %q: %s", string(e.ID), e.Problem)
}
