//go:build linux && integration

package testagent

import (
	"bytes"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/tests/testagent/control"
)

// TestAllModesStartAndExit covers the whole mode matrix: every mode must
// announce itself with its own pid and mode, and must terminate on an explicit
// request rather than on a timer.
func TestAllModesStartAndExit(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			f := start(t, mode, withLabel("m-"+mode))
			f.ready()
			f.startSize(24, 80)
			if mode == "slow-start" {
				f.command(control.Command{Cmd: control.CmdGo})
			}
			f.kind(control.EventStarted)
			// exit-code terminates on its own; every other mode is stopped
			// with an explicit control command.
			var ev *control.Exit
			var ps *os.ProcessState
			if mode == "exit-code" {
				ev, ps = f.awaitExitOnly()
			} else {
				ev, ps = f.waitExit(0)
			}
			if ev.Code != 0 {
				t.Fatalf("exit event code = %d, want 0", ev.Code)
			}
			if ps.ExitCode() != 0 {
				t.Fatalf("process exit code = %d, want 0", ps.ExitCode())
			}
		})
	}
}

// TestUnknownModeRejected proves the fixture refuses a mode it does not
// implement rather than starting and misbehaving.
func TestUnknownModeRejected(t *testing.T) {
	ptmx, slave, err := openRawPTY()
	if err != nil {
		t.Fatalf("open pty: %v", err)
	}
	defer ptmx.Close()
	ctlParent, ctlChild, err := socketPair()
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	defer ctlParent.Close()
	defer ctlChild.Close()

	cmd := fixtureCommand(t, "no-such-mode", "bad", nil, ctlChild, slave)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err == nil {
		t.Fatal("unknown mode exited successfully, want failure")
	}
	if !strings.Contains(out.String(), "unknown mode") {
		t.Fatalf("output = %q, want it to mention unknown mode", out.String())
	}
}

// TestEchoWritesOnlyTerminalBytes proves terminal output stays on the PTY and
// control output stays on fd 3, with no mixing in either direction.
func TestEchoWritesOnlyTerminalBytes(t *testing.T) {
	f := start(t, "echo", withLabel("e1"))
	f.ready()
	f.startSize(24, 80)
	f.kind(control.EventStarted)

	f.write("hello\n")
	ev := f.kind(control.EventLine)
	ln, ok := ev.(*control.Line)
	if !ok {
		t.Fatalf("line: got %T", ev)
	}
	if ln.Data != "hello" {
		t.Fatalf("line data = %q, want %q", ln.Data, "hello")
	}

	terminal := f.awaitTerminalContains("ECHO e1:hello\r\n")
	if strings.Contains(terminal, `"event"`) || strings.Contains(terminal, `"line"`) {
		t.Fatalf("control JSON leaked to PTY: %q", terminal)
	}
	ex, _ := f.waitExit(0)
	if ex.Code != 0 {
		t.Fatalf("exit code = %d, want 0", ex.Code)
	}
}

// TestLineModeKeepsTerminalSilent proves line events use only the control channel.
func TestLineModeKeepsTerminalSilent(t *testing.T) {
	f := start(t, "line", withLabel("line-only"))
	f.ready()
	f.startSize(24, 80)
	f.kind(control.EventStarted)
	f.write("control-only\n")
	ev := f.kind(control.EventLine)
	if got := ev.(*control.Line).Data; got != "control-only" {
		t.Fatalf("line data = %q, want control-only", got)
	}
	if got := f.terminal(); got != "" {
		t.Fatalf("line mode wrote terminal bytes: %q", got)
	}
	if ex, _ := f.waitExit(0); ex.Code != 0 {
		t.Fatalf("exit code = %d, want 0", ex.Code)
	}
}

// TestSlowStartGatesOnExplicitCommand proves slow-start is released by a
// control command and not by elapsed time.
func TestSlowStartGatesOnExplicitCommand(t *testing.T) {
	f := start(t, "slow-start", withLabel("slow"))
	f.ready()
	f.startSize(24, 80)

	// Nothing may start until the test asks.
	f.silence(shortSilence)

	f.command(control.Command{Cmd: control.CmdGo})
	f.kind(control.EventStarted)

	f.write("after-go\n")
	ev := f.kind(control.EventLine)
	if got := ev.(*control.Line).Data; got != "after-go" {
		t.Fatalf("line data = %q, want %q", got, "after-go")
	}
	f.awaitTerminalContains("ECHO slow:after-go\r\n")

	ex, _ := f.waitExit(0)
	if ex.Code != 0 {
		t.Fatalf("exit code = %d, want 0", ex.Code)
	}
}

// TestRequestedExitCode proves the fixture can be asked to exit with an
// arbitrary status and reports that status on the control channel.
func TestRequestedExitCode(t *testing.T) {
	for _, code := range []int{3, 7, 42} {
		f := start(t, "exit-code", withLabel("ec"),
			withArgs("--exit-code="+itoa(code)))
		f.ready()
		f.startSize(24, 80)
		f.kind(control.EventStarted)
		ev, ps := f.awaitExitOnly()
		if ev.Code != code {
			t.Fatalf("exit event code = %d, want %d", ev.Code, code)
		}
		if ps.ExitCode() != code {
			t.Fatalf("process exit code = %d, want %d", ps.ExitCode(), code)
		}
	}
}

// TestIgnoredTermSurvivesSignal proves the ignored-term mode traps a
// termination signal, reports it, and stays alive to exit on request.
func TestIgnoredTermSurvivesSignal(t *testing.T) {
	f := start(t, "ignored-term", withLabel("ign"))
	f.ready()
	f.startSize(24, 80)
	f.kind(control.EventStarted)

	if err := f.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal fixture: %v", err)
	}
	ev := f.kind(control.EventSignal)
	sig, ok := ev.(*control.Signal)
	if !ok {
		t.Fatalf("signal: got %T", ev)
	}
	if sig.Name != "SIGTERM" {
		t.Fatalf("signal name = %q, want SIGTERM", sig.Name)
	}
	if sig.Count != 1 {
		t.Fatalf("signal count = %d, want 1", sig.Count)
	}

	// The process must still be running: no exit event yet.
	f.silence(shortSilence)

	ex, ps := f.waitExit(0)
	if ex.Code != 0 {
		t.Fatalf("exit code = %d, want 0", ex.Code)
	}
	if ps.ExitCode() != 0 {
		t.Fatalf("process exit code = %d, want 0", ps.ExitCode())
	}
}

// TestOutputFlood proves a bounded burst is delivered in full, in order, and
// that the fixture does not wedge when the reader is attached.
func TestOutputFlood(t *testing.T) {
	f := start(t, "flood", withLabel("fl"), withArgs("--flood-lines=2000"))
	f.ready()
	f.startSize(24, 80)
	f.kind(control.EventStarted)

	ev := f.kind(control.EventFlood)
	fl, ok := ev.(*control.Flood)
	if !ok {
		t.Fatalf("flood: got %T", ev)
	}
	if fl.Lines != 2000 {
		t.Fatalf("flood lines = %d, want 2000", fl.Lines)
	}
	wantBytes := fl.Lines * len(floodLine(0))
	if fl.Bytes != wantBytes {
		t.Fatalf("flood bytes = %d, want %d", fl.Bytes, wantBytes)
	}

	term := f.awaitTerminalBytes(fl.Bytes)
	if got := floodLine(0); !strings.HasPrefix(term, got) {
		t.Fatalf("flood stream does not start with %q", got)
	}
	if got := floodLine(fl.Lines - 1); !strings.HasSuffix(strings.TrimSuffix(term, "\r\n"), got) {
		t.Fatalf("flood stream does not end with the last record %q", got)
	}

	ex, _ := f.waitExit(0)
	if ex.Code != 0 {
		t.Fatalf("exit code = %d, want 0", ex.Code)
	}
}

// TestUnicodeRoundTrip proves multi-byte payloads survive the PTY byte for
// byte, both as fixture output and as input the fixture echoes.
func TestUnicodeRoundTrip(t *testing.T) {
	f := start(t, "unicode", withLabel("uni"))
	f.ready()
	f.startSize(24, 80)
	f.kind(control.EventStarted)

	for i, want := range unicodeSampleNames {
		ev := f.kind(control.EventSample)
		s, ok := ev.(*control.Sample)
		if !ok {
			t.Fatalf("sample %d: got %T", i, ev)
		}
		if s.Name != want {
			t.Fatalf("sample %d name = %q, want %q", i, s.Name, want)
		}
		if s.Size == 0 {
			t.Fatalf("sample %d (%s) reported zero bytes", i, s.Name)
		}
		if len(s.Hex) != 2*s.Size {
			t.Fatalf("sample %d (%s) hex is %d chars for %d bytes", i, s.Name, len(s.Hex), s.Size)
		}
	}
	f.awaitTerminalContains("\u4f60\u597d\u4e16\u754c")
	f.awaitTerminalContains("\U0001F642\U0001F680")

	const in = "mixed \u4f60\u597d \U0001F680 e\u0301"
	f.write(in + "\n")
	ev := f.kind(control.EventLine)
	if got := ev.(*control.Line).Data; got != in {
		t.Fatalf("line data = %q, want %q", got, in)
	}
	f.awaitTerminalContains("ECHO uni:" + in + "\r\n")

	ex, _ := f.waitExit(0)
	if ex.Code != 0 {
		t.Fatalf("exit code = %d, want 0", ex.Code)
	}
}

// TestQueryObservesReplies proves the fixture writes terminal queries to the
// PTY and observes the answers that come back, one event per reply.
func TestQueryObservesReplies(t *testing.T) {
	f := start(t, "query", withLabel("q"))
	f.ready()
	f.startSize(24, 80)
	f.kind(control.EventStarted)

	// The fixture must have written its query sequences to the terminal.
	f.awaitTerminalContains("\x1b[c")
	f.awaitTerminalContains("\x1b[6n")
	f.awaitTerminalContains("\x1b[?2004$p")

	// Feed one reply per query, splitting a reply across writes to prove the
	// scanner is incremental rather than assuming message boundaries.
	f.write("\x1b[?62")
	f.write(";1;6c")
	f.write("\x1b[1;1R")
	f.write("\x1b[?2004;2$y")

	wantKinds := []string{"DA", "DSR", "DECRQM"}
	wantRaw := []string{"\x1b[?62;1;6c", "\x1b[1;1R", "\x1b[?2004;2$y"}
	for i, kind := range wantKinds {
		ev := f.kind(control.EventQuery)
		q, ok := ev.(*control.Query)
		if !ok {
			t.Fatalf("query: got %T", ev)
		}
		if q.Kind != kind {
			t.Fatalf("query %d kind = %q, want %q", i, q.Kind, kind)
		}
		if q.Raw != wantRaw[i] {
			t.Fatalf("query %d raw = %q, want %q", i, q.Raw, wantRaw[i])
		}
	}

	ev, _ := f.waitExit(0)
	if ev.Code != 0 {
		t.Fatalf("exit code = %d, want 0", ev.Code)
	}
}

// TestSizeModeReportsResize proves the size mode reports the start-up size,
// reports every window change, and answers an explicit size request.
func TestSizeModeReportsResize(t *testing.T) {
	f := start(t, "size", withLabel("sz"))
	f.ready()
	f.startSize(24, 80)
	f.kind(control.EventStarted)

	for _, size := range [][2]uint16{{30, 100}, {40, 120}, {24, 80}} {
		f.resize(size[0], size[1])
		ev := f.kindIs(control.EventSize, func(ev control.Event) bool {
			return ev.(*control.Size).Reason == "winch"
		}, "winch size")
		s := ev.(*control.Size)
		if s.Rows != int(size[0]) || s.Cols != int(size[1]) {
			t.Fatalf("winch size = %dx%d, want %dx%d", s.Rows, s.Cols, size[0], size[1])
		}
		f.awaitTerminalContains("SIZE " + itoa(int(size[0])) + " " + itoa(int(size[1])) + "\r\n")
	}

	f.command(control.Command{Cmd: control.CmdGetsize})
	ev := f.kindIs(control.EventSize, func(ev control.Event) bool {
		return ev.(*control.Size).Reason == "getsize"
	}, "getsize")
	if s := ev.(*control.Size); s.Rows != 24 || s.Cols != 80 {
		t.Fatalf("getsize = %dx%d, want 24x80", s.Rows, s.Cols)
	}

	ex, _ := f.waitExit(0)
	if ex.Code != 0 {
		t.Fatalf("exit code = %d, want 0", ex.Code)
	}
}

// TestAnsiFullScreenRepaints proves the full-screen mode enters and leaves the
// alternate screen and repaints a frame for every size it is given.
func TestAnsiFullScreenRepaints(t *testing.T) {
	f := start(t, "ansi", withLabel("fs"))
	f.ready()
	f.startSize(24, 80)
	f.kind(control.EventStarted)

	ev := f.kind(control.EventFrame)
	fr := ev.(*control.Frame)
	if fr.Rows != 24 || fr.Cols != 80 {
		t.Fatalf("first frame = %dx%d, want 24x80", fr.Rows, fr.Cols)
	}
	f.awaitTerminalContains("\x1b[?1049h")

	for _, size := range [][2]uint16{{30, 100}, {50, 120}} {
		f.resize(size[0], size[1])
		f.kindIs(control.EventSize, func(ev control.Event) bool {
			return ev.(*control.Size).Reason == "winch"
		}, "winch size")
		ev := f.kindIs(control.EventFrame, func(ev control.Event) bool {
			fr := ev.(*control.Frame)
			return fr.Rows == int(size[0]) && fr.Cols == int(size[1])
		}, "repainted frame")
		_ = ev
	}

	f.awaitTerminalContains("FRAME fs 1 50x120")
	f.stop(0)
	f.awaitExit()
	f.awaitTerminalContains("\x1b[?1049l")
}

// TestFullFrameContent proves a repaint redraws every row of the screen at the
// new size rather than only a header line.
func TestFullFrameContent(t *testing.T) {
	f := start(t, "ansi", withLabel("rows"), withSize(24, 80))
	f.ready()
	f.startSize(24, 80)
	f.kind(control.EventStarted)
	f.kind(control.EventFrame)

	f.resize(10, 40)
	f.kindIs(control.EventSize, func(ev control.Event) bool {
		return ev.(*control.Size).Reason == "winch"
	}, "winch size")
	ev := f.kindIs(control.EventFrame, func(ev control.Event) bool {
		return ev.(*control.Frame).Rows == 10
	}, "frame at 10 rows")
	if got := ev.(*control.Frame).Cols; got != 40 {
		t.Fatalf("frame cols = %d, want 40", got)
	}
	term := f.awaitTerminalContains("FRAME rows 1 10x40")
	if n := strings.Count(term, "10x40"); n != 10 {
		t.Fatalf("repaint contains %d rows tagged 10x40, want 10", n)
	}
}

// TestUntrustedTermIsIgnored proves the fixture does not consult TERM: two runs
// with different TERM values behave identically.
func TestUntrustedTermIsIgnored(t *testing.T) {
	first := start(t, "size", withLabel("t1"), withEnv("TERM=xterm-256color"))
	first.ready()
	first.startSize(24, 80)
	first.kind(control.EventStarted)
	first.command(control.Command{Cmd: control.CmdGetsize})
	ev := first.kindIs(control.EventSize, func(ev control.Event) bool {
		return ev.(*control.Size).Reason == "getsize"
	}, "getsize with TERM set")
	want := *ev.(*control.Size)
	if _, _ = first.waitExit(0); false {
		t.Fatal("unreachable")
	}

	second := start(t, "size", withLabel("t2"), withEnv("TERM=this-is-not-a-terminal"))
	second.ready()
	second.startSize(24, 80)
	second.kind(control.EventStarted)
	second.command(control.Command{Cmd: control.CmdGetsize})
	ev = second.kindIs(control.EventSize, func(ev control.Event) bool {
		return ev.(*control.Size).Reason == "getsize"
	}, "getsize with TERM unset")
	got := *ev.(*control.Size)
	if got != want {
		t.Fatalf("TERM changed behaviour: %+v vs %+v", got, want)
	}
	if _, _ = second.waitExit(0); false {
		t.Fatal("unreachable")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
