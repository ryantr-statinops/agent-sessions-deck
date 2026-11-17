package tui

import (
	"os"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestStatusIndicator(t *testing.T) {
	// Color mode
	sym, text := StatusIndicator(session.LifecycleRunning, false)
	if text != "Running" || sym == "" {
		t.Fatalf("expected Running indicator, got %s, %s", sym, text)
	}

	sym, text = StatusIndicator(session.LifecycleFailed, false)
	if text != "Failed" || sym == "" {
		t.Fatalf("expected Failed indicator, got %s, %s", sym, text)
	}

	// NoColor mode
	sym, text = StatusIndicator(session.LifecycleRunning, true)
	if sym != "[RUN]" || text != "Running" {
		t.Fatalf("expected [RUN] in no-color mode, got %s, %s", sym, text)
	}

	sym, text = StatusIndicator(session.LifecycleFailed, true)
	if sym != "[FAIL]" || text != "Failed" {
		t.Fatalf("expected [FAIL] in no-color mode, got %s, %s", sym, text)
	}
}

func TestTruncatePath(t *testing.T) {
	// Short path
	short := "/tmp/repo"
	if res := TruncatePath(short, 20); res != short {
		t.Fatalf("expected %q, got %q", short, res)
	}

	// Long path truncation
	long := "/home/developer/projects/tools/special-subproject/agent-sessions-deck"
	truncated := TruncatePath(long, 30)
	if len(truncated) > 30 {
		t.Fatalf("expected length <= 30, got %d (%q)", len(truncated), truncated)
	}
}

func TestThemeNoColor(t *testing.T) {
	os.Setenv("NO_COLOR", "1")
	defer os.Unsetenv("NO_COLOR")

	theme := NewTheme()
	if !theme.NoColor {
		t.Fatalf("expected theme.NoColor true when NO_COLOR is set")
	}
}
