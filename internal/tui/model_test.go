package tui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

type mockAppClient struct {
	app.Client
	listFunc func(ctx context.Context, req app.ListRequest) (app.ListResult, error)
	scanFunc func(ctx context.Context, req app.ScanRequest) (app.ScanResult, error)
	openFunc func(ctx context.Context, req app.OpenRequest) (app.OpenResult, error)
}

func (m *mockAppClient) List(ctx context.Context, req app.ListRequest) (app.ListResult, error) {
	if m.listFunc != nil {
		return m.listFunc(ctx, req)
	}
	return app.ListResult{}, nil
}

func (m *mockAppClient) Scan(ctx context.Context, req app.ScanRequest) (app.ScanResult, error) {
	if m.scanFunc != nil {
		return m.scanFunc(ctx, req)
	}
	return app.ScanResult{}, nil
}

func (m *mockAppClient) Open(ctx context.Context, req app.OpenRequest) (app.OpenResult, error) {
	if m.openFunc != nil {
		return m.openFunc(ctx, req)
	}
	return app.OpenResult{}, nil
}

func TestModelInitialization(t *testing.T) {
	client := &mockAppClient{}
	m := NewModel(Options{
		Client:           client,
		RecentWorkspaces: []string{"/workspace/test"},
		IsOwner:          true,
	})

	if m.mode != ViewDashboard {
		t.Fatalf("expected initial mode ViewDashboard, got %v", m.mode)
	}
	if !m.isOwner {
		t.Fatalf("expected isOwner true, got false")
	}
	if m.launcherWorkspace != "/workspace/test" {
		t.Fatalf("expected launcherWorkspace '/workspace/test', got %q", m.launcherWorkspace)
	}
	if m.terminalPrefix == nil || m.terminalScroll == nil {
		t.Fatal("expected terminalPrefix and terminalScroll to be initialized")
	}
	if ViewTerminal.String() != "Terminal" {
		t.Fatalf("expected ViewTerminal.String() == 'Terminal', got %q", ViewTerminal.String())
	}

	cmd := m.Init()
	if cmd == nil {
		t.Fatalf("expected non-nil Init command")
	}
}

func TestModelSequenceFencing(t *testing.T) {
	client := &mockAppClient{}
	m := NewModel(Options{Client: client})

	m.seq = 5

	staleMsg := SessionListMsg{
		Seq: 3,
		Result: app.ListResult{
			Snapshot: session.Snapshot{
				Sessions: []session.SessionSnapshot{
					{ID: session.ID("stale-id"), Name: "stale"},
				},
			},
		},
	}

	updated, _ := m.Update(staleMsg)
	mUpdated := updated.(Model)
	if len(mUpdated.sessions) != 0 {
		t.Fatalf("expected stale SessionListMsg to be ignored, got %d sessions", len(mUpdated.sessions))
	}

	validMsg := SessionListMsg{
		Seq: 5,
		Result: app.ListResult{
			Snapshot: session.Snapshot{
				Sessions: []session.SessionSnapshot{
					{ID: session.ID("valid-id"), Name: "valid"},
				},
			},
		},
	}

	updated2, _ := m.Update(validMsg)
	mUpdated2 := updated2.(Model)
	if len(mUpdated2.sessions) != 1 || mUpdated2.sessions[0].Name != "valid" {
		t.Fatalf("expected valid SessionListMsg to be applied, got %+v", mUpdated2.sessions)
	}
}

func TestModelWindowSize(t *testing.T) {
	m := NewModel(Options{})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	mUpdated := updated.(Model)

	if mUpdated.width != 120 || mUpdated.height != 40 {
		t.Fatalf("expected dimensions 120x40, got %dx%d", mUpdated.width, mUpdated.height)
	}
}

func TestModelErrorHandling(t *testing.T) {
	m := NewModel(Options{})
	m.seq = 2

	errMsg := SessionListMsg{
		Seq: 2,
		Err: errors.New("network failure"),
	}

	updated, _ := m.Update(errMsg)
	mUpdated := updated.(Model)

	if mUpdated.err == nil || mUpdated.err.Error() != "network failure" {
		t.Fatalf("expected err 'network failure', got %v", mUpdated.err)
	}
	if !mUpdated.statusErr {
		t.Fatalf("expected statusErr true")
	}
}

func TestModelKeyNavigation(t *testing.T) {
	m := NewModel(Options{})
	m.filteredSessions = []session.SessionSnapshot{
		{ID: session.ID("s1"), Name: "session 1", AgentID: agent.ID("claude")},
		{ID: session.ID("s2"), Name: "session 2", AgentID: agent.ID("codex")},
	}

	// Down / j
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	mUp := updated.(Model)
	if mUp.cursor != 1 {
		t.Fatalf("expected cursor 1 after 'j', got %d", mUp.cursor)
	}

	// Up / k
	updated2, _ := mUp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	mUp2 := updated2.(Model)
	if mUp2.cursor != 0 {
		t.Fatalf("expected cursor 0 after 'k', got %d", mUp2.cursor)
	}

	// New mode switch 'n'
	updated3, _ := mUp2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	mUp3 := updated3.(Model)
	if mUp3.mode != ViewLauncher {
		t.Fatalf("expected mode ViewLauncher after 'n', got %v", mUp3.mode)
	}

	// Help overlay '?'
	mUp4 := NewModel(Options{})
	updated4, _ := mUp4.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	mUp5 := updated4.(Model)
	if mUp5.mode != ViewHelp {
		t.Fatalf("expected mode ViewHelp after '?', got %v", mUp5.mode)
	}
}

type mockTerminalSub struct {
	app.TerminalSubscription
	frame       app.TerminalFrame
	err         error
	resizeCols  int
	resizeRows  int
	resizeCount int
}

func (m *mockTerminalSub) ReadFrame() (app.TerminalFrame, error) {
	return m.frame, m.err
}

func (m *mockTerminalSub) InitialSnapshot() app.TerminalSnapshot {
	if m.frame.Snapshot != nil {
		return *m.frame.Snapshot
	}
	return app.TerminalSnapshot{}
}

func (m *mockTerminalSub) Resize(cols, rows int) error {
	m.resizeCols = cols
	m.resizeRows = rows
	m.resizeCount++
	return nil
}

func TestTerminalResizeOnWindowSize(t *testing.T) {
	sub := &mockTerminalSub{}
	m := NewModel(Options{})
	m.mode = ViewTerminal
	m.activeSub = sub

	// Send WindowSizeMsg 100x30
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	mUpdated := updated.(Model)

	if mUpdated.width != 100 || mUpdated.height != 30 {
		t.Fatalf("expected dimensions 100x30, got %dx%d", mUpdated.width, mUpdated.height)
	}
	if sub.resizeCount != 1 || sub.resizeCols != 100 || sub.resizeRows != 29 {
		t.Fatalf("expected sub.Resize(100, 29), got count=%d cols=%d rows=%d",
			sub.resizeCount, sub.resizeCols, sub.resizeRows)
	}
}

func TestWaitForFrameCmd(t *testing.T) {
	sub := &mockTerminalSub{
		frame: app.TerminalFrame{Sequence: 42},
	}
	cmd := waitForFrameCmd(sub, "sess-1")
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}

	msg := cmd()
	frameMsg, ok := msg.(TerminalFrameMsg)
	if !ok {
		t.Fatalf("expected TerminalFrameMsg, got %T", msg)
	}
	if frameMsg.SessionID != "sess-1" || frameMsg.Frame.Sequence != 42 {
		t.Fatalf("unexpected frameMsg: %+v", frameMsg)
	}
}

func TestApplyTerminalFrame(t *testing.T) {
	m := NewModel(Options{})
	m.mode = ViewTerminal
	m.activeSessionID = "test-sess"

	// 1. Snapshot application
	snapshot := &app.TerminalSnapshot{
		Sequence: 1,
		Screen: app.TerminalScreen{
			Columns: 10,
			Rows:    2,
			Lines: []app.TerminalLine{
				{Index: 0, Cells: []app.TerminalCell{{Text: "Hello"}}},
				{Index: 1, Cells: []app.TerminalCell{{Text: "World"}}},
			},
			Cursor: app.TerminalCursor{X: 5, Y: 0, Visible: true},
		},
	}
	m.applyTerminalFrame(app.TerminalFrame{Snapshot: snapshot})
	if m.terminalScreen.Columns != 10 || len(m.terminalScreen.Lines) != 2 {
		t.Fatalf("expected 2 lines of 10 cols, got %+v", m.terminalScreen)
	}
	if m.terminalScreen.Lines[0].Cells[0].Text != "Hello" {
		t.Fatalf("expected 'Hello', got %q", m.terminalScreen.Lines[0].Cells[0].Text)
	}

	// 2. Incremental Delta application
	delta := &app.TerminalDelta{
		BaseSequence: 1,
		Columns:      10,
		Rows:         2,
		Lines: []app.TerminalLine{
			{Index: 1, Cells: []app.TerminalCell{{Text: "Updated"}}},
		},
		Cursor: app.TerminalCursor{X: 7, Y: 1, Visible: true},
	}
	m.applyTerminalFrame(app.TerminalFrame{Delta: delta})
	if m.terminalScreen.Lines[0].Cells[0].Text != "Hello" {
		t.Fatalf("line 0 should remain 'Hello', got %q", m.terminalScreen.Lines[0].Cells[0].Text)
	}
	if m.terminalScreen.Lines[1].Cells[0].Text != "Updated" {
		t.Fatalf("line 1 should be 'Updated', got %q", m.terminalScreen.Lines[1].Cells[0].Text)
	}
	if m.terminalScreen.Cursor.X != 7 || m.terminalScreen.Cursor.Y != 1 {
		t.Fatalf("expected cursor at (7,1), got (%d,%d)", m.terminalScreen.Cursor.X, m.terminalScreen.Cursor.Y)
	}
}

func TestTerminalStreamTerminationFreezesFinalScreen(t *testing.T) {
	m := NewModel(Options{})
	m.mode = ViewTerminal
	m.activeSessionID = "active-1"
	m.terminalScreen = app.TerminalScreen{
		Columns: 10,
		Rows:    1,
		Lines: []app.TerminalLine{
			{Index: 0, Cells: []app.TerminalCell{{Text: "Final Output"}}},
		},
	}

	// Deliver EOF
	updated, cmd := m.Update(TerminalFrameMsg{
		SessionID: "active-1",
		Err:       errors.New("EOF"),
	})

	mFinal := updated.(Model)
	if !mFinal.terminalExited {
		t.Fatal("expected terminalExited to be true")
	}
	if mFinal.terminalExitErr == nil {
		t.Fatal("expected non-nil terminalExitErr")
	}
	if cmd != nil {
		t.Fatal("expected nil command after EOF (stream finished)")
	}
	if len(mFinal.terminalScreen.Lines) != 1 || mFinal.terminalScreen.Lines[0].Cells[0].Text != "Final Output" {
		t.Fatalf("expected final screen content to be preserved, got %+v", mFinal.terminalScreen)
	}
}

func TestOpenSessionTransitionsToViewTerminal(t *testing.T) {
	sub := &mockTerminalSub{
		frame: app.TerminalFrame{
			Snapshot: &app.TerminalSnapshot{
				Screen: app.TerminalScreen{Columns: 80, Rows: 24},
			},
		},
	}
	client := &mockAppClient{
		openFunc: func(ctx context.Context, req app.OpenRequest) (app.OpenResult, error) {
			return app.OpenResult{Terminal: sub}, nil
		},
	}
	m := NewModel(Options{Client: client})
	cmd := m.openSessionCmd("s1")
	msg := cmd()
	updated, _ := m.Update(msg)
	mUpdated := updated.(Model)
	if mUpdated.mode != ViewTerminal || mUpdated.activeSessionID != "s1" {
		t.Fatalf("expected ViewTerminal mode and activeSessionID 's1', got mode=%v id=%s",
			mUpdated.mode, mUpdated.activeSessionID)
	}
}
