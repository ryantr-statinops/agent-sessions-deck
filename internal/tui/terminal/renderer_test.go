package terminal

import (
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

func TestRenderLine(t *testing.T) {
	t.Run("Unstyled plain line", func(t *testing.T) {
		cells := []app.TerminalCell{
			{Text: "H", Width: 1},
			{Text: "i", Width: 1},
		}
		line := app.TerminalLine{Index: 0, Cells: cells}
		got := RenderLine(line, 5)
		want := "Hi   "
		if got != want {
			t.Fatalf("RenderLine() = %q, want %q", got, want)
		}
	})

	t.Run("Run-length batching for styled cells", func(t *testing.T) {
		boldStyle := app.TerminalStyle{Attributes: app.TerminalAttrBold}
		italicStyle := app.TerminalStyle{Attributes: app.TerminalAttrItalic}

		cells := []app.TerminalCell{
			{Text: "A", Width: 1, Style: boldStyle},
			{Text: "B", Width: 1, Style: boldStyle}, // Same style, should batch
			{Text: "C", Width: 1, Style: italicStyle},
		}
		line := app.TerminalLine{Index: 0, Cells: cells}
		got := RenderLine(line, 3)

		// Expect bold around "AB", italic around "C"
		expected := "\x1b[1mAB\x1b[0m\x1b[3mC\x1b[0m"
		if got != expected {
			t.Fatalf("RenderLine() = %q, want %q", got, expected)
		}
	})

	t.Run("Padding empty line", func(t *testing.T) {
		line := app.TerminalLine{Index: 0, Cells: nil}
		got := RenderLine(line, 10)
		if got != strings.Repeat(" ", 10) {
			t.Fatalf("RenderLine() = %q, want 10 spaces", got)
		}
	})
}

func TestRenderLineWithCursor(t *testing.T) {
	line := app.TerminalLine{
		Index: 0,
		Cells: []app.TerminalCell{
			{Text: "A", Width: 1},
			{Text: "B", Width: 1},
			{Text: "C", Width: 1},
		},
	}

	t.Run("Block cursor on B", func(t *testing.T) {
		got := RenderLineWithCursor(line, 3, 1, app.TerminalCursorBlock)
		// Expect 'A', then reverse video on 'B' (\x1b[7mB\x1b[0m), then 'C'
		expected := "A\x1b[7mB\x1b[0mC"
		if got != expected {
			t.Fatalf("RenderLineWithCursor() = %q, want %q", got, expected)
		}
	})

	t.Run("Underline cursor on C", func(t *testing.T) {
		got := RenderLineWithCursor(line, 3, 2, app.TerminalCursorUnderline)
		expected := "AB\x1b[4mC\x1b[0m"
		if got != expected {
			t.Fatalf("RenderLineWithCursor() = %q, want %q", got, expected)
		}
	})
}

func TestRenderScreen(t *testing.T) {
	screen := app.TerminalScreen{
		Columns: 4,
		Rows:    2,
		Lines: []app.TerminalLine{
			{Index: 0, Cells: []app.TerminalCell{{Text: "X", Width: 1}, {Text: "Y", Width: 1}}},
		},
		Cursor: app.TerminalCursor{
			X:       1,
			Y:       0,
			Visible: true,
			Shape:   app.TerminalCursorBlock,
		},
	}

	rendered := RenderScreen(screen, 4, 2)
	lines := strings.Split(rendered, "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	// Line 0 has cursor on Y: "X\x1b[7mY\x1b[0m  "
	expectedLine0 := "X\x1b[7mY\x1b[0m  "
	if lines[0] != expectedLine0 {
		t.Fatalf("line 0 = %q, want %q", lines[0], expectedLine0)
	}
}

func TestRenderScreenRows(t *testing.T) {
	screen := app.TerminalScreen{
		Columns: 4,
		Rows:    2,
		Lines: []app.TerminalLine{
			{Index: 0, Cells: []app.TerminalCell{{Text: "X", Width: 1}, {Text: "Y", Width: 1}}},
		},
	}

	rows := RenderScreenRows(screen, 4, 2)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0] != "XY  " {
		t.Fatalf("row 0 = %q, want %q", rows[0], "XY  ")
	}
	if rows[1] != "    " {
		t.Fatalf("row 1 = %q, want 4 spaces", rows[1])
	}
}
