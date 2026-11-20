package terminal

import (
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
