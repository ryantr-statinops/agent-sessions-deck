package terminal

// FooterHeight defines the vertical rows reserved for the terminal status footer.
const FooterHeight = 1

// ViewportDimensions calculates available columns and rows for child agent PTY
// rendering, reserving FooterHeight rows for ASD status chrome.
func ViewportDimensions(winWidth, winHeight int) (cols, rows int) {
	cols = winWidth
	if cols < 1 {
		cols = 1
	}

	rows = winHeight - FooterHeight
	if rows < 1 {
		rows = 1
	}

	return cols, rows
}
