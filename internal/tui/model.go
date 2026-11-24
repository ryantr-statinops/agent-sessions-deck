package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	termview "github.com/ryantr-statinops/agent-sessions-deck/internal/tui/terminal"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
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
	groupByWorkspace bool
	workspaceGroups  []WorkspaceGroup

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
	attachTarget string

	// Form inputs (Launcher)
	launcherSelectedAgent int
	launcherWorkspace     string
	launcherName          string
	launcherFocusedField  int // 0: Agent, 1: Workspace, 2: Name
	launcherErr           string

	// Window dimensions
	width  int
	height int

	// Terminal view state
	activeSessionID string
	activeSub       app.TerminalSubscription
	terminalScreen  app.TerminalScreen
	terminalPrefix  *termview.PrefixMachine
	terminalScroll  *termview.ScrollController
	terminalExited  bool
	terminalExitErr error
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
		terminalPrefix:   termview.NewPrefixMachine(),
		terminalScroll:   termview.NewScrollController(nil),
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

// openSessionCmd opens an interactive terminal stream to a session.
func (m Model) openSessionCmd(sessionID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		res, err := client.Open(ctx, app.OpenRequest{
			Ref:    sessionID,
			Holder: "tui-client",
		})
		return SessionOpenResultMsg{
			SessionID: sessionID,
			Result:    res,
			Err:       err,
		}
	}
}

// detachSessionCmd asynchronously asks the client to detach and release the lease.
func (m Model) detachSessionCmd(sessionID string) tea.Cmd {
	if m.client == nil || sessionID == "" {
		return nil
	}
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = client.Detach(ctx, app.DetachRequest{Ref: sessionID})
		return nil
	}
}

// Update handles incoming messages and events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.mode == ViewTerminal && m.activeSub != nil {
			cols, rows := termview.ViewportDimensions(m.width, m.height)
			_ = m.activeSub.Resize(cols, rows)
		}
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

	case ResyncMsg:
		seq := m.NextSeq()
		return m, m.loadSessionsCmd(seq)

	case events.Notification:
		seq := m.NextSeq()
		return m, m.loadSessionsCmd(seq)

	case events.Event:
		seq := m.NextSeq()
		return m, m.loadSessionsCmd(seq)

	case TerminalFrameMsg:
		if msg.SessionID != m.activeSessionID || m.mode != ViewTerminal {
			return m, nil
		}
		if msg.Err != nil {
			m.terminalExitErr = msg.Err
			m.terminalExited = true
			return m, nil
		}
		m.applyTerminalFrame(msg.Frame)
		return m, waitForFrameCmd(m.activeSub, m.activeSessionID)

	case SessionOpenResultMsg:
		if msg.Err != nil {
			m.setStatus("Failed to open session: "+msg.Err.Error(), true, 5*time.Second)
			return m, nil
		}
		m.mode = ViewTerminal
		m.activeSessionID = msg.SessionID
		m.activeSub = msg.Result.Terminal
		if msg.Result.Terminal != nil {
			m.terminalScreen = msg.Result.Terminal.InitialSnapshot().Screen
		}
		m.terminalExited = false
		m.terminalExitErr = nil
		if m.terminalPrefix != nil {
			m.terminalPrefix.Reset()
		}
		if m.terminalScroll != nil {
			m.terminalScroll.SetAlternateScreen(m.terminalScreen.AlternateScreen)
			m.terminalScroll.ScrollToBottom()
		}
		if m.activeSub != nil {
			cols, rows := termview.ViewportDimensions(m.width, m.height)
			_ = m.activeSub.Resize(cols, rows)
		}
		return m, waitForFrameCmd(m.activeSub, m.activeSessionID)

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	}

	return m, nil
}

// waitForFrameCmd listens asynchronously for terminal frames from an active subscription.
func waitForFrameCmd(sub app.TerminalSubscription, sessionID string) tea.Cmd {
	if sub == nil || sessionID == "" {
		return nil
	}
	return func() tea.Msg {
		frame, err := sub.ReadFrame()
		return TerminalFrameMsg{
			SessionID: sessionID,
			Frame:     frame,
			Err:       err,
		}
	}
}

// applyTerminalFrame merges incoming terminal frames into the active screen model.
func (m *Model) applyTerminalFrame(frame app.TerminalFrame) {
	if frame.Snapshot != nil {
		m.terminalScreen = frame.Snapshot.Screen
		if m.terminalScroll != nil {
			m.terminalScroll.SetAlternateScreen(frame.Snapshot.Screen.AlternateScreen)
		}
		return
	}

	if frame.Delta != nil {
		delta := frame.Delta
		m.terminalScreen.Columns = delta.Columns
		m.terminalScreen.Rows = delta.Rows
		m.terminalScreen.Cursor = delta.Cursor
		m.terminalScreen.AlternateScreen = delta.AlternateScreen
		m.terminalScreen.ScrollbackLines = delta.ScrollbackLines

		if m.terminalScroll != nil {
			m.terminalScroll.SetAlternateScreen(delta.AlternateScreen)
		}

		if len(m.terminalScreen.Lines) < delta.Rows {
			newLines := make([]app.TerminalLine, delta.Rows)
			copy(newLines, m.terminalScreen.Lines)
			m.terminalScreen.Lines = newLines
		} else if len(m.terminalScreen.Lines) > delta.Rows && delta.Rows > 0 {
			m.terminalScreen.Lines = m.terminalScreen.Lines[:delta.Rows]
		}

		for _, line := range delta.Lines {
			if line.Index >= 0 && line.Index < len(m.terminalScreen.Lines) {
				m.terminalScreen.Lines[line.Index] = line
			}
		}
	}
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
	if m.groupByWorkspace {
		m.workspaceGroups = m.buildWorkspaceGroups()
	}
}

// GroupByWorkspace reports whether sessions are grouped by workspace.
func (m Model) GroupByWorkspace() bool {
	return m.groupByWorkspace
}

// WorkspaceGroups returns the active workspace groups.
func (m Model) WorkspaceGroups() []WorkspaceGroup {
	return m.workspaceGroups
}

// SetGroupByWorkspace toggles or sets workspace grouping.
func (m *Model) SetGroupByWorkspace(enabled bool) {
	m.groupByWorkspace = enabled
	if enabled {
		m.workspaceGroups = m.buildWorkspaceGroups()
	} else {
		m.workspaceGroups = nil
	}
}

// SetWindowSize sets the dimensions of the terminal window.
func (m *Model) SetWindowSize(width, height int) {
	m.width = width
	m.height = height
}

// SetSessions sets the active session snapshots and refreshes filters and groupings.
func (m *Model) SetSessions(sessions []session.SessionSnapshot) {
	m.sessions = sessions
	m.reapplyFilter()
}

// buildWorkspaceGroups partitions the filtered sessions into WorkspaceGroup structures.
func (m Model) buildWorkspaceGroups() []WorkspaceGroup {
	if len(m.filteredSessions) == 0 {
		return nil
	}
	grouped := make(map[workspace.ID][]session.SessionSnapshot)
	order := make([]workspace.ID, 0)
	for _, s := range m.filteredSessions {
		wsID := s.WorkspaceID
		if _, ok := grouped[wsID]; !ok {
			order = append(order, wsID)
		}
		grouped[wsID] = append(grouped[wsID], s)
	}

	result := make([]WorkspaceGroup, 0, len(order))
	for _, wsID := range order {
		sessList := grouped[wsID]
		ws, _ := workspace.New(string(wsID))
		recs := make([]workspace.SessionRecord, 0, len(sessList))
		for _, s := range sessList {
			recs = append(recs, workspace.SessionRecord{
				ID:         string(s.ID),
				Workspace:  wsID,
				Active:     s.Lifecycle.Active(),
				ObservedAt: s.ObservedAt,
			})
		}
		summary := workspace.SummarizeGroup(ws, recs)
		result = append(result, WorkspaceGroup{
			Summary:  summary,
			Sessions: sessList,
		})
	}
	return result
}

// AttachTarget returns the session ID to attach into when TUI suspends, if any.
func (m Model) AttachTarget() string {
	return m.attachTarget
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
