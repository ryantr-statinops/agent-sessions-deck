package terminal

import (
	"fmt"
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
