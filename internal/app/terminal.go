package app

import "errors"

// ErrTerminalOutputGap means a bounded raw-byte subscriber fell behind. The
// screen-frame stream remains available and resynchronizes with a full snapshot.
var ErrTerminalOutputGap = errors.New("terminal byte stream lost output; resubscribe from a screen snapshot")

// TerminalAttributes are the supported cell text attributes. They are bit flags.
type TerminalAttributes uint8

const (
	TerminalAttrBold TerminalAttributes = 1 << iota
	TerminalAttrFaint
	TerminalAttrItalic
	TerminalAttrBlink
	TerminalAttrRapidBlink
	TerminalAttrReverse
	TerminalAttrConceal
	TerminalAttrStrikethrough
)

// TerminalUnderlineStyle is the cell underline variant.
type TerminalUnderlineStyle uint8

const (
	TerminalUnderlineNone TerminalUnderlineStyle = iota
	TerminalUnderlineSingle
	TerminalUnderlineDouble
	TerminalUnderlineCurly
	TerminalUnderlineDotted
	TerminalUnderlineDashed
)

// TerminalColor is a terminal cell color. Set=false means use the terminal default.
type TerminalColor struct {
	Red   uint8
	Green uint8
	Blue  uint8
	Alpha uint8
	Set   bool
}

// TerminalStyle is the supported style state of one rendered cell.
type TerminalStyle struct {
	Foreground     TerminalColor
	Background     TerminalColor
	UnderlineColor TerminalColor
	Underline      TerminalUnderlineStyle
	Attributes     TerminalAttributes
}

// TerminalCell is one grapheme cluster in a screen row.
type TerminalCell struct {
	Text      string
	Width     int
	Style     TerminalStyle
	Hyperlink string
}

// TerminalCursorShape is the cursor shape requested by the child terminal.
type TerminalCursorShape uint8

const (
	TerminalCursorBlock TerminalCursorShape = iota
	TerminalCursorUnderline
	TerminalCursorBar
)

// TerminalCursor carries the screen cursor state.
type TerminalCursor struct {
	X       int
	Y       int
	Visible bool
	Shape   TerminalCursorShape
	Blink   bool
}

// TerminalLine is one complete screen row. Lines in snapshots are ordered by Index.
type TerminalLine struct {
	Index int
	Cells []TerminalCell
}

// TerminalScreen is the current visible screen and terminal modes.
type TerminalScreen struct {
	Columns         int
	Rows            int
	Lines           []TerminalLine
	Cursor          TerminalCursor
	AlternateScreen bool
	ScrollbackLines int
}

// TerminalSnapshot is an atomic screen state at Sequence.
type TerminalSnapshot struct {
	Sequence uint64
	Screen   TerminalScreen
}

// TerminalDelta applies changed rows and terminal metadata to BaseSequence.
type TerminalDelta struct {
	BaseSequence    uint64
	Columns         int
	Rows            int
	Lines           []TerminalLine
	Cursor          TerminalCursor
	AlternateScreen bool
	ScrollbackLines int
}

// TerminalFrame carries either a full resynchronization snapshot or an ordered
// incremental update. Exactly one of Snapshot and Delta is set.
type TerminalFrame struct {
	Sequence uint64
	Snapshot *TerminalSnapshot
	Delta    *TerminalDelta
}
