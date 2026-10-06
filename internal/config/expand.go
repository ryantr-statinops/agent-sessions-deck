package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ExpandHome expands an explicit leading `~` or `~/...` against home. It does
// not consult the environment for secret values and does not expand `~user`
// forms: only the current user's own home, and only when the path is written
// with the tilde, so a stored workspace never depends on whoever happens to
// run the process (ADR 0003's "no ad-hoc environment expansion").
func ExpandHome(path, home string) (string, error) {
	if path == "~" {
		return home, nil
	}
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(home, path[2:]), nil
	}
	if strings.HasPrefix(path, "~") {
		return "", fmt.Errorf("~%s expansion is not supported; write ~/...", path[1:])
	}
	return path, nil
}

// RedactedCommand renders the agent's executable without its argv, so the
// display path never leaks an argument that may carry a token (ADR 0003).
func (a Agent) RedactedCommand() string {
	if len(a.Args) == 0 {
		return a.Executable
	}
	return a.Executable + " [redacted]"
}

// CommandLine renders the literal argv for explicit, user-requested display.
// It is never used for logging: command-line logging stays off by default.
func (a Agent) CommandLine() string {
	parts := []string{a.Executable}
	parts = append(parts, a.Args...)
	return strings.Join(parts, " ")
}
