package terminal

import (
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

// RenderLine renders one app.TerminalLine into an ANSI-styled string with run-length
// style batching. If the line has fewer than maxCols cells, it is padded with spaces.
func RenderLine(line app.TerminalLine, maxCols int) string {
	cells := line.Cells
	if len(cells) == 0 && maxCols > 0 {
		return strings.Repeat(" ", maxCols)
	}

	var sb strings.Builder
	col := 0
	i := 0

	for i < len(cells) && (maxCols <= 0 || col < maxCols) {
		startCell := cells[i]
		runStyle := startCell.Style
		sgr := StyleToSGR(runStyle)

		if sgr != "" {
			sb.WriteString(sgr)
		}

		// Gather contiguous cells with the identical style
		for i < len(cells) && (maxCols <= 0 || col < maxCols) {
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
