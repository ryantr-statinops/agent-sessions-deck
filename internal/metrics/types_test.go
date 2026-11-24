package metrics

import (
	"testing"
	"time"
)

func TestProcessMetricsMethods(t *testing.T) {
	var zero ProcessMetrics
	if !zero.IsZero() {
		t.Fatalf("expected zero.IsZero() to be true")
	}
	if zero.Active() {
		t.Fatalf("expected zero.Active() to be false")
	}

	active := ProcessMetrics{
		SampledAt:      time.Now().UTC(),
		PID:            1234,
		State:          "S",
		CPUPercent:     2.5,
		MemoryRSSBytes: 1024 * 1024,
		TreeRSSBytes:   2048 * 1024,
		ProcessCount:   2,
	}

	if active.IsZero() {
		t.Fatalf("expected active.IsZero() to be false")
	}
	if !active.Active() {
		t.Fatalf("expected active.Active() to be true for state 'S'")
	}

	dead := active
	dead.State = "Z" // Zombie
	if dead.Active() {
		t.Fatalf("expected dead.Active() to be false for state 'Z'")
	}

	unavail := active
	unavail.Unavailable = true
	if unavail.Active() {
		t.Fatalf("expected unavail.Active() to be false when Unavailable is true")
	}
}
