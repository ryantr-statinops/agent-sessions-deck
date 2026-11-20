package terminal

import (
	"bytes"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestEncodeKeyBasicAndControl(t *testing.T) {
	tests := []struct {
		name     string
		msg      tea.KeyMsg
		expected []byte
	}{
		{"Enter", tea.KeyMsg{Type: tea.KeyEnter}, []byte{'\r'}},
		{"Tab", tea.KeyMsg{Type: tea.KeyTab}, []byte{'\t'}},
		{"Backspace", tea.KeyMsg{Type: tea.KeyBackspace}, []byte{0x7F}},
		{"Esc", tea.KeyMsg{Type: tea.KeyEsc}, []byte{0x1B}},
		{"Space", tea.KeyMsg{Type: tea.KeySpace}, []byte{' '}},
		{"Ctrl+C", tea.KeyMsg{Type: tea.KeyCtrlC}, []byte{0x03}},
		{"Ctrl+D", tea.KeyMsg{Type: tea.KeyCtrlD}, []byte{0x04}},
		{"Ctrl+A", tea.KeyMsg{Type: tea.KeyCtrlA}, []byte{0x01}},
		{"Ctrl+Z", tea.KeyMsg{Type: tea.KeyCtrlZ}, []byte{0x1A}},
		{"Ctrl+At", tea.KeyMsg{Type: tea.KeyCtrlAt}, []byte{0x00}},
		{"Ctrl+Backslash", tea.KeyMsg{Type: tea.KeyCtrlBackslash}, []byte{0x1C}},
		{"Ctrl+CloseBracket (Ctrl+])", tea.KeyMsg{Type: tea.KeyCtrlCloseBracket}, []byte{0x1D}},
		{"Ctrl+Caret", tea.KeyMsg{Type: tea.KeyCtrlCaret}, []byte{0x1E}},
		{"Ctrl+Underscore", tea.KeyMsg{Type: tea.KeyCtrlUnderscore}, []byte{0x1F}},
		{"Runes 'hello'", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")}, []byte("hello")},
	}

	mode := InputMode{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EncodeKey(tc.msg, mode)
			if !bytes.Equal(got, tc.expected) {
				t.Fatalf("EncodeKey(%v) = %v, want %v", tc.msg, got, tc.expected)
			}
		})
	}
}
