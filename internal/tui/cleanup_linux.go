//go:build linux

package tui

import (
	"io"

	"github.com/charmbracelet/x/term"
)

// ExitAltScreen writes the ANSI escape sequence to switch back to the normal screen buffer.
func ExitAltScreen(w io.Writer) error {
	if w == nil {
		return nil
	}
	_, err := io.WriteString(w, "\x1b[?1049l")
	return err
}

// ShowCursor writes the ANSI escape sequence to ensure the terminal cursor is visible.
func ShowCursor(w io.Writer) error {
	if w == nil {
		return nil
	}
	_, err := io.WriteString(w, "\x1b[?25h")
	return err
}

// RestoreTermios restores original terminal state if a valid state is provided.
func RestoreTermios(fd uintptr, state *term.State) error {
	if state == nil {
		return nil
	}
	return term.Restore(fd, state)
}

// ResetHostTerminal restores termios state, exits the alternate screen buffer, and restores cursor visibility.
func ResetHostTerminal(w io.Writer, fd uintptr, state *term.State) error {
	var firstErr error
	if err := RestoreTermios(fd, state); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := ExitAltScreen(w); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := ShowCursor(w); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
