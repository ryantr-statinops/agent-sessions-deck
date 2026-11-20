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
