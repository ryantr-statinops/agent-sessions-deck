package terminal

import (
	tea "github.com/charmbracelet/bubbletea"
)

// InputMode specifies terminal modes affecting key encoding.
type InputMode struct {
	// AppCursorKeys indicates DECCKM (Application Cursor Keys mode) is active.
	AppCursorKeys bool
	// AppKeypad indicates DECKPNM (Application Keypad mode) is active.
	AppKeypad bool
}

// EncodeKey translates a Bubble Tea tea.KeyMsg into raw VT byte sequences
// destined for a child PTY.
func EncodeKey(msg tea.KeyMsg, mode InputMode) []byte {
	// Handle control keys and ASCII
	switch msg.Type {
	case tea.KeyCtrlAt:
		return []byte{0x00}
	case tea.KeyCtrlA:
		return []byte{0x01}
	case tea.KeyCtrlB:
		return []byte{0x02}
	case tea.KeyCtrlC:
		return []byte{0x03}
	case tea.KeyCtrlD:
		return []byte{0x04}
	case tea.KeyCtrlE:
		return []byte{0x05}
	case tea.KeyCtrlF:
		return []byte{0x06}
	case tea.KeyCtrlG:
		return []byte{0x07}
	case tea.KeyCtrlH:
		return []byte{0x08}
	case tea.KeyTab: // same as KeyCtrlI
		return []byte{'\t'}
	case tea.KeyCtrlJ:
		return []byte{0x0A}
	case tea.KeyCtrlK:
		return []byte{0x0B}
	case tea.KeyCtrlL:
		return []byte{0x0C}
	case tea.KeyEnter: // same as KeyCtrlM
		return []byte{'\r'}
	case tea.KeyCtrlN:
		return []byte{0x0E}
	case tea.KeyCtrlO:
		return []byte{0x0F}
	case tea.KeyCtrlP:
		return []byte{0x10}
	case tea.KeyCtrlQ:
		return []byte{0x11}
	case tea.KeyCtrlR:
		return []byte{0x12}
	case tea.KeyCtrlS:
		return []byte{0x13}
	case tea.KeyCtrlT:
		return []byte{0x14}
	case tea.KeyCtrlU:
		return []byte{0x15}
	case tea.KeyCtrlV:
		return []byte{0x16}
	case tea.KeyCtrlW:
		return []byte{0x17}
	case tea.KeyCtrlX:
		return []byte{0x18}
	case tea.KeyCtrlY:
		return []byte{0x19}
	case tea.KeyCtrlZ:
		return []byte{0x1A}
	case tea.KeyEsc: // same as KeyCtrlOpenBracket
		return []byte{0x1B}
	case tea.KeyCtrlBackslash:
		return []byte{0x1C}
	case tea.KeyCtrlCloseBracket:
		return []byte{0x1D}
	case tea.KeyCtrlCaret:
		return []byte{0x1E}
	case tea.KeyCtrlUnderscore:
		return []byte{0x1F}
	case tea.KeyBackspace: // same as KeyCtrlQuestionMark
		return []byte{0x7F}
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyRunes:
		if len(msg.Runes) > 0 {
			return []byte(string(msg.Runes))
		}
		return nil
	default:
		return nil
	}
}
