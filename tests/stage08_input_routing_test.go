package tests

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/tui"
)

type mockSubscriptionIO struct {
	app.TerminalSubscription
	writes     [][]byte
	closeCount int
}

func (m *mockSubscriptionIO) Write(p []byte) (int, error) {
	cp := make([]byte, len(p))
	copy(cp, p)
	m.writes = append(m.writes, cp)
	return len(p), nil
}

func (m *mockSubscriptionIO) Close() error {
	m.closeCount++
	return nil
}

func (m *mockSubscriptionIO) Resize(cols, rows int) error {
	return nil
}

func (m *mockSubscriptionIO) InitialSnapshot() app.TerminalSnapshot {
	return app.TerminalSnapshot{}
}

func (m *mockSubscriptionIO) ReadFrame() (app.TerminalFrame, error) {
	return app.TerminalFrame{}, nil
}

func TestTerminalView_InputRouting(t *testing.T) {
	sub := &mockSubscriptionIO{}
	m := tui.NewModel(tui.Options{})
	// Switch to ViewTerminal with active subscription
	openMsg := tui.SessionOpenResultMsg{
		SessionID: "sess-1",
		Result: app.OpenResult{
			Terminal: sub,
		},
	}
	up, _ := m.Update(openMsg)
	mTerminal := up.(tui.Model)

	// Test 1: ASCII keystrokes are forwarded to PTY
	upKey, _ := mTerminal.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h', 'i'}})
	mTerminal = upKey.(tui.Model)
	if len(sub.writes) == 0 || string(sub.writes[len(sub.writes)-1]) != "hi" {
		t.Fatalf("expected 'hi' written to PTY, got %+v", sub.writes)
	}

	// Test 2: Arrow keys are encoded as VT sequences
	upArrow, _ := mTerminal.Update(tea.KeyMsg{Type: tea.KeyUp})
	mTerminal = upArrow.(tui.Model)
	lastWrite := string(sub.writes[len(sub.writes)-1])
	if lastWrite != "\x1b[A" {
		t.Fatalf("expected Up arrow sequence '\\x1b[A', got %q", lastWrite)
	}

	// Test 3: Enter key is encoded as \r
	upEnter, _ := mTerminal.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = upEnter.(tui.Model)
	lastWrite = string(sub.writes[len(sub.writes)-1])
	if lastWrite != "\r" {
		t.Fatalf("expected Enter sequence '\\r', got %q", lastWrite)
	}
}

func TestTerminalView_ISIGCtrlC_Isolation(t *testing.T) {
	sub := &mockSubscriptionIO{}
	m := tui.NewModel(tui.Options{})
	openMsg := tui.SessionOpenResultMsg{
		SessionID: "sess-isig",
		Result: app.OpenResult{
			Terminal: sub,
		},
	}
	up, _ := m.Update(openMsg)
	mTerminal := up.(tui.Model)

	// In terminal view, Ctrl+C must forward 0x03 to PTY and NOT quit
	upC, cmd := mTerminal.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	_ = upC.(tui.Model)

	if cmd != nil {
		t.Fatal("expected nil command on Ctrl+C in terminal view, got non-nil cmd")
	}
	if len(sub.writes) == 0 {
		t.Fatal("expected write to PTY on Ctrl+C")
	}
	lastWrite := sub.writes[len(sub.writes)-1]
	if len(lastWrite) != 1 || lastWrite[0] != 0x03 {
		t.Fatalf("expected 0x03 written to PTY on Ctrl+C, got %+v", lastWrite)
	}

	// In contrast, in dashboard view, Ctrl+C requests quit
	mDash := tui.NewModel(tui.Options{})
	_, dashCmd := mDash.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if dashCmd == nil {
		t.Fatal("expected quit cmd on Ctrl+C in dashboard view")
	}
}

func TestTerminalView_PrefixActions(t *testing.T) {
	sub := &mockSubscriptionIO{}
	m := tui.NewModel(tui.Options{})
	openMsg := tui.SessionOpenResultMsg{
		SessionID: "sess-prefix",
		Result: app.OpenResult{
			Terminal: sub,
		},
	}
	up, _ := m.Update(openMsg)
	mTerminal := up.(tui.Model)

	// 1. Send Ctrl+]
	up1, _ := mTerminal.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	mPending := up1.(tui.Model)

	// Verify no write to PTY for prefix trigger
	writeCountBefore := len(sub.writes)

	// 2. Send 's' to detach
	up2, _ := mPending.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	_ = up2.(tui.Model)

	if len(sub.writes) != writeCountBefore {
		t.Fatalf("expected no writes during prefix detach, got new writes: %+v", sub.writes[writeCountBefore:])
	}
	if sub.closeCount != 1 {
		t.Fatalf("expected subscription to be closed once on detach, got %d", sub.closeCount)
	}

	// 3. Literal byte: Ctrl+] followed by Ctrl+] writes 0x1D to PTY
	sub2 := &mockSubscriptionIO{}
	m2 := tui.NewModel(tui.Options{})
	upOpen2, _ := m2.Update(tui.SessionOpenResultMsg{
		SessionID: "sess-literal",
		Result: app.OpenResult{
			Terminal: sub2,
		},
	})
	mTerm2 := upOpen2.(tui.Model)

	upLit1, _ := mTerm2.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	mTermLit1 := upLit1.(tui.Model)
	upLit2, _ := mTermLit1.Update(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	_ = upLit2.(tui.Model)

	if len(sub2.writes) != 1 || len(sub2.writes[0]) != 1 || sub2.writes[0][0] != 0x1D {
		t.Fatalf("expected literal 0x1D written to PTY, got %+v", sub2.writes)
	}
}
