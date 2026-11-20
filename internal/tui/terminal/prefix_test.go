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
