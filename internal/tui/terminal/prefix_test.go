package terminal

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPrefixMachineTransitions(t *testing.T) {
	pm := NewPrefixMachine()
	if pm.IsPending() {
		t.Fatal("expected newly created PrefixMachine to not be pending")
	}

	// Normal key should not trigger prefix
	res := pm.ProcessKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if res.Handled || pm.IsPending() {
		t.Fatal("expected normal key to not be handled or set pending")
	}

	// Ctrl+] triggers pending
	res = pm.ProcessKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
	if !res.Handled || !pm.IsPending() {
		t.Fatal("expected Ctrl+] to be handled and set pending to true")
	}

	// Reset clears pending
	pm.Reset()
	if pm.IsPending() {
		t.Fatal("expected Reset to clear pending state")
	}
}

func TestPrefixMachineActions(t *testing.T) {
	tests := []struct {
		name         string
		actionKey    tea.KeyMsg
		expectAction PrefixAction
		expectLit    byte
		expectHandle bool
	}{
		{"Detach s", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")}, ActionDetach, 0, true},
		{"Detach uppercase S", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")}, ActionDetach, 0, true},
		{"New session n", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")}, ActionNew, 0, true},
		{"Help ?", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")}, ActionHelp, 0, true},
		{"Scroll [", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[")}, ActionScroll, 0, true},
		{"Literal Ctrl+]", tea.KeyMsg{Type: tea.KeyCtrlCloseBracket}, ActionSendLiteral, 0x1D, true},
		{"Fallback invalid key 'x'", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}, ActionNone, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pm := NewPrefixMachine()
			// Step 1: trigger prefix
			res := pm.ProcessKey(tea.KeyMsg{Type: tea.KeyCtrlCloseBracket})
			if !res.Handled || !pm.IsPending() {
				t.Fatal("failed to enter pending prefix state")
			}

			// Step 2: send action key
			res = pm.ProcessKey(tc.actionKey)
			if pm.IsPending() {
				t.Fatal("expected pending state to be cleared after action key")
			}
			if res.Handled != tc.expectHandle {
				t.Fatalf("expected Handled=%v, got %v", tc.expectHandle, res.Handled)
			}
			if res.Action != tc.expectAction {
				t.Fatalf("expected Action=%v, got %v", tc.expectAction, res.Action)
			}
			if res.LiteralByte != tc.expectLit {
				t.Fatalf("expected LiteralByte=%x, got %x", tc.expectLit, res.LiteralByte)
			}
		})
	}
}
