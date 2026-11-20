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
	// Bracketed paste wrapping
	if msg.Paste {
		content := string(msg.Runes)
		res := make([]byte, 0, len(content)+12)
		res = append(res, "\x1b[200~"...)
		res = append(res, content...)
		res = append(res, "\x1b[201~"...)
		return res
	}

	bytes := encodeKeyBase(msg, mode)
	if len(bytes) == 0 {
		return nil
	}

	// Alt modifier prepends ESC (\x1b)
	if msg.Alt {
		res := make([]byte, 1+len(bytes))
		res[0] = 0x1B
		copy(res[1:], bytes)
		return res
	}

	return bytes
}

func encodeKeyBase(msg tea.KeyMsg, mode InputMode) []byte {
	// Handle navigation and editing keys
	switch msg.Type {
	case tea.KeyUp:
		if mode.AppCursorKeys {
			return []byte("\x1bOA")
		}
		return []byte("\x1b[A")
	case tea.KeyDown:
		if mode.AppCursorKeys {
			return []byte("\x1bOB")
		}
		return []byte("\x1b[B")
	case tea.KeyRight:
		if mode.AppCursorKeys {
			return []byte("\x1bOC")
		}
		return []byte("\x1b[C")
	case tea.KeyLeft:
		if mode.AppCursorKeys {
			return []byte("\x1bOD")
		}
		return []byte("\x1b[D")
	case tea.KeyShiftTab:
		return []byte("\x1b[Z")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	}

	// Handle function keys F1-F12
	switch msg.Type {
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	}

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
