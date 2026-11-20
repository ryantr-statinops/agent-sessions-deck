package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	termview "github.com/ryantr-statinops/agent-sessions-deck/internal/tui/terminal"
)

func TestRenderTerminalViewAndFooters(t *testing.T) {
	m := NewModel(Options{})
	m.mode = ViewTerminal
	m.activeSessionID = "my-agent-1"
	m.width = 80
	m.height = 24
	m.terminalScreen = app.TerminalScreen{
		Columns: 80,
		Rows:    23,
		Lines: []app.TerminalLine{
			{Index: 0, Cells: []app.TerminalCell{{Text: "Agent output line 1"}}},
		},
	}

	t.Run("Normal terminal view", func(t *testing.T) {
		view := m.View()
		if !strings.Contains(view, "Agent output line 1") {
			t.Fatalf("expected view to contain terminal output, got %q", view)
		}
		if !strings.Contains(view, "[my-agent-1]") {
			t.Fatalf("expected footer to contain session name, got %q", view)
		}
	})

	t.Run("Prefix pending footer", func(t *testing.T) {
		mPrefix := m
		mPrefix.terminalPrefix = termview.NewPrefixMachine()
		// Fake key to put machine in pending state
		_ = mPrefix.terminalPrefix.ProcessKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})

		view := mPrefix.View()
		if !strings.Contains(view, "[PREFIX Ctrl+]") {
			t.Fatalf("expected prefix banner in footer, got %q", view)
		}
	})

	t.Run("Exited process footer", func(t *testing.T) {
		mExited := m
		mExited.terminalExited = true

		view := mExited.View()
		if !strings.Contains(view, "Process exited") {
			t.Fatalf("expected process exited banner in footer, got %q", view)
		}
	})

	t.Run("Scrolled history footer", func(t *testing.T) {
		mScroll := m
		ring := termview.NewScrollbackRing(10)
		ring.Push(app.TerminalLine{Index: 0, Cells: []app.TerminalCell{{Text: "history line"}}})
		mScroll.terminalScroll = termview.NewScrollController(ring)
		mScroll.terminalScroll.ScrollUp(1)

		view := mScroll.View()
		if !strings.Contains(view, "[SCROLL Line -1") {
			t.Fatalf("expected scroll banner in footer, got %q", view)
		}
	})
}
