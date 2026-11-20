package terminal

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
)

func TestScrollbackRingPushAndBound(t *testing.T) {
	ring := NewScrollbackRing(3)
	if ring.Len() != 0 {
		t.Fatalf("expected initial Len=0, got %d", ring.Len())
	}

	// Push 2 lines
	ring.Push(app.TerminalLine{Index: 0, Cells: []app.TerminalCell{{Text: "line 0"}}})
	ring.Push(app.TerminalLine{Index: 1, Cells: []app.TerminalCell{{Text: "line 1"}}})
	if ring.Len() != 2 {
		t.Fatalf("expected Len=2, got %d", ring.Len())
	}

	// Push 3rd line (reaches max capacity)
	ring.Push(app.TerminalLine{Index: 2, Cells: []app.TerminalCell{{Text: "line 2"}}})
	if ring.Len() != 3 {
		t.Fatalf("expected Len=3, got %d", ring.Len())
	}

	// Push 4th line (should evict line 0)
	ring.Push(app.TerminalLine{Index: 3, Cells: []app.TerminalCell{{Text: "line 3"}}})
	if ring.Len() != 3 {
		t.Fatalf("expected Len=3 after eviction, got %d", ring.Len())
	}

	line0, ok := ring.Get(0)
	if !ok || line0.Cells[0].Text != "line 1" {
		t.Fatalf("expected oldest line to be 'line 1', got %+v", line0)
	}

	line2, ok := ring.Get(2)
	if !ok || line2.Cells[0].Text != "line 3" {
		t.Fatalf("expected newest line to be 'line 3', got %+v", line2)
	}

	// Out of bounds Get
	if _, ok := ring.Get(-1); ok {
		t.Fatal("expected Get(-1) to return false")
	}
	if _, ok := ring.Get(3); ok {
		t.Fatal("expected Get(3) to return false")
	}

	// Clear
	ring.Clear()
	if ring.Len() != 0 {
		t.Fatalf("expected Len=0 after Clear, got %d", ring.Len())
	}
}

func TestScrollbackRingDefaultCapacity(t *testing.T) {
	ring := NewScrollbackRing(0)
	if ring.MaxLines() != DefaultMaxScrollback {
		t.Fatalf("expected DefaultMaxScrollback (%d), got %d", DefaultMaxScrollback, ring.MaxLines())
	}

	for i := 0; i < 50; i++ {
		ring.Push(app.TerminalLine{Index: i, Cells: []app.TerminalCell{{Text: fmt.Sprintf("line %d", i)}}})
	}
	if ring.Len() != 50 {
		t.Fatalf("expected Len=50, got %d", ring.Len())
	}
}

func TestScrollControllerNavigation(t *testing.T) {
	ring := NewScrollbackRing(10)
	for i := 0; i < 5; i++ {
		ring.Push(app.TerminalLine{Index: i, Cells: []app.TerminalCell{{Text: fmt.Sprintf("hist %d", i)}}})
	}

	ctrl := NewScrollController(ring)
	if ctrl.IsScrolled() || ctrl.Offset() != 0 {
		t.Fatal("expected controller to start in live mode (offset=0)")
	}

	// Scroll up 2 lines
	ctrl.ScrollUp(2)
	if !ctrl.IsScrolled() || ctrl.Offset() != 2 {
		t.Fatalf("expected offset=2, got %d", ctrl.Offset())
	}

	// Scroll up beyond history length -> clamps to ring.Len()
	ctrl.ScrollUp(10)
	if ctrl.Offset() != 5 {
		t.Fatalf("expected offset to clamp to 5, got %d", ctrl.Offset())
	}

	// Scroll down 3 lines
	ctrl.ScrollDown(3)
	if ctrl.Offset() != 2 {
		t.Fatalf("expected offset=2, got %d", ctrl.Offset())
	}

	// Scroll down to 0
	ctrl.ScrollDown(10)
	if ctrl.IsScrolled() || ctrl.Offset() != 0 {
		t.Fatalf("expected offset=0, got %d", ctrl.Offset())
	}

	// Alternate screen bypass
	ctrl.ScrollUp(4)
	if ctrl.Offset() != 4 {
		t.Fatalf("expected offset=4 before alt-screen, got %d", ctrl.Offset())
	}
	ctrl.SetAlternateScreen(true)
	if ctrl.IsScrolled() || ctrl.Offset() != 0 {
		t.Fatal("expected alternate screen to force offset=0 and disable scrolling")
	}

	// Attempts to scroll in alternate screen should be ignored
	ctrl.ScrollUp(3)
	if ctrl.IsScrolled() || ctrl.Offset() != 0 {
		t.Fatal("expected scroll in alternate screen to remain at 0")
	}
}

func TestScrollControllerVisibleRows(t *testing.T) {
	ring := NewScrollbackRing(10)
	ring.Push(app.TerminalLine{Index: 0, Cells: []app.TerminalCell{{Text: "old 1"}}})
	ring.Push(app.TerminalLine{Index: 1, Cells: []app.TerminalCell{{Text: "old 2"}}})

	ctrl := NewScrollController(ring)
	screen := app.TerminalScreen{
		Columns: 5,
		Rows:    2,
		Lines: []app.TerminalLine{
			{Index: 0, Cells: []app.TerminalCell{{Text: "cur 1"}}},
			{Index: 1, Cells: []app.TerminalCell{{Text: "cur 2"}}},
		},
	}

	// When not scrolled, returns screen lines
	rows := ctrl.VisibleRows(screen, 5, 2)
	if strings.TrimSpace(rows[0]) != "cur 1" || strings.TrimSpace(rows[1]) != "cur 2" {
		t.Fatalf("expected live rows, got %v", rows)
	}

	// Scroll up 1 line
	ctrl.ScrollUp(1)
	rows = ctrl.VisibleRows(screen, 5, 2)
	// Expect oldest visible line to be "old 2", followed by "cur 1"
	if strings.TrimSpace(rows[0]) != "old 2" || strings.TrimSpace(rows[1]) != "cur 1" {
		t.Fatalf("expected scrolled rows, got %v", rows)
	}
}
