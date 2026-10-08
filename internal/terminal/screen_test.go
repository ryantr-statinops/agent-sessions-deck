package terminal

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

func TestScreenFeedEmitsStyledRowsAndOrderedSequence(t *testing.T) {
	screen := newTestScreen(t, 16, 3, 4)
	sub, err := screen.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	defer sub.Close()
	if got := sub.InitialSnapshot(); got.Sequence != 0 || got.Screen.Columns != 16 || got.Screen.Rows != 3 {
		t.Fatalf("initial snapshot = %+v, want empty 16x3 sequence 0", got)
	}

	input := []byte("\x1b[1;31mR")
	if n, err := screen.Feed(input); err != nil || n != len(input) {
		t.Fatalf("Feed() = %d, %v; want %d bytes", n, err, len(input))
	}
	frame, err := sub.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if frame.Sequence != 1 || frame.Snapshot != nil || frame.Delta == nil || frame.Delta.BaseSequence != 0 {
		t.Fatalf("frame = %+v, want delta sequence 1 from 0", frame)
	}
	if len(frame.Delta.Lines) != 1 || frame.Delta.Lines[0].Index != 0 {
		t.Fatalf("changed lines = %+v, want row 0", frame.Delta.Lines)
	}
	cell := frame.Delta.Lines[0].Cells[0]
	if cell.Text != "R" || cell.Width != 1 || cell.Style.Attributes&app.TerminalAttrBold == 0 || !cell.Style.Foreground.Set || cell.Style.Foreground.Red == 0 || cell.Style.Foreground.Green != 0 || cell.Style.Foreground.Blue != 0 {
		t.Fatalf("styled terminal cell = %+v, want bold red R", cell)
	}
	if frame.Delta.Cursor.X != 1 || frame.Delta.Cursor.Y != 0 {
		t.Fatalf("cursor = %+v, want (1,0)", frame.Delta.Cursor)
	}
}

func TestScreenHandlesUTF8AcrossFeedBoundaries(t *testing.T) {
	screen := newTestScreen(t, 8, 2, 2)
	sub, err := screen.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	input := []byte("界")
	if _, err := screen.Feed(input[:1]); err != nil {
		t.Fatal(err)
	}
	if frame, err := sub.ReadFrame(); err != nil || frame.Sequence != 1 {
		t.Fatalf("first partial UTF-8 frame = %+v, %v", frame, err)
	}
	if _, err := screen.Feed(input[1:]); err != nil {
		t.Fatal(err)
	}
	frame, err := sub.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if len(frame.Delta.Lines) != 1 || frame.Delta.Lines[0].Cells[0].Text != "界" || frame.Delta.Lines[0].Cells[0].Width != 2 {
		t.Fatalf("UTF-8 cell update = %+v, want one wide grapheme", frame.Delta)
	}
}

func TestScreenResynchronizesSlowReaderWithLatestSnapshot(t *testing.T) {
	screen := newTestScreen(t, 8, 2, 2)
	sub, err := screen.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	for _, text := range []string{"A", "\rB"} {
		if _, err := screen.Feed([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	frame, err := sub.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Snapshot == nil || frame.Delta != nil || frame.Sequence != 2 {
		t.Fatalf("resync frame = %+v, want latest snapshot at sequence 2", frame)
	}
	if got := lineText(frame.Snapshot.Screen.Lines[0]); !strings.HasPrefix(got, "B") {
		t.Fatalf("resync screen line = %q, want latest B", got)
	}

	if _, err := screen.Feed([]byte("\rC")); err != nil {
		t.Fatal(err)
	}
	next, err := sub.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if next.Delta == nil || next.Delta.BaseSequence != 2 || next.Sequence != 3 {
		t.Fatalf("post-resync frame = %+v, want delta 2→3", next)
	}
}

func TestScreenResizeForcesSnapshotAndValidatesAllocationBounds(t *testing.T) {
	screen := newTestScreen(t, 8, 2, 2)
	sub, err := screen.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if err := screen.Resize(12, 4); err != nil {
		t.Fatalf("Resize() error = %v", err)
	}
	frame, err := sub.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.Snapshot == nil || frame.Sequence != 1 || frame.Snapshot.Screen.Columns != 12 || frame.Snapshot.Screen.Rows != 4 {
		t.Fatalf("resize frame = %+v, want 12x4 snapshot at sequence 1", frame)
	}
	for _, size := range [][2]int{{0, 24}, {513, 1}, {1, 257}, {512, 256}} {
		if err := ValidateSize(size[0], size[1]); err == nil {
			t.Errorf("ValidateSize(%d,%d) succeeded", size[0], size[1])
		}
	}
}

func TestScreenKeepsParsingWhileDetachedAndBoundsScrollback(t *testing.T) {
	screen := newTestScreen(t, 8, 2, 2)
	input := []byte("one\r\ntwo\r\nthree\r\nfour\r\nfive\r\n")
	if _, err := screen.Feed(input); err != nil {
		t.Fatal(err)
	}
	if got := screen.Snapshot().Screen.ScrollbackLines; got != 2 {
		t.Fatalf("scrollback lines = %d, want cap 2", got)
	}
	sub, err := screen.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	got := sub.InitialSnapshot()
	if got.Sequence != 1 || !strings.Contains(lineText(got.Screen.Lines[0]), "five") {
		t.Fatalf("detached snapshot = %+v, want latest output at sequence 1", got)
	}
}

func TestScreenAllowsOnlyOneSubscriberAndCloseUnblocksReader(t *testing.T) {
	screen := newTestScreen(t, 8, 2, 2)
	first, err := screen.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := screen.Subscribe(); !errors.Is(err, ErrSubscriberExists) {
		t.Fatalf("second Subscribe() error = %v, want ErrSubscriberExists", err)
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := first.ReadFrame()
		readDone <- err
	}()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("ReadFrame after Close() error = %v, want EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadFrame did not unblock after subscription close")
	}
	second, err := screen.Subscribe()
	if err != nil {
		t.Fatalf("Subscribe() after close error = %v", err)
	}
	if err := screen.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.ReadFrame(); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadFrame after screen close error = %v, want EOF", err)
	}
}

func TestNewScreenRejectsUnboundedDimensionsAndScrollback(t *testing.T) {
	for _, tc := range []struct {
		columns, rows, scrollback int
	}{
		{columns: 513, rows: 1, scrollback: 10},
		{columns: 512, rows: 256, scrollback: 10},
		{columns: 80, rows: 24, scrollback: MaxScrollbackLines + 1},
	} {
		if _, err := NewScreen(tc.columns, tc.rows, tc.scrollback); err == nil {
			t.Errorf("NewScreen(%d,%d,%d) succeeded", tc.columns, tc.rows, tc.scrollback)
		}
	}
}

func newTestScreen(t *testing.T, columns, rows, scrollback int) *Screen {
	t.Helper()
	screen, err := NewScreen(columns, rows, scrollback)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = screen.Close() })
	return screen
}

func lineText(line app.TerminalLine) string {
	var builder strings.Builder
	for _, cell := range line.Cells {
		builder.WriteString(cell.Text)
	}
	return builder.String()
}
