//go:build linux

package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestExitAltScreen(t *testing.T) {
	var buf bytes.Buffer
	err := ExitAltScreen(&buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "\x1b[?1049l" {
		t.Fatalf("expected \\x1b[?1049l, got %q", buf.String())
	}
}

func TestShowCursor(t *testing.T) {
	var buf bytes.Buffer
	err := ShowCursor(&buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "\x1b[?25h" {
		t.Fatalf("expected \\x1b[?25h, got %q", buf.String())
	}
}

func TestResetHostTerminal(t *testing.T) {
	var buf bytes.Buffer
	err := ResetHostTerminal(&buf, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "\x1b[?1049l") || !strings.Contains(out, "\x1b[?25h") {
		t.Fatalf("expected alt-screen exit and show-cursor in output, got %q", out)
	}
}

func TestWithTerminalRecovery(t *testing.T) {
	var buf bytes.Buffer
	recovered := false

	func() {
		defer func() {
			if r := recover(); r != nil {
				recovered = true
			}
		}()

		WithTerminalRecovery(&buf, 0, nil, func() {
			panic("intentional test panic")
		})
	}()

	if !recovered {
		t.Fatal("expected panic to be re-thrown")
	}

	out := buf.String()
	if !strings.Contains(out, "\x1b[?1049l") || !strings.Contains(out, "\x1b[?25h") {
		t.Fatalf("expected reset sequences to be written on panic, got %q", out)
	}
}

func TestInstallSignalCleanupCancel(t *testing.T) {
	var buf bytes.Buffer
	cancel := InstallSignalCleanup(&buf, 0, nil)
	// Cancel should terminate the background signal watcher cleanly
	cancel()
}
