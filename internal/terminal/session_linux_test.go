//go:build linux

package terminal

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/pty"
	domain "github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestTerminalAgentProcess(t *testing.T) {
	if os.Getenv("ASD_TERMINAL_AGENT_HELPER") != "1" {
		t.Skip("subprocess fake-agent fixture")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if want := os.Getenv("ASD_TERMINAL_AGENT_CWD"); cwd != want {
		t.Fatalf("child cwd = %q, want %q", cwd, want)
	}
	setTTYMode(t, "raw", "-echo")

	winch := make(chan os.Signal, 1)
	signalDone := make(chan struct{})
	signalStop := make(chan struct{})
	signal.Notify(winch, syscall.SIGWINCH)
	go func() {
		defer close(signalDone)
		select {
		case <-winch:
			_, _ = fmt.Fprintln(os.Stdout, "WINCH")
		case <-signalStop:
		}
	}()
	defer func() {
		signal.Stop(winch)
		close(signalStop)
		<-signalDone
	}()

	_, _ = fmt.Fprintln(os.Stdout, "READY")
	_, _ = os.Stdout.Write([]byte("\x1b[6n"))
	var reply [6]byte
	if _, err := io.ReadFull(os.Stdin, reply[:]); err != nil {
		t.Fatalf("read DSR reply: %v", err)
	}
	if string(reply[:]) != "\x1b[2;6R" {
		t.Fatalf("DSR reply = %q, want cursor report", reply[:])
	}
	_, _ = fmt.Fprintln(os.Stdout, "QUERY_OK")

	var raw [3]byte
	if _, err := io.ReadFull(os.Stdin, raw[:]); err != nil {
		t.Fatalf("read raw input: %v", err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "RAW:%02x%02x%02x\n", raw[0], raw[1], raw[2])
	setTTYMode(t, "echo", "icanon", "isig", "opost", "onlcr", "icrnl")
	_, _ = fmt.Fprintln(os.Stdout, "LINE_READY")
	reader := bufio.NewReader(os.Stdin)
	first, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read first line input: %v", err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "LINE1:%s\n", strings.TrimSpace(first))
	second, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read second line input: %v", err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "LINE2:%s\n", strings.TrimSpace(second))
}

func TestTerminalSessionRawInputResizeAndDetach(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("ASD_TERMINAL_AGENT_HELPER", "1")
	t.Setenv("ASD_TERMINAL_AGENT_CWD", cwd)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixture := newSessionFixture(t, binary, []string{"-test.run=^TestTerminalAgentProcess$"}, cwd, DefaultOptions())
	sub := fixture.subscribe(t, "holder-one")

	waitScreenContains(t, fixture.session, "QUERY_OK")
	if _, err := sub.Write([]byte{'A', 0x03, 'B'}); err != nil {
		t.Fatalf("Write(raw input) error = %v", err)
	}
	waitScreenContains(t, fixture.session, "RAW:410342")

	if err := sub.Resize(100, 30); err != nil {
		t.Fatalf("Resize(100,30) error = %v", err)
	}
	waitScreenContains(t, fixture.session, "WINCH")
	if got := fixture.session.screen.Snapshot().Screen; got.Columns != 100 || got.Rows != 30 {
		t.Fatalf("screen dimensions after resize = %dx%d, want 100x30", got.Columns, got.Rows)
	}
	waitScreenContains(t, fixture.session, "LINE_READY")
	if _, err := sub.Write([]byte("one\n")); err != nil {
		t.Fatalf("Write(first line) error = %v", err)
	}
	if got := waitRawMarker(t, sub, "LINE1:one"); !strings.Contains(got, "LINE1:one") {
		t.Fatalf("raw stream = %q, want first line acknowledgement", got)
	}

	pid := fixture.child.PID()
	if err := sub.Close(); err != nil {
		t.Fatalf("Close(detach) error = %v", err)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("detaching terminated child %d: %v", pid, err)
	}
	reattached := fixture.subscribe(t, "holder-two")
	if got := snapshotText(reattached.InitialSnapshot().Screen); !strings.Contains(got, "LINE1:one") {
		t.Fatalf("reattach snapshot lost prior screen: %q", got)
	}
	if _, err := reattached.Write([]byte("two\n")); err != nil {
		t.Fatalf("Write(second line) error = %v", err)
	}
	if got := waitRawMarker(t, reattached, "LINE2:two"); !strings.Contains(got, "LINE2:two") {
		t.Fatalf("reattached raw stream = %q, want second line acknowledgement", got)
	}
	_ = reattached.Close()
	fixture.finish(t)
}

func TestTerminalSessionAnswersQueriesWhileDetached(t *testing.T) {
	cwd := t.TempDir()
	script := "stty raw -echo\n" +
		"printf '\\033[6n'\n" +
		"reply=$(dd bs=1 count=6 2>/dev/null | od -An -t x1 | tr -d ' \\n')\n" +
		"if [ \\\"$reply\\\" = \\\"1b5b313b3152\\\" ]; then printf 'QUERY_REPLY_OK\\n'; else printf 'QUERY_REPLY_BAD:%s\\n' \\\"$reply\\\"; fi"
	fixture := newSessionFixture(t, "/bin/sh", []string{"-c", script}, cwd, DefaultOptions())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fixture.session.WaitOutput(ctx); err != nil {
		t.Fatalf("WaitOutput() error = %v", err)
	}
	if err := fixture.waitChild(); err != nil {
		t.Fatalf("child Wait() error = %v", err)
	}
	if err := fixture.child.Close(); err != nil {
		t.Fatalf("PTY Close() error = %v", err)
	}
	sub := fixture.subscribe(t, "late-reader")
	if got := snapshotText(sub.InitialSnapshot().Screen); !strings.Contains(got, "QUERY_REPLY_OK") {
		t.Fatalf("detached terminal snapshot = %q, want QUERY_REPLY_OK", got)
	}
	_ = sub.Close()
	fixture.remove(t)
}

func TestTerminalSessionBoundsRawOutputAndResynchronizesScreen(t *testing.T) {
	cwd := t.TempDir()
	script := `IFS= read -r _; yes x | head -c 8192`
	options := DefaultOptions()
	options.MaxRawOutputBytes = 1024
	fixture := newSessionFixture(t, "/bin/sh", []string{"-c", script}, cwd, options)
	sub := fixture.subscribe(t, "flood-reader")
	if _, err := sub.Write([]byte{103, 111, 10}); err != nil {
		t.Fatalf("Write(flood trigger) error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fixture.session.WaitOutput(ctx); err != nil {
		t.Fatalf("WaitOutput() error = %v", err)
	}
	if _, err := sub.Read(make([]byte, 256)); !errors.Is(err, app.ErrTerminalOutputGap) {
		t.Fatalf("raw Read() after flood error = %v, want ErrTerminalOutputGap", err)
	}
	frame, err := sub.ReadFrame()
	if err != nil {
		t.Fatalf("ReadFrame() after flood error = %v", err)
	}
	if frame.Snapshot == nil || frame.Sequence < 2 || !strings.Contains(snapshotText(frame.Snapshot.Screen), "x") {
		t.Fatalf("flood resync frame = %+v, want latest snapshot containing output", frame)
	}
	_ = sub.Close()
	fixture.finish(t)
}

func setTTYMode(t *testing.T, modes ...string) {
	t.Helper()
	command := exec.Command("stty", modes...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		t.Fatalf("stty %v: %v", modes, err)
	}
}

func newSessionFixture(t *testing.T, executable string, args []string, cwd string, options Options) *sessionFixture {
	t.Helper()
	size := pty.Size{Columns: 80, Rows: 24}
	child, err := pty.Start(executable, args, cwd, size)
	if err != nil {
		t.Fatalf("pty.Start(%q): %v", executable, err)
	}
	manager, err := NewManager(options)
	if err != nil {
		t.Fatalf("NewManager(): %v", err)
	}
	id := domain.ID("terminal-test")
	runtime, err := manager.Register(id, 1, child, size)
	if err != nil {
		t.Fatalf("Manager.Register(): %v", err)
	}
	fixture := &sessionFixture{manager: manager, session: runtime, child: child, id: id}
	t.Cleanup(func() { fixture.cleanup() })
	return fixture
}

type sessionFixture struct {
	manager *Manager
	session *Session
	child   *pty.Child
	id      domain.ID
	waited  bool
	removed bool
}

func (f *sessionFixture) subscribe(t *testing.T, holder string) app.TerminalSubscription {
	t.Helper()
	lease := app.InteractiveLease{
		SessionID:  f.id,
		Generation: 1,
		Holder:     holder,
		AcquiredAt: time.Now().UTC(),
	}
	sub, err := f.manager.Subscribe(context.Background(), lease)
	if err != nil {
		t.Fatalf("Manager.Subscribe(): %v", err)
	}
	return sub
}

func (f *sessionFixture) finish(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.session.WaitOutput(ctx); err != nil {
		t.Fatalf("WaitOutput(): %v", err)
	}
	if err := f.waitChild(); err != nil {
		t.Fatalf("child Wait(): %v", err)
	}
	if err := f.child.Close(); err != nil {
		t.Fatalf("PTY Close(): %v", err)
	}
	f.remove(t)
}

func (f *sessionFixture) waitChild() error {
	if f.waited {
		return nil
	}
	f.waited = true
	return f.child.Wait()
}

func (f *sessionFixture) remove(t *testing.T) {
	t.Helper()
	if f.removed {
		return
	}
	if err := f.manager.Remove(f.id, 1); err != nil {
		t.Fatalf("Manager.Remove(): %v", err)
	}
	f.removed = true
}

func (f *sessionFixture) cleanup() {
	if f.removed {
		return
	}
	if !f.waited {
		pid := f.child.PID()
		if pgid, err := syscall.Getpgid(pid); err == nil && pgid == pid {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
		_ = f.child.Wait()
		f.waited = true
	}
	_ = f.child.Close()
	select {
	case <-f.session.OutputDone():
	case <-time.After(3 * time.Second):
		return
	}
	_ = f.manager.Remove(f.id, 1)
	f.removed = true
}

func waitScreenContains(t *testing.T, s *Session, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(snapshotText(s.screen.Snapshot().Screen), marker) {
			return
		}
		if s.OutputFinished() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("terminal screen never contained %q; final=%q", marker, snapshotText(s.screen.Snapshot().Screen))
}

func waitRawMarker(t *testing.T, sub app.TerminalSubscription, marker string) string {
	t.Helper()
	type result struct {
		text string
		err  error
	}
	ready := make(chan result, 1)
	go func() {
		var output strings.Builder
		buffer := make([]byte, 4096)
		for {
			n, err := sub.Read(buffer)
			if n > 0 {
				output.Write(buffer[:n])
			}
			if strings.Contains(output.String(), marker) {
				ready <- result{text: output.String()}
				return
			}
			if err != nil {
				ready <- result{text: output.String(), err: err}
				return
			}
		}
	}()
	select {
	case got := <-ready:
		if got.err != nil {
			t.Fatalf("raw Read() ended before %q: %v; output=%q", marker, got.err, got.text)
		}
		return got.text
	case <-time.After(5 * time.Second):
		_ = sub.Close()
		got := <-ready
		t.Fatalf("raw Read() did not reach %q; output=%q err=%v", marker, got.text, got.err)
		return ""
	}
}

func snapshotText(screen app.TerminalScreen) string {
	var output strings.Builder
	for _, line := range screen.Lines {
		for _, cell := range line.Cells {
			output.WriteString(cell.Text)
		}
		output.WriteByte('\n')
	}
	return output.String()
}

func TestTerminalSessionResizeDoesNotReuseDetachedLease(t *testing.T) {
	cwd := t.TempDir()
	options := DefaultOptions()
	options.ResizeDebounce = 250 * time.Millisecond
	fixture := newSessionFixture(t, "/bin/sh", []string{"-c", "stty raw -echo; exec sleep 60"}, cwd, options)
	first := fixture.subscribe(t, "first-holder")
	firstResult := make(chan error, 1)
	firstStarted := make(chan struct{})
	go func() {
		close(firstStarted)
		firstResult <- first.Resize(90, 25)
	}()
	<-firstStarted
	time.Sleep(20 * time.Millisecond)
	if err := first.Close(); err != nil {
		t.Fatalf("Close(first subscription) error = %v", err)
	}
	second := fixture.subscribe(t, "second-holder")
	secondResult := make(chan error, 1)
	go func() { secondResult <- second.Resize(110, 31) }()
	select {
	case err := <-firstResult:
		if !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("first resize error = %v, want closed lease", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first resize did not finish")
	}
	select {
	case err := <-secondResult:
		if err != nil {
			t.Fatalf("second resize error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second resize did not finish")
	}
	if got := fixture.session.screen.Snapshot().Screen; got.Columns != 110 || got.Rows != 31 {
		t.Fatalf("screen dimensions after reattach resize = %dx%d, want 110x31", got.Columns, got.Rows)
	}
	_ = second.Close()
}
