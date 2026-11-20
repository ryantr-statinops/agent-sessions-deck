package terminal

import (
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

// DefaultMaxScrollback defines the default line limit for the scrollback ring.
const DefaultMaxScrollback = 10000

// ScrollbackRing is a bounded ring buffer of historical lines that scrolled off
// the visible screen.
type ScrollbackRing struct {
	lines    []app.TerminalLine
	maxLines int
}

// NewScrollbackRing constructs a ring buffer with the specified capacity limit.
func NewScrollbackRing(maxLines int) *ScrollbackRing {
	if maxLines <= 0 {
		maxLines = DefaultMaxScrollback
	}
	return &ScrollbackRing{
		lines:    make([]app.TerminalLine, 0, min(maxLines, 128)),
		maxLines: maxLines,
	}
}

// Push appends a line to the scrollback buffer, evicting the oldest line if full.
func (s *ScrollbackRing) Push(line app.TerminalLine) {
	if s.maxLines <= 0 {
		return
	}

	if len(s.lines) >= s.maxLines {
		// Evict oldest line
		s.lines = s.lines[1:]
	}
	s.lines = append(s.lines, line)
}

// Len returns the number of retained scrollback lines.
func (s *ScrollbackRing) Len() int {
	return len(s.lines)
}

// MaxLines returns the capacity bound.
func (s *ScrollbackRing) MaxLines() int {
	return s.maxLines
}

// Get returns the line at index (0 = oldest, Len()-1 = most recent).
func (s *ScrollbackRing) Get(index int) (app.TerminalLine, bool) {
	if index < 0 || index >= len(s.lines) {
		return app.TerminalLine{}, false
	}
	return s.lines[index], true
}

// Clear clears all stored scrollback lines.
func (s *ScrollbackRing) Clear() {
	s.lines = s.lines[:0]
}

// ScrollController manages viewport offset and alternate-screen bypass.
type ScrollController struct {
	ring            *ScrollbackRing
	offset          int
	alternateScreen bool
}

// NewScrollController constructs a controller bound to a ScrollbackRing.
func NewScrollController(ring *ScrollbackRing) *ScrollController {
	if ring == nil {
		ring = NewScrollbackRing(DefaultMaxScrollback)
	}
	return &ScrollController{
		ring: ring,
	}
}

// SetAlternateScreen updates alternate-screen state. When entering alt screen,
// scroll offset is reset to 0 (live view).
func (c *ScrollController) SetAlternateScreen(alt bool) {
	c.alternateScreen = alt
	if alt {
		c.offset = 0
	}
}

// IsScrolled returns true if the user is viewing historical lines.
func (c *ScrollController) IsScrolled() bool {
	return !c.alternateScreen && c.offset > 0
}

// Offset returns the current scrollback offset.
func (c *ScrollController) Offset() int {
	if c.alternateScreen {
		return 0
	}
	return c.offset
}

// ScrollUp moves viewport back in time by lines.
func (c *ScrollController) ScrollUp(lines int) {
	if c.alternateScreen || lines <= 0 {
		return
	}
	maxOffset := c.ring.Len()
	c.offset += lines
	if c.offset > maxOffset {
		c.offset = maxOffset
	}
}

// ScrollDown moves viewport forward towards live view by lines.
func (c *ScrollController) ScrollDown(lines int) {
	if c.alternateScreen || lines <= 0 {
		return
	}
	c.offset -= lines
	if c.offset < 0 {
		c.offset = 0
	}
}

// ScrollToBottom restores live follow view.
func (c *ScrollController) ScrollToBottom() {
	c.offset = 0
}

// VisibleRows renders rows accounting for scrollback offset when active.
func (c *ScrollController) VisibleRows(screen app.TerminalScreen, maxCols, maxRows int) []string {
	if !c.IsScrolled() {
		return RenderScreenRows(screen, maxCols, maxRows)
	}

	rows := make([]string, maxRows)
	totalHistory := c.ring.Len()
	firstHistoryIdx := totalHistory - c.offset

	for r := 0; r < maxRows; r++ {
		idx := firstHistoryIdx + r
		if idx < 0 {
			// Blank line before earliest history
			rows[r] = strings.Repeat(" ", maxCols)
		} else if idx < totalHistory {
			// Line from historical scrollback ring
			if line, ok := c.ring.Get(idx); ok {
				rows[r] = RenderLine(line, maxCols)
			} else {
				rows[r] = strings.Repeat(" ", maxCols)
			}
		} else {
			// Line from top of current screen
			screenLineIdx := idx - totalHistory
			if screenLineIdx < len(screen.Lines) {
				rows[r] = RenderLine(screen.Lines[screenLineIdx], maxCols)
			} else {
				rows[r] = strings.Repeat(" ", maxCols)
			}
		}
	}

	return rows
}
