package terminal

import (
	tea "github.com/charmbracelet/bubbletea"
)

// PrefixAction identifies an action triggered by a shortcut following the prefix.
type PrefixAction int

const (
	ActionNone PrefixAction = iota
	ActionDetach
	ActionNew
	ActionHelp
	ActionScroll
	ActionSendLiteral
)

// PrefixResult carries the result of processing a key through the prefix state machine.
type PrefixResult struct {
	// Handled is true if the key was consumed by the prefix state machine.
	Handled bool
	// Action is the action triggered by this key event, if any.
	Action PrefixAction
	// LiteralByte is the raw byte to emit when Action is ActionSendLiteral.
	LiteralByte byte
}

// PrefixMachine manages the Ctrl+] prefix state for terminal shortcuts.
type PrefixMachine struct {
	pending bool
}

// NewPrefixMachine creates an initialized PrefixMachine.
func NewPrefixMachine() *PrefixMachine {
	return &PrefixMachine{}
}

// IsPending returns true if the machine is waiting for an action key following Ctrl+].
func (p *PrefixMachine) IsPending() bool {
	return p.pending
}

// Reset clears the pending prefix state.
func (p *PrefixMachine) Reset() {
	p.pending = false
}

// ProcessKey evaluates a key message against the prefix state machine.
func (p *PrefixMachine) ProcessKey(msg tea.KeyMsg) PrefixResult {
	if !p.pending {
		if isPrefixKey(msg) {
			p.pending = true
			return PrefixResult{Handled: true, Action: ActionNone}
		}
		return PrefixResult{Handled: false, Action: ActionNone}
	}

	// Machine is pending an action key
	p.pending = false

	if isPrefixKey(msg) {
		// Ctrl+] pressed twice sends literal 0x1D
		return PrefixResult{Handled: true, Action: ActionSendLiteral, LiteralByte: 0x1D}
	}

	switch msg.String() {
	case "s", "S":
		return PrefixResult{Handled: true, Action: ActionDetach}
	case "n", "N":
		return PrefixResult{Handled: true, Action: ActionNew}
	case "?":
		return PrefixResult{Handled: true, Action: ActionHelp}
	case "[":
		return PrefixResult{Handled: true, Action: ActionScroll}
	default:
		// Unknown key after prefix: cancel pending state and pass through key to PTY
		return PrefixResult{Handled: false, Action: ActionNone}
	}
}

func isPrefixKey(msg tea.KeyMsg) bool {
	return msg.Type == tea.KeyCtrlCloseBracket || msg.String() == "ctrl+]"
}
