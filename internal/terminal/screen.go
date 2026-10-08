package terminal

import (
	"errors"
	"image/color"
	"io"
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

const (
	MaxColumns         = 512
	MaxRows            = 256
	MaxScreenCells     = 1 << 16
	MaxScrollbackLines = 10000
)

var ErrSubscriberExists = errors.New("terminal screen already has an interactive subscriber")

// Screen stores the current terminal state and emits bounded, sequence-numbered
// screen updates. Every emulator operation that touches screen state is serialized
// under mu; the PTY drain is the only writer.
type Screen struct {
	mu sync.Mutex

	emu           *vt.Emulator
	columns       int
	rows          int
	sequence      uint64
	lines         []app.TerminalLine
	subscriber    *ScreenSubscription
	closed        bool
	finished      bool
	cursorVisible atomic.Bool
	cursorShape   atomic.Uint32
	cursorBlink   atomic.Bool
}

// ScreenSubscription is one reader's bounded incremental screen stream.
type ScreenSubscription struct {
	screen *Screen

	mu            sync.Mutex
	cond          *sync.Cond
	readMu        sync.Mutex
	initial       app.TerminalSnapshot
	lastSequence  uint64
	frame         app.TerminalFrame
	hasFrame      bool
	needsSnapshot bool
	closed        bool
	finished      bool
}

// NewScreen constructs a VT emulator with a bounded screen and scrollback.
func NewScreen(columns, rows, scrollbackLines int) (*Screen, error) {
	if err := ValidateSize(columns, rows); err != nil {
		return nil, err
	}
	if scrollbackLines < 1 || scrollbackLines > MaxScrollbackLines {
		return nil, errors.New("scrollback line limit must be between 1 and 10000")
	}

	s := &Screen{columns: columns, rows: rows, lines: make([]app.TerminalLine, rows)}
	s.cursorVisible.Store(true)
	s.cursorBlink.Store(true)
	s.emu = vt.NewEmulator(columns, rows)
	s.emu.SetScrollbackSize(scrollbackLines)
	s.emu.SetCallbacks(vt.Callbacks{
		CursorVisibility: func(visible bool) { s.cursorVisible.Store(visible) },
		CursorStyle: func(style vt.CursorStyle, blink bool) {
			s.cursorShape.Store(uint32(cursorShape(style)))
			s.cursorBlink.Store(blink)
		},
	})
	s.lines = s.readAllLinesLocked()
	return s, nil
}

// ValidateSize bounds both dimensions and total visible cells before an emulator
// allocation or PTY resize.
func ValidateSize(columns, rows int) error {
	if columns < 1 || columns > MaxColumns || rows < 1 || rows > MaxRows || columns*rows > MaxScreenCells {
		return errors.New("terminal size must be positive, at most 512 columns by 256 rows, and at most 65536 cells")
	}
	return nil
}

// Feed parses every PTY byte, even when no screen subscriber is attached. Screen
// frames may be coalesced; parser input is never dropped.
func (s *Screen) Feed(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.finished {
		return 0, io.ErrClosedPipe
	}
	n, err := s.emu.Write(data)
	if err != nil || n == 0 {
		return n, err
	}
	s.sequence++

	sub := s.subscriber
	if sub != nil {
		sub.mu.Lock()
		defer sub.mu.Unlock()
	}
	canQueue := sub != nil && !sub.closed && !sub.needsSnapshot && !sub.hasFrame
	changed := make([]app.TerminalLine, 0)
	for y := range s.rows {
		previous := s.lines[y].Cells
		var current []app.TerminalCell
		for x := range s.columns {
			cell := terminalCell(s.emu.CellAt(x, y))
			if current == nil && cell != previous[x] {
				current = make([]app.TerminalCell, s.columns)
				copy(current, previous[:x])
			}
			if current != nil {
				current[x] = cell
			}
		}
		if current != nil {
			line := app.TerminalLine{Index: y, Cells: current}
			s.lines[y] = line
			if canQueue {
				changed = append(changed, cloneLine(line))
			}
		}
	}

	if sub == nil || sub.closed || sub.needsSnapshot {
		return n, nil
	}
	if sub.hasFrame {
		sub.frame = app.TerminalFrame{}
		sub.hasFrame = false
		sub.needsSnapshot = true
		sub.cond.Signal()
		return n, nil
	}
	delta := &app.TerminalDelta{
		BaseSequence:    s.sequence - 1,
		Columns:         s.columns,
		Rows:            s.rows,
		Lines:           changed,
		Cursor:          s.cursorLocked(),
		AlternateScreen: s.emu.IsAltScreen(),
		ScrollbackLines: s.emu.ScrollbackLen(),
	}
	sub.frame = app.TerminalFrame{Sequence: s.sequence, Delta: delta}
	sub.hasFrame = true
	sub.cond.Signal()
	return n, nil
}

// Resize changes the emulator dimensions and requires subscribers to resync with
// a full snapshot. The caller updates the OS PTY size before calling Resize.
func (s *Screen) Resize(columns, rows int) error {
	if err := ValidateSize(columns, rows); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.finished {
		return io.ErrClosedPipe
	}
	if s.columns == columns && s.rows == rows {
		return nil
	}
	s.emu.Resize(columns, rows)
	s.columns, s.rows = columns, rows
	s.lines = s.readAllLinesLocked()
	s.sequence++
	if sub := s.subscriber; sub != nil {
		sub.mu.Lock()
		if !sub.closed {
			sub.frame = app.TerminalFrame{}
			sub.hasFrame = false
			sub.needsSnapshot = true
			sub.cond.Signal()
		}
		sub.mu.Unlock()
	}
	return nil
}

// Snapshot returns a copy of the current screen at one sequence boundary.
func (s *Screen) Snapshot() app.TerminalSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

// Finish marks PTY output complete while preserving the final screen for snapshots.
// Pending frames are delivered before readers receive EOF.
func (s *Screen) Finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.finished {
		return
	}
	s.finished = true
	if sub := s.subscriber; sub != nil {
		sub.mu.Lock()
		if !sub.closed {
			sub.finished = true
			sub.cond.Broadcast()
		}
		sub.mu.Unlock()
	}
}

// Subscribe registers one screen reader and returns the snapshot captured at the
// same sequence boundary. A second writer is refused; V1 leases are exclusive.
func (s *Screen) Subscribe() (*ScreenSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, io.ErrClosedPipe
	}
	if s.subscriber != nil {
		return nil, ErrSubscriberExists
	}
	initial := s.snapshotLocked()
	sub := &ScreenSubscription{screen: s, initial: initial, lastSequence: s.sequence, finished: s.finished}
	sub.cond = sync.NewCond(&sub.mu)
	s.subscriber = sub
	return sub, nil
}

// Close closes the screen and releases its active subscription. It does not own
// the PTY descriptor or send a process signal.
func (s *Screen) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.finished = true
	s.closed = true
	if sub := s.subscriber; sub != nil {
		sub.mu.Lock()
		sub.closed = true
		sub.frame = app.TerminalFrame{}
		sub.hasFrame = false
		sub.needsSnapshot = false
		sub.cond.Broadcast()
		sub.mu.Unlock()
		s.subscriber = nil
	}
	return s.emu.Close()
}

// InitialSnapshot returns the immutable screen captured atomically with attach.
func (sub *ScreenSubscription) InitialSnapshot() app.TerminalSnapshot {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	return cloneSnapshot(sub.initial)
}

// ReadFrame blocks for the next ordered delta, or a full snapshot when the bounded
// one-frame queue coalesced output while the reader was behind.
func (sub *ScreenSubscription) ReadFrame() (app.TerminalFrame, error) {
	sub.readMu.Lock()
	defer sub.readMu.Unlock()
	for {
		sub.mu.Lock()
		if sub.closed {
			sub.mu.Unlock()
			return app.TerminalFrame{}, io.EOF
		}
		if sub.needsSnapshot {
			sub.mu.Unlock()
			return sub.snapshotFrame()
		}
		if sub.hasFrame {
			frame := sub.frame
			sub.frame = app.TerminalFrame{}
			sub.hasFrame = false
			if frame.Delta != nil && frame.Delta.BaseSequence != sub.lastSequence {
				sub.needsSnapshot = true
				sub.mu.Unlock()
				continue
			}
			sub.lastSequence = frame.Sequence
			sub.mu.Unlock()
			return frame, nil
		}
		if sub.finished {
			sub.mu.Unlock()
			return app.TerminalFrame{}, io.EOF
		}
		sub.cond.Wait()
		sub.mu.Unlock()
	}
}

// Close unregisters the reader without closing the screen or the PTY.
func (sub *ScreenSubscription) Close() error {
	s := sub.screen
	s.mu.Lock()
	defer s.mu.Unlock()
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed {
		return nil
	}
	sub.closed = true
	sub.frame = app.TerminalFrame{}
	sub.hasFrame = false
	sub.needsSnapshot = false
	sub.cond.Broadcast()
	if s.subscriber == sub {
		s.subscriber = nil
	}
	return nil
}

func (sub *ScreenSubscription) snapshotFrame() (app.TerminalFrame, error) {
	s := sub.screen
	s.mu.Lock()
	defer s.mu.Unlock()
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.closed || s.closed {
		return app.TerminalFrame{}, io.EOF
	}
	snapshot := s.snapshotLocked()
	sub.frame = app.TerminalFrame{}
	sub.hasFrame = false
	sub.needsSnapshot = false
	sub.lastSequence = s.sequence
	return app.TerminalFrame{Sequence: s.sequence, Snapshot: &snapshot}, nil
}

func (s *Screen) snapshotLocked() app.TerminalSnapshot {
	lines := make([]app.TerminalLine, len(s.lines))
	for i, line := range s.lines {
		lines[i] = cloneLine(line)
	}
	return app.TerminalSnapshot{
		Sequence: s.sequence,
		Screen: app.TerminalScreen{
			Columns:         s.columns,
			Rows:            s.rows,
			Lines:           lines,
			Cursor:          s.cursorLocked(),
			AlternateScreen: s.emu.IsAltScreen(),
			ScrollbackLines: s.emu.ScrollbackLen(),
		},
	}
}

func (s *Screen) readAllLinesLocked() []app.TerminalLine {
	lines := make([]app.TerminalLine, s.rows)
	for y := range s.rows {
		cells := make([]app.TerminalCell, s.columns)
		for x := range s.columns {
			cells[x] = terminalCell(s.emu.CellAt(x, y))
		}
		lines[y] = app.TerminalLine{Index: y, Cells: cells}
	}
	return lines
}

func (s *Screen) cursorLocked() app.TerminalCursor {
	position := s.emu.CursorPosition()
	shape := app.TerminalCursorShape(s.cursorShape.Load())
	return app.TerminalCursor{
		X:       position.X,
		Y:       position.Y,
		Visible: s.cursorVisible.Load(),
		Shape:   shape,
		Blink:   s.cursorBlink.Load(),
	}
}

func terminalCell(cell *uv.Cell) app.TerminalCell {
	if cell == nil {
		return app.TerminalCell{}
	}
	return app.TerminalCell{
		Text:      cell.Content,
		Width:     cell.Width,
		Style:     terminalStyle(cell.Style),
		Hyperlink: cell.Link.URL,
	}
}

func terminalStyle(style uv.Style) app.TerminalStyle {
	return app.TerminalStyle{
		Foreground:     terminalColor(style.Fg),
		Background:     terminalColor(style.Bg),
		UnderlineColor: terminalColor(style.UnderlineColor),
		Underline:      terminalUnderline(style.Underline),
		Attributes:     app.TerminalAttributes(style.Attrs),
	}
}

func terminalColor(c color.Color) app.TerminalColor {
	if c == nil {
		return app.TerminalColor{}
	}
	nrgba := color.NRGBAModel.Convert(c).(color.NRGBA)
	return app.TerminalColor{Red: nrgba.R, Green: nrgba.G, Blue: nrgba.B, Alpha: nrgba.A, Set: true}
}

func terminalUnderline(style uv.Underline) app.TerminalUnderlineStyle {
	switch style {
	case uv.UnderlineSingle:
		return app.TerminalUnderlineSingle
	case uv.UnderlineDouble:
		return app.TerminalUnderlineDouble
	case uv.UnderlineCurly:
		return app.TerminalUnderlineCurly
	case uv.UnderlineDotted:
		return app.TerminalUnderlineDotted
	case uv.UnderlineDashed:
		return app.TerminalUnderlineDashed
	default:
		return app.TerminalUnderlineNone
	}
}

func cursorShape(style vt.CursorStyle) app.TerminalCursorShape {
	switch style {
	case vt.CursorUnderline:
		return app.TerminalCursorUnderline
	case vt.CursorBar:
		return app.TerminalCursorBar
	default:
		return app.TerminalCursorBlock
	}
}

func cloneSnapshot(snapshot app.TerminalSnapshot) app.TerminalSnapshot {
	copy := snapshot
	copy.Screen.Lines = make([]app.TerminalLine, len(snapshot.Screen.Lines))
	for i, line := range snapshot.Screen.Lines {
		copy.Screen.Lines[i] = cloneLine(line)
	}
	return copy
}

func cloneLine(line app.TerminalLine) app.TerminalLine {
	return app.TerminalLine{Index: line.Index, Cells: append([]app.TerminalCell(nil), line.Cells...)}
}
