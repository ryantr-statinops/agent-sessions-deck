//go:build linux && integration

package tests

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	termview "github.com/ryantr-statinops/agent-sessions-deck/internal/tui/terminal"
)

func TestMemoryAllocationBoundsUnderWorkload(t *testing.T) {
	runtime.GC()
	var m1, m2 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// Create a scrollback ring buffer bounded at 10,000 lines
	capacity := 10000
	ring := termview.NewScrollbackRing(capacity)

	boldStyle := app.TerminalStyle{Attributes: app.TerminalAttrBold}

	// Push 50,000 lines through the 10,000-capacity ring buffer
	for i := 0; i < 50000; i++ {
		cells := make([]app.TerminalCell, 80)
		for c := 0; c < 80; c++ {
			cells[c] = app.TerminalCell{
				Text:  "X",
				Width: 1,
				Style: boldStyle,
			}
		}
		ring.Push(app.TerminalLine{Index: i, Cells: cells})
	}

	if ring.Len() > capacity {
		t.Fatalf("ring buffer exceeded max capacity: %d > %d", ring.Len(), capacity)
	}

	runtime.GC()
	runtime.ReadMemStats(&m2)

	// HeapAlloc delta in megabytes
	allocMB := float64(m2.HeapAlloc-m1.HeapAlloc) / (1024 * 1024)
	if m2.HeapAlloc < m1.HeapAlloc {
		allocMB = 0
	}

	// Budget: Memory growth for 10,000 retained screen lines should be strictly < 100 MB (well below 612MB budget)
	if allocMB > 100.0 {
		t.Fatalf("heap allocation grew by %.2f MB, exceeded 100MB budget (total allowance 612MB)", allocMB)
	}

	// Verify line retrieval remains fast and intact
	line, ok := ring.Get(0)
	if !ok || len(line.Cells) != 80 {
		t.Fatalf("expected valid line at index 0, got %v, cells=%d", ok, len(line.Cells))
	}

}

func BenchmarkScrollbackRingPush(b *testing.B) {
	ring := termview.NewScrollbackRing(10000)
	dummy := app.TerminalLine{
		Cells: []app.TerminalCell{{Text: "Z", Width: 1}},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ring.Push(dummy)
	}
	_ = fmt.Sprintf("%d", ring.Len())
}
