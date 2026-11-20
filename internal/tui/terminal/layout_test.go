package terminal

import (
	"testing"
)

func TestViewportDimensions(t *testing.T) {
	tests := []struct {
		winW, winH   int
		wantW, wantH int
	}{
		{80, 24, 80, 23},
		{120, 40, 120, 39},
		{10, 2, 10, 1},
		{10, 1, 10, 1},  // Clamped rows to min 1
		{0, 0, 1, 1},    // Clamped both to min 1
		{-5, -10, 1, 1}, // Clamped negative dimensions
	}

	for _, tc := range tests {
		gotW, gotH := ViewportDimensions(tc.winW, tc.winH)
		if gotW != tc.wantW || gotH != tc.wantH {
			t.Fatalf("ViewportDimensions(%d, %d) = (%d, %d), want (%d, %d)",
				tc.winW, tc.winH, gotW, gotH, tc.wantW, tc.wantH)
		}
	}
}
