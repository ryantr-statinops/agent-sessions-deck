//go:build linux

package tui

import (
	"io"
	"os"
	"os/signal"
	"syscall"

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

// InstallSignalCleanup sets up signal handling for SIGINT, SIGTERM, and SIGHUP.
// If any such signal is received, host terminal state is restored before exiting.
// The returned cancel function stops signal listening.
func InstallSignalCleanup(w io.Writer, fd uintptr, state *term.State) func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	done := make(chan struct{})

	go func() {
		select {
		case sig, ok := <-sigCh:
			if !ok {
				return
			}
			_ = ResetHostTerminal(w, fd, state)
			signal.Stop(sigCh)
			if s, ok := sig.(syscall.Signal); ok {
				_ = syscall.Kill(syscall.Getpid(), s)
			} else {
				os.Exit(1)
			}
		case <-done:
			return
		}
	}()

	return func() {
		close(done)
		signal.Stop(sigCh)
	}
}

// WithTerminalRecovery executes fn and ensures that if a panic occurs, host terminal
// state is restored before re-panicking.
func WithTerminalRecovery(w io.Writer, fd uintptr, state *term.State, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			_ = ResetHostTerminal(w, fd, state)
			panic(r)
		}
	}()
	fn()
}
