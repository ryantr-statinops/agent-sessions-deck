package terminal

import (
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

// RenderLine renders one app.TerminalLine into an ANSI-styled string with run-length
// style batching.
func RenderLine(line app.TerminalLine, maxCols int) string {
	return RenderLineWithCursor(line, maxCols, -1, app.TerminalCursorBlock)
}

// RenderLineWithCursor renders one app.TerminalLine, applying a virtual cursor at cursorX
// if cursorX >= 0.
func RenderLineWithCursor(line app.TerminalLine, maxCols int, cursorX int, cursorShape app.TerminalCursorShape) string {
	cells := line.Cells
	if len(cells) == 0 && maxCols > 0 {
		cells = make([]app.TerminalCell, maxCols)
	}

	// Ensure cells array covers at least up to cursorX if cursor is on this line
	if cursorX >= len(cells) && (maxCols <= 0 || cursorX < maxCols) {
		extended := make([]app.TerminalCell, cursorX+1)
		copy(extended, cells)
		cells = extended
	}

	var sb strings.Builder
	col := 0
	i := 0

	for i < len(cells) && (maxCols <= 0 || col < maxCols) {
		// Special handling for the cursor cell
		if col == cursorX {
			cell := cells[i]
			txt := cell.Text
			if txt == "" {
				txt = " "
			}
			w := cell.Width
			if w <= 0 {
				w = 1
			}

			// Render cursor with inverted style or underline
			switch cursorShape {
			case app.TerminalCursorUnderline:
				sb.WriteString("\x1b[4m" + txt + ResetSGR())
			case app.TerminalCursorBar:
				sb.WriteString("\x1b[7m|" + ResetSGR())
			case app.TerminalCursorBlock:
				fallthrough
			default:
				sb.WriteString("\x1b[7m" + txt + ResetSGR())
			}

			col += w
			i++
			continue
		}

		startCell := cells[i]
		runStyle := startCell.Style
		sgr := StyleToSGR(runStyle)

		if sgr != "" {
			sb.WriteString(sgr)
		}

		// Gather contiguous cells with the identical style until cursor or style change
		for i < len(cells) && (maxCols <= 0 || col < maxCols) {
			if col == cursorX {
				break
			}

			cell := cells[i]
			if cell.Style != runStyle {
				break
			}

			txt := cell.Text
			if txt == "" {
				txt = " "
			}
			w := cell.Width
			if w <= 0 {
				w = 1
			}

			if maxCols > 0 && col+w > maxCols {
				break
			}

			sb.WriteString(txt)
			col += w
			i++
		}

		if sgr != "" {
			sb.WriteString(ResetSGR())
		}
	}

	// Pad remaining columns if line is shorter than maxCols
	if maxCols > 0 && col < maxCols {
		sb.WriteString(strings.Repeat(" ", maxCols-col))
	}

	return sb.String()
}

// RenderScreen renders the full app.TerminalScreen into an ANSI-formatted string.
func RenderScreen(screen app.TerminalScreen, maxCols, maxRows int) string {
	rows := make([]string, maxRows)
	lineCount := len(screen.Lines)

	cursorVisible := screen.Cursor.Visible
	cursorX := -1
	cursorY := -1
	if cursorVisible {
		cursorX = screen.Cursor.X
		cursorY = screen.Cursor.Y
	}

	for y := 0; y < maxRows; y++ {
		curX := -1
		if y == cursorY {
			curX = cursorX
		}

		if y < lineCount {
			rows[y] = RenderLineWithCursor(screen.Lines[y], maxCols, curX, screen.Cursor.Shape)
		} else {
			emptyLine := app.TerminalLine{Index: y}
			rows[y] = RenderLineWithCursor(emptyLine, maxCols, curX, screen.Cursor.Shape)
		}
	}

	return strings.Join(rows, "\n")
}

// RenderScreenRows renders visible rows from app.TerminalScreen into a slice of strings.
func RenderScreenRows(screen app.TerminalScreen, maxCols, maxRows int) []string {
	rows := make([]string, maxRows)
	lineCount := len(screen.Lines)

	for y := 0; y < maxRows; y++ {
		if y < lineCount {
			rows[y] = RenderLine(screen.Lines[y], maxCols)
		} else {
			rows[y] = strings.Repeat(" ", maxCols)
		}
	}

	return rows
}
