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
