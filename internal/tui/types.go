package tui

import (
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// WorkspaceGroup represents a grouped slice of sessions under one workspace.
type WorkspaceGroup struct {
	Summary  workspace.WorkspaceSummary
	Sessions []session.SessionSnapshot
}

// ViewMode defines the active full-screen or overlay mode.
type ViewMode int

const (
	// ViewDashboard is the primary session list, search bar, and detail inspector.
	ViewDashboard ViewMode = iota
	// ViewLauncher is the new-session form (agent selection, workspace path, name).
	ViewLauncher
	// ViewConfirm is an active modal confirmation dialog.
	ViewConfirm
	// ViewHelp is the keyboard shortcuts overlay.
	ViewHelp
	// ViewTerminal is the interactive virtual terminal view.
	ViewTerminal
)

// String returns a human-readable name for the ViewMode.
func (m ViewMode) String() string {
	switch m {
	case ViewDashboard:
		return "Dashboard"
	case ViewLauncher:
		return "Launcher"
	case ViewConfirm:
		return "Confirm"
	case ViewHelp:
		return "Help"
	case ViewTerminal:
		return "Terminal"
	default:
		return "Unknown"
	}
}

// AsyncSeq is a monotonic sequence counter used to discard stale async I/O responses.
type AsyncSeq uint64

// ConfirmAction defines the pending action waiting for explicit user confirmation.
type ConfirmAction int

const (
	ConfirmNone ConfirmAction = iota
	ConfirmRestartRunning
	ConfirmGracefulStop
	ConfirmForceKill
	ConfirmOwnerQuit
)

// ConfirmState holds details of an active confirmation request.
type ConfirmState struct {
	Action      ConfirmAction
	SessionID   string
	SessionName string
	ActiveCount int // Used for owner quit confirmation
	Prompt      string
	Impact      string
}

// Options configures the TUI initialization.
type Options struct {
	Client           app.Client
	Paths            config.Paths
	Config           config.Config
	IsOwner          bool
	RecentWorkspaces []string
	InitialAgents    []owner.AgentProbe
}

// SessionItem wraps a session.SessionSnapshot with cached formatted metadata for rendering.
type SessionItem struct {
	Row          session.SessionSnapshot
	Matched      bool
	Score        int
	DisplayPath  string
	GitBranch    string
	StatusSymbol string
	StatusText   string
}
