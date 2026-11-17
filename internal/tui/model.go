package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// Model is the top-level Bubble Tea model orchestrating the TUI views.
type Model struct {
	client     app.Client
	paths      config.Paths
	config     config.Config
	isOwner    bool
	mode       ViewMode
	returnMode ViewMode
	seq        AsyncSeq

	// Data
	sessions         []session.SessionSnapshot
	filteredSessions []session.SessionSnapshot
	agents           []owner.AgentProbe
	recentWorkspaces []string

	// Navigation & state
	cursor        int
	searchQuery   string
	searchActive  bool
	showDetail    bool
	agentFilter   string
	statusFilter  string
	statusMessage string
	statusErr     bool
	statusExpiry  time.Time
	err           error

	// Modals & sub-views
	confirmState ConfirmState

	// Form inputs (Launcher)
	launcherSelectedAgent int
	launcherWorkspace     string
	launcherName          string
	launcherFocusedField  int // 0: Agent, 1: Workspace, 2: Name
	launcherErr           string

	// Window dimensions
	width  int
	height int
}

// NewModel instantiates a Model configured with application client and environment options.
func NewModel(opts Options) Model {
	m := Model{
		client:           opts.Client,
		paths:            opts.Paths,
		config:           opts.Config,
		isOwner:          opts.IsOwner,
		mode:             ViewDashboard,
		recentWorkspaces: opts.RecentWorkspaces,
		agents:           opts.InitialAgents,
		width:            80,
		height:           24,
	}
	if len(m.recentWorkspaces) > 0 {
		m.launcherWorkspace = m.recentWorkspaces[0]
	}
	return m
}

// Init triggers initial asynchronous data fetching.
func (m Model) Init() tea.Cmd {
	m.seq++
	cmds := []tea.Cmd{
		m.loadSessionsCmd(m.seq),
	}
	if len(m.agents) == 0 {
		cmds = append(cmds, m.loadAgentsCmd(m.seq))
	}
	return tea.Batch(cmds...)
}

// NextSeq increments and returns the next async sequence counter.
func (m *Model) NextSeq() AsyncSeq {
	m.seq++
	return m.seq
}

// loadSessionsCmd queries session list asynchronously.
func (m Model) loadSessionsCmd(seq AsyncSeq) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		res, err := client.List(ctx, app.ListRequest{})
		return SessionListMsg{
			Seq:    seq,
			Result: res,
			Err:    err,
		}
	}
}

// loadAgentsCmd probes agents asynchronously.
func (m Model) loadAgentsCmd(seq AsyncSeq) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		res, err := client.Scan(ctx, app.ScanRequest{})
		return ScanResultMsg{
			Seq:    seq,
			Result: res,
			Err:    err,
		}
	}
}

// Update handles incoming messages and events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case SessionListMsg:
		// Stale sequence check: discard messages from older requests.
		if msg.Seq < m.seq {
			return m, nil
		}
		if msg.Err != nil {
			m.err = msg.Err
			m.setStatus("Error loading sessions: "+msg.Err.Error(), true, 5*time.Second)
			return m, nil
		}
		m.err = nil
		m.sessions = msg.Result.Snapshot.Sessions
		m.reapplyFilter()
		return m, nil

	case ScanResultMsg:
		if msg.Seq < m.seq {
			return m, nil
		}
		if msg.Err != nil {
			m.setStatus("Error probing agents: "+msg.Err.Error(), true, 5*time.Second)
			return m, nil
		}
		probes := make([]owner.AgentProbe, len(msg.Result.Agents))
		for i, a := range msg.Result.Agents {
			probes[i] = owner.AgentProbe{
				ID: a.ID,
			}
		}
		m.agents = probes
		return m, nil

	case ActionCompletedMsg:
		if msg.Seq < m.seq {
			return m, nil
		}
		if msg.Err != nil {
			m.setStatus(msg.Action+" failed: "+msg.Err.Error(), true, 5*time.Second)
		} else {
			m.setStatus(msg.Action+" completed successfully", false, 3*time.Second)
		}
		seq := m.NextSeq()
		return m, m.loadSessionsCmd(seq)

	case LaunchCompletedMsg:
		if msg.Seq < m.seq {
			return m, nil
		}
		if msg.Err != nil {
			m.launcherErr = msg.Err.Error()
			m.setStatus("Launch failed: "+msg.Err.Error(), true, 5*time.Second)
			return m, nil
		}
		m.launcherErr = ""
		m.mode = ViewDashboard
		m.setStatus("Session "+msg.SessionID+" created", false, 3*time.Second)
		seq := m.NextSeq()
		return m, m.loadSessionsCmd(seq)

	case StatusClearMsg:
		if time.Now().After(m.statusExpiry) {
			m.statusMessage = ""
			m.statusErr = false
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	}

	return m, nil
}

// setStatus sets a temporary status bar message with auto-expiry.
func (m *Model) setStatus(msg string, isErr bool, duration time.Duration) {
	m.statusMessage = msg
	m.statusErr = isErr
	m.statusExpiry = time.Now().Add(duration)
}

// reapplyFilter filters sessions by search query and active facets.
func (m *Model) reapplyFilter() {
	m.filteredSessions = FilterSessions(m.sessions, m.searchQuery, m.agentFilter, m.statusFilter)
	if m.cursor >= len(m.filteredSessions) {
		m.cursor = max(0, len(m.filteredSessions)-1)
	}
}

// SelectedSession returns the currently selected SessionSnapshot, if any.
func (m Model) SelectedSession() (session.SessionSnapshot, bool) {
	if len(m.filteredSessions) == 0 || m.cursor < 0 || m.cursor >= len(m.filteredSessions) {
		return session.SessionSnapshot{}, false
	}
	return m.filteredSessions[m.cursor], true
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
