package mcp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
)

var (
	// ErrApprovalRequired indicates a mutation was attempted in read-only mode or without approval.
	ErrApprovalRequired = errors.New("approval required: mutation tool execution requires explicit grant")

	// ErrForbidden indicates a tool or action is forbidden by configuration allowlists.
	ErrForbidden = errors.New("forbidden: operation not permitted by policy")

	// ErrWorkspaceEscape indicates a target path resolves outside configured workspaces.
	ErrWorkspaceEscape = errors.New("workspace policy violation: target path escapes allowed workspaces")

	// ErrAgentNotAllowed indicates an agent is not permitted by policy.
	ErrAgentNotAllowed = errors.New("agent policy violation: target agent is not in allowed list")
)

// PolicyEngine enforces access control, read-only gating, and workspace containment.
type PolicyEngine struct {
	readOnly          bool
	allowedTools      map[string]bool
	allowedWorkspaces []string
	allowedAgents     map[string]bool
	maxOutputBytes    int
}

// NewPolicyEngine creates an engine from MCP configuration.
func NewPolicyEngine(cfg config.MCP) (*PolicyEngine, error) {
	pe := &PolicyEngine{
		readOnly:          cfg.IsReadOnly(),
		allowedTools:      make(map[string]bool),
		allowedWorkspaces: make([]string, 0, len(cfg.AllowedWorkspaces)),
		allowedAgents:     make(map[string]bool),
		maxOutputBytes:    cfg.MaxOutputBytes,
	}
	if pe.maxOutputBytes <= 0 {
		pe.maxOutputBytes = 65536
	}
	for _, t := range cfg.AllowedTools {
		trimmed := strings.TrimSpace(t)
		if trimmed != "" {
			pe.allowedTools[trimmed] = true
		}
	}
	for _, a := range cfg.AllowedAgents {
		trimmed := strings.TrimSpace(a)
		if trimmed != "" {
			pe.allowedAgents[trimmed] = true
		}
	}
	for _, w := range cfg.AllowedWorkspaces {
		canonical, err := canonicalizePath(w)
		if err == nil {
			pe.allowedWorkspaces = append(pe.allowedWorkspaces, canonical)
		}
	}
	return pe, nil
}

// IsReadOnly reports whether the policy enforces read-only mode.
func (p *PolicyEngine) IsReadOnly() bool {
	return p.readOnly
}

// MaxOutputBytes returns the configured maximum output bytes.
func (p *PolicyEngine) MaxOutputBytes() int {
	return p.maxOutputBytes
}

// IsToolAllowed returns true if tool is permitted by allowed_tools filter.
func (p *PolicyEngine) IsToolAllowed(name string) bool {
	if len(p.allowedTools) == 0 {
		return true
	}
	return p.allowedTools[name]
}

// CheckToolExecution verifies whether the tool is allowed to run.
func (p *PolicyEngine) CheckToolExecution(name string, annotations *ToolAnnotation) error {
	if !p.IsToolAllowed(name) {
		return fmt.Errorf("%w: tool %q is not in allowed_tools list", ErrForbidden, name)
	}

	isMutation := annotations != nil && (annotations.Destructive || !annotations.ReadOnly)
	if isMutation && p.readOnly {
		return fmt.Errorf("%w: server is operating in read-only mode; cannot execute %q", ErrApprovalRequired, name)
	}

	return nil
}

// CheckAgentAllowed verifies if an agent ID is permitted.
func (p *PolicyEngine) CheckAgentAllowed(agentID string) error {
	if len(p.allowedAgents) == 0 {
		return nil
	}
	if !p.allowedAgents[agentID] {
		return fmt.Errorf("%w: agent %q is not in allowed_agents", ErrAgentNotAllowed, agentID)
	}
	return nil
}

// CheckWorkspaceAllowed checks that targetDir resolves inside at least one allowed workspace.
func (p *PolicyEngine) CheckWorkspaceAllowed(targetDir string) error {
	if len(p.allowedWorkspaces) == 0 {
		return nil
	}

	canonicalTarget, err := canonicalizePath(targetDir)
	if err != nil {
		return fmt.Errorf("%w: cannot resolve path %q: %v", ErrWorkspaceEscape, targetDir, err)
	}

	for _, allowed := range p.allowedWorkspaces {
		if isInsideDirectory(allowed, canonicalTarget) {
			return nil
		}
	}

	return fmt.Errorf("%w: directory %q is outside configured allowed_workspaces", ErrWorkspaceEscape, canonicalTarget)
}

func canonicalizePath(p string) (string, error) {
	if strings.HasPrefix(p, "~/") || p == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	// If path exists on disk, evaluate symlinks to prevent symlink traversal escapes
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	return filepath.Clean(abs), nil
}

func isInsideDirectory(parent, target string) bool {
	parent = filepath.Clean(parent)
	target = filepath.Clean(target)
	if parent == target {
		return true
	}
	rel, err := filepath.Rel(parent, target)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != ".."
}
