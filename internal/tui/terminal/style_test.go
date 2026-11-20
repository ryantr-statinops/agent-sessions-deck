package terminal

import (
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

func TestStyleToSGR(t *testing.T) {
	tests := []struct {
		name     string
		style    app.TerminalStyle
		expected string
	}{
		{
			"Default empty style",
			app.TerminalStyle{},
			"",
		},
		{
			"Bold and Italic",
			app.TerminalStyle{
				Attributes: app.TerminalAttrBold | app.TerminalAttrItalic,
			},
			"\x1b[1;3m",
		},
		{
			"Underline and Reverse",
			app.TerminalStyle{
				Underline:  app.TerminalUnderlineSingle,
				Attributes: app.TerminalAttrReverse,
			},
			"\x1b[7;4m",
		},
		{
			"RGB Foreground",
			app.TerminalStyle{
				Foreground: app.TerminalColor{Red: 255, Green: 128, Blue: 0, Set: true},
			},
			"\x1b[38;2;255;128;0m",
		},
		{
			"RGB Background",
			app.TerminalStyle{
				Background: app.TerminalColor{Red: 0, Green: 64, Blue: 128, Set: true},
			},
			"\x1b[48;2;0;64;128m",
		},
		{
			"Combined Full Style",
			app.TerminalStyle{
				Attributes: app.TerminalAttrBold | app.TerminalAttrStrikethrough,
				Underline:  app.TerminalUnderlineSingle,
				Foreground: app.TerminalColor{Red: 200, Green: 200, Blue: 200, Set: true},
				Background: app.TerminalColor{Red: 10, Green: 20, Blue: 30, Set: true},
			},
			"\x1b[1;9;4;38;2;200;200;200;48;2;10;20;30m",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StyleToSGR(tc.style)
			if got != tc.expected {
				t.Fatalf("StyleToSGR(%+v) = %q, want %q", tc.style, got, tc.expected)
			}
		})
	}

	if ResetSGR() != "\x1b[0m" {
		t.Fatalf("expected ResetSGR() to be \\x1b[0m, got %q", ResetSGR())
	}
}
