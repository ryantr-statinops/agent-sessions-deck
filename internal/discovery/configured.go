package discovery

import (
	"fmt"
	"sort"
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
)

// CollisionError reports configured agents that collide with built-in
// providers or with each other. It fails deterministically instead of
// launching an unexpected binary.
type CollisionError struct {
	Problems []string
}

func (e *CollisionError) Error() string {
	sort.Strings(e.Problems)
	return "configured agent collision: " + strings.Join(e.Problems, "; ")
}

// ValidateConfiguredAgents checks every configured override before any of
// them is used: stable IDs must be grammar-valid and unique across the
// configured set and the built-in registry, and display names must not
// shadow a different configured or built-in agent.
func ValidateConfiguredAgents(configured []config.Agent, builtins agent.Registry) error {
	var problems []string
	seenID := map[string]config.Agent{}
	seenName := map[string]config.Agent{}
	for _, c := range configured {
		if err := agent.ID(c.ID).Validate(); err != nil {
			problems = append(problems, fmt.Sprintf("agent id %q: %v", c.ID, err))
		}
		if c.Name == "" {
			problems = append(problems, fmt.Sprintf("agent %q: empty name", c.ID))
		}
		if c.Executable == "" {
			problems = append(problems, fmt.Sprintf("agent %q: empty executable", c.ID))
		}
		if prev, dup := seenID[c.ID]; dup {
			problems = append(problems, fmt.Sprintf("duplicate agent id %q (names %q and %q)", c.ID, prev.Name, c.Name))
		}
		if prev, dup := seenName[c.Name]; dup && c.Name != "" {
			problems = append(problems, fmt.Sprintf("duplicate agent name %q (ids %q and %q)", c.Name, prev.ID, c.ID))
		}
		seenID[c.ID] = c
		seenName[c.Name] = c
		if builtins != nil {
			if _, found := builtins.Lookup(agent.ID(c.ID)); found {
				problems = append(problems, fmt.Sprintf("configured agent id %q collides with a built-in provider", c.ID))
			}
		}
	}
	if len(problems) > 0 {
		return &CollisionError{Problems: problems}
	}
	return nil
}
