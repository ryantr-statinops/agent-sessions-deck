package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestKeymapPagingAndJump(t *testing.T) {
	m := NewModel(Options{})
	m.height = 20 // pageSize = max(5, 20-10) = 10
	sessions := make([]session.SessionSnapshot, 30)
	for i := range sessions {
		sessions[i] = session.SessionSnapshot{
			ID:      session.ID(string(rune('a' + i))),
			AgentID: agent.ID("claude"),
		}
	}
	m.filteredSessions = sessions
	m.cursor = 0

	// End / 'G' -> jump to last
	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	mUp := up.(Model)
	if mUp.cursor != 29 {
		t.Fatalf("expected cursor at 29 after 'G', got %d", mUp.cursor)
	}

	// Home / 'g' -> jump to first
	up2, _ := mUp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	mUp2 := up2.(Model)
	if mUp2.cursor != 0 {
		t.Fatalf("expected cursor at 0 after 'g', got %d", mUp2.cursor)
	}

	// Page down / 'f'
	up3, _ := mUp2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'f'}})
	mUp3 := up3.(Model)
	if mUp3.cursor != 10 {
		t.Fatalf("expected cursor at 10 after page down, got %d", mUp3.cursor)
	}

	// Page up / 'b'
	up4, _ := mUp3.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	mUp4 := up4.(Model)
	if mUp4.cursor != 0 {
		t.Fatalf("expected cursor at 0 after page up, got %d", mUp4.cursor)
	}
}

func TestKeymapSearchModeIsolation(t *testing.T) {
	m := NewModel(Options{})
	m.filteredSessions = []session.SessionSnapshot{
		{
			ID:        session.ID("sess-1"),
			Name:      "test",
			AgentID:   agent.ID("claude"),
			Lifecycle: session.LifecycleRunning,
		},
	}
	m.cursor = 0

	// 1. Activate search mode with '/'
	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mUp := up.(Model)
	if !mUp.searchActive {
		t.Fatalf("expected searchActive true after '/'")
	}

	// 2. Press 'r' -> should append to search query, NOT trigger restart modal!
	up2, _ := mUp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	mUp2 := up2.(Model)
	if mUp2.mode == ViewConfirm {
		t.Fatalf("typing 'r' during search must NOT switch to ViewConfirm")
	}
	if mUp2.searchQuery != "r" {
		t.Fatalf("expected searchQuery 'r', got %q", mUp2.searchQuery)
	}

	// 3. Press 'x' -> should append to search query, NOT trigger stop modal!
	up3, _ := mUp2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	mUp3 := up3.(Model)
	if mUp3.mode == ViewConfirm {
		t.Fatalf("typing 'x' during search must NOT switch to ViewConfirm")
	}
	if mUp3.searchQuery != "rx" {
		t.Fatalf("expected searchQuery 'rx', got %q", mUp3.searchQuery)
	}

	// 4. Press Enter -> exit search input mode
	up4, _ := mUp3.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mUp4 := up4.(Model)
	if mUp4.searchActive {
		t.Fatalf("expected searchActive false after Enter")
	}
}

func TestHelpOverlayRenderingAndToggle(t *testing.T) {
	m := NewModel(Options{})
	m.mode = ViewDashboard

	// Press '?'
	up, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	mUp := up.(Model)
	if mUp.mode != ViewHelp {
		t.Fatalf("expected mode ViewHelp after '?', got %v", mUp.mode)
	}

	rendered := mUp.renderHelp()
	if !strings.Contains(rendered, "KEYBOARD SHORTCUTS REFERENCE") {
		t.Fatalf("expected header in help overlay, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Dashboard Navigation") {
		t.Fatalf("expected section 'Dashboard Navigation' in help")
	}

	// Press '?' again -> back to ViewDashboard
	up2, _ := mUp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	mUp2 := up2.(Model)
	if mUp2.mode != ViewDashboard {
		t.Fatalf("expected return to ViewDashboard after second '?', got %v", mUp2.mode)
	}
}
