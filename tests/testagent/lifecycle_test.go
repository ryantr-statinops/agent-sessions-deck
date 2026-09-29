//go:build linux && integration

package testagent

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/ryantr-statinops/agent-sessions-deck/tests/testagent/control"
)

// floodLine rebuilds the fixture's deterministic record for index i. The test
// derives the expected byte count itself rather than trusting the fixture's
// reported total.
func floodLine(i int) string {
	return fmt.Sprintf("ASD-TESTAGENT-FLOOD-%08d-0123456789abcdefghijklmnopqrstuvwxyz\n", i)
}

// unicodeSampleNames is the fixed sample order the fixture must emit.
var unicodeSampleNames = []string{
	"ascii", "cjk", "hiragana", "hangul", "emoji-scalar",
	"emoji-zwj", "combining", "box-drawing", "rtl", "halfwidth-kana",
}

// resize changes the PTY window size, which is what makes the kernel deliver
// SIGWINCH to the fixture's process group.
func (f *fx) resize(rows, cols uint16) {
	f.t.Helper()
	if err := pty.Setsize(f.ptmx, &pty.Winsize{Rows: rows, Cols: cols}); err != nil {
		f.t.Fatalf("pty.Setsize %dx%d: %v", rows, cols, err)
	}
}

// awaitTerminalContains polls the drained terminal output until want appears
// and returns the full stream. The poll is bounded and exists only because the
// drain runs in its own goroutine; every ordering decision still comes from a
// control event.
func (f *fx) awaitTerminalContains(want string) string {
	f.t.Helper()
	deadline := time.Now().Add(eventTimeout)
	for {
		got := f.terminal()
		if strings.Contains(got, want) {
			return got
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("terminal output never contained %q; got %d bytes: %q",
				want, len(got), truncate(got, 400))
		}
		time.Sleep(time.Millisecond)
	}
}

// awaitTerminalBytes waits until at least n bytes have been drained. If the
// drain had to discard bytes the byte-count assertions downstream would be
// meaningless, so that is a failure rather than a silent truncation.
func (f *fx) awaitTerminalBytes(n int) string {
	f.t.Helper()
	deadline := time.Now().Add(eventTimeout)
	for {
		f.mu.Lock()
		got := f.term.String()
		overrun := f.overrun
		f.mu.Unlock()
		if overrun {
			f.t.Fatalf("drain discarded terminal output past its %d byte bound", drainChunkCap)
		}
		if len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("terminal produced %d bytes, want at least %d", len(got), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// openRawPTY creates a pty pair with the slave already in raw mode.
func openRawPTY() (ptmx, slave *os.File, err error) {
	ptmx, slave, err = pty.Open()
	if err != nil {
		return nil, nil, err
	}
	if err := pty.Setsize(slave, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		_ = ptmx.Close()
		_ = slave.Close()
		return nil, nil, err
	}
	if err := setRaw(slave); err != nil {
		_ = ptmx.Close()
		_ = slave.Close()
		return nil, nil, err
	}
	return ptmx, slave, nil
}

// fixtureCommand builds a fixture invocation sharing the pty and control channel
// the test opened. It is used for the negative case that never becomes a fx.
func fixtureCommand(t *testing.T, mode, label string, extra []string, ctl, slave *os.File) *exec.Cmd {
	t.Helper()
	args := append([]string{"--mode=" + mode, "--label=" + label}, extra...)
	cmd := exec.Command(fixtureBin, args...)
	cmd.Env = os.Environ()
	isolateFixtureCommand(t, cmd)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.ExtraFiles = []*os.File{ctl}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	return cmd
}

// TestChildIdentity proves the fixture reports a real descendant and that the
// reported identity matches the kernel, with the child in the fixture's own
// process group.
func TestChildIdentity(t *testing.T) {
	f := start(t, "child", withLabel("ch"))
	f.ready()
	f.startSize(24, 80)

	child := f.ident("child")
	if child.PID == f.pid {
		t.Fatalf("child pid %d equals the fixture pid", child.PID)
	}
	if child.PPID != f.pid {
		t.Fatalf("child ppid = %d, want the fixture pid %d", child.PPID, f.pid)
	}
	if child.PGID != f.pid {
		t.Fatalf("child pgid = %d, want the fixture pgid %d", child.PGID, f.pid)
	}

	// Verify the claim against procfs rather than trusting the fixture.
	id, err := readProcIdentity(t, child.PID)
	if err != nil {
		t.Fatalf("read child identity: %v", err)
	}
	if id.PPID != f.pid {
		t.Fatalf("procfs says child ppid = %d, fixture reported %d", id.PPID, child.PPID)
	}
	if id.PGID != f.pid {
		t.Fatalf("procfs says child pgid = %d, fixture reported %d", id.PGID, child.PGID)
	}

	f.kind(control.EventStarted)
	ev, _ := f.waitExit(0)
	if ev.Code != 0 {
		t.Fatalf("exit code = %d, want 0", ev.Code)
	}

	// The fixture reaps its own descendants, so the child must be gone.
	if !waitGone(child.PID, exitTimeout) {
		t.Fatalf("child pid %d outlived the fixture", child.PID)
	}
}

// TestGrandchildIdentity proves the two-level process tree is real: the
// grandchild's parent is the child, not the fixture, and the fixture reaps the
// whole tree on exit.
func TestGrandchildIdentity(t *testing.T) {
	f := start(t, "grandchild", withLabel("gc"))
	f.ready()
	f.startSize(24, 80)

	child := f.ident("child")
	grand := f.ident("grandchild")

	if child.PID == f.pid || grand.PID == f.pid {
		t.Fatalf("descendant pids must differ from the fixture pid %d", f.pid)
	}
	if child.PPID != f.pid {
		t.Fatalf("child ppid = %d, want %d", child.PPID, f.pid)
	}
	if grand.PPID != child.PID {
		t.Fatalf("grandchild ppid = %d, want the child pid %d", grand.PPID, child.PID)
	}
	if grand.PID == child.PID {
		t.Fatalf("grandchild and child share pid %d", child.PID)
	}

	// The whole tree must share the fixture's session and process group, which
	// is what proves the pty placed the fixture in its own session.
	for _, tc := range []struct {
		role string
		pid  int
		ppid int
	}{{
		role: "child", pid: child.PID, ppid: f.pid,
	}, {
		role: "grandchild", pid: grand.PID, ppid: child.PID,
	}} {
		id, err := readProcIdentity(t, tc.pid)
		if err != nil {
			t.Fatalf("read identity for %d: %v", tc.pid, err)
		}
		if id.PPID != tc.ppid {
			t.Fatalf("%s pid %d ppid = %d, want %d", tc.role, tc.pid, id.PPID, tc.ppid)
		}
		if id.PGID != f.pid {
			t.Fatalf("%s pid %d pgid = %d, want the fixture pgid %d", tc.role, tc.pid, id.PGID, f.pid)
		}
		if id.SID != f.pid {
			t.Fatalf("%s pid %d sid = %d, want the fixture session %d", tc.role, tc.pid, id.SID, f.pid)
		}
	}

	f.kind(control.EventStarted)
	ev, _ := f.waitExit(0)
	if ev.Code != 0 {
		t.Fatalf("exit code = %d, want 0", ev.Code)
	}

	for _, pid := range []int{child.PID, grand.PID} {
		if !waitGone(pid, exitTimeout) {
			t.Fatalf("descendant pid %d outlived the fixture", pid)
		}
	}
}

// TestAncestorChainReaped proves every descendant the fixture created is gone
// once the fixture itself is gone, in both the child and grandchild shapes.
func TestAncestorChainReaped(t *testing.T) {
	for _, mode := range []string{"child", "grandchild"} {
		t.Run(mode, func(t *testing.T) {
			f := start(t, mode, withLabel("reap-"+mode))
			f.ready()
			f.startSize(24, 80)
			child := f.ident("child")
			pids := []int{child.PID}
			if mode == "grandchild" {
				pids = append(pids, f.ident("grandchild").PID)
			}
			f.kind(control.EventStarted)

			// While the fixture runs, every descendant must still be alive.
			// Helpers ignore SIGHUP and SIGTERM, so a survivor here cannot be
			// explained by incidental pty teardown.
			for _, pid := range pids {
				if err := syscall.Kill(pid, 0); err != nil {
					t.Fatalf("%s: descendant %d is not alive while the fixture runs: %v", mode, pid, err)
				}
			}

			if ev, _ := f.waitExit(0); ev.Code != 0 {
				t.Fatalf("exit code = %d, want 0", ev.Code)
			}
			for _, pid := range pids {
				if !waitGone(pid, exitTimeout) {
					t.Fatalf("%s: pid %d survived the fixture", mode, pid)
				}
			}
		})
	}
}

// TestExitBySignal proves a signal death is reported as such, distinguishing it
// from a requested exit status.
func TestExitBySignal(t *testing.T) {
	f := start(t, "size", withLabel("sig"))
	f.ready()
	f.startSize(24, 80)
	f.kind(control.EventStarted)

	if err := f.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal fixture: %v", err)
	}
	info := f.shutdown()
	if !info.exited {
		t.Fatal("fixture did not exit after SIGTERM")
	}
	if !info.signaled {
		t.Fatalf("process was not reported as signalled: %+v", info)
	}
	if info.signal != syscall.SIGTERM {
		t.Fatalf("terminating signal = %v, want SIGTERM", info.signal)
	}
	if info.exitCode != -1 {
		t.Fatalf("signalled exit code = %d, want -1", info.exitCode)
	}
}

// TestExitByRequestedStatus proves a status can be requested over the control
// channel and reaches the process exactly.
func TestExitByRequestedStatus(t *testing.T) {
	for _, code := range []int{0, 1, 5, 17, 64} {
		t.Run("code-"+strconv.Itoa(code), func(t *testing.T) {
			f := start(t, "size", withLabel("st"))
			f.ready()
			f.startSize(24, 80)
			f.kind(control.EventStarted)
			f.command(control.Command{Cmd: control.CmdExit, Code: code})
			ev, ps := f.waitExit(0)
			if ev.Code != code {
				t.Fatalf("exit event code = %d, want %d", ev.Code, code)
			}
			if ev.Signal != "" {
				t.Fatalf("exit event reported signal %q, want none", ev.Signal)
			}
			if ps.ExitCode() != code {
				t.Fatalf("process exit code = %d, want %d", ps.ExitCode(), code)
			}
		})
	}
}

// TestFixtureRejectsUnusableControlChannel proves fd 3 must be a socket rather
// than merely an inherited open descriptor.
func TestFixtureRejectsUnusableControlChannel(t *testing.T) {
	cmd := exec.Command(fixtureBin, "--mode=echo", "--label=orphan")
	isolateFixtureCommand(t, cmd)
	cmd.Stdin = strings.NewReader("")
	controlFile, err := os.CreateTemp(t.TempDir(), "not-control-")
	if err != nil {
		t.Fatalf("create invalid control descriptor: %v", err)
	}
	defer controlFile.Close()
	cmd.ExtraFiles = []*os.File{controlFile}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err = cmd.Run()
	if err == nil {
		t.Fatalf("fixture without a control channel exited successfully: %s", out.String())
	}
	if !strings.Contains(out.String(), "control descriptor") {
		t.Fatalf("output = %q, want it to mention the missing control descriptor", out.String())
	}
}

// TestFixtureRequiresMode proves the fixture refuses to start without a mode.
func TestFixtureRequiresMode(t *testing.T) {
	cmd := exec.Command(fixtureBin)
	isolateFixtureCommand(t, cmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if err == nil {
		t.Fatalf("fixture without --mode exited successfully: %s", out.String())
	}
	if !strings.Contains(out.String(), "--mode is required") {
		t.Fatalf("output = %q, want it to require --mode", out.String())
	}
}

// TestCleanupLeavesNoFixtureProcessOrPty proves teardown removes both the
// fixture process and its controlling pty, for a fixture that still has live
// descendants at teardown time.
func TestCleanupLeavesNoFixtureProcessOrPty(t *testing.T) {
	for _, mode := range modes {
		if mode == "exit-code" {
			continue // exits before teardown, nothing to clean up
		}
		t.Run(mode, func(t *testing.T) {
			f := start(t, mode, withLabel("leak-"+mode))
			f.ready()
			f.startSize(24, 80)
			if mode == "slow-start" {
				f.command(control.Command{Cmd: control.CmdGo})
			}
			f.kind(control.EventStarted)
			// Capture the pty path while the fixture is alive.
			slave := f.slave()
			if slave == "" {
				t.Fatal("could not resolve the fixture pty path")
			}
			if _, err := os.Stat(slave); err != nil {
				t.Fatalf("pty %s should exist while the fixture runs: %v", slave, err)
			}
			pid := f.pid

			info := f.shutdown()
			if !info.exited {
				t.Fatalf("fixture pid %d did not exit during teardown", pid)
			}
			if info.stillAlive {
				t.Fatalf("fixture pid %d is still alive after teardown", pid)
			}

			if id, err := readProcIdentity(t, pid); err == nil && id.StartTime == f.registryKey.startTime {
				t.Fatalf("fixture process identity still present after teardown: %+v", id)
			}
			if current, err := os.Stat(slave); err == nil && os.SameFile(f.slaveInfo, current) {
				t.Fatalf("fixture pty %s still present after teardown", slave)
			}
			for _, p := range info.pids[1:] {
				if !waitGone(p, exitTimeout) {
					t.Fatalf("descendant pid %d survived teardown", p)
				}
			}
		})
	}
}
