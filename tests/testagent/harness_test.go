//go:build linux && integration

package testagent

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/creack/pty"

	"github.com/ryantr-statinops/agent-sessions-deck/tests/testagent/control"
)

// Linux-only harness. Every wait below is deadline bounded and every wait is
// on an explicit event, never on a fixed sleep.

const (
	eventTimeout  = 20 * time.Second
	exitTimeout   = 20 * time.Second
	drainTimeout  = 20 * time.Second
	drainChunkCap = 1 << 20
	shortSilence  = 250 * time.Millisecond
)

// modes is the contract the fixture must satisfy. It is redeclared here rather
// than imported so the test pins the protocol independently of the fixture.
var modes = []string{
	"echo", "line", "ansi", "exit-code", "slow-start", "ignored-term",
	"child", "grandchild", "flood", "unicode", "query", "size",
}

var (
	fixtureBin string

	registryMu sync.Mutex
	registry   = map[processKey]registryEntry{}
)

type processKey struct {
	pid       int
	startTime uint64
}

type registryEntry struct {
	mode        string
	label       string
	slavePath   string
	slaveInfo   os.FileInfo
	descendants []procIdentity
}

// TestMain builds the fixture once and, after the suite, proves that no fixture
// process or PTY survived.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "asd-testagent-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testagent: temp dir: %v\n", err)
		os.Exit(1)
	}
	fixtureBin = filepath.Join(dir, "fakeagent")
	if err := buildFixture(fixtureBin); err != nil {
		_ = os.RemoveAll(dir)
		fmt.Fprintf(os.Stderr, "testagent: build fixture: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	if leaks := leftoverFixtures(); len(leaks) > 0 {
		fmt.Fprintf(os.Stderr, "testagent: fixture processes or PTYs left behind: %s\n", strings.Join(leaks, "; "))
		code = 1
	}
	registryMu.Lock()
	pending := len(registry)
	registryMu.Unlock()
	if pending > 0 {
		fmt.Fprintf(os.Stderr, "testagent: %d fixture cleanup record(s) remain after the suite\n", pending)
		code = 1
	}
	os.Exit(code)
}

// buildFixture compiles the fixture from source. GOPROXY is disabled and the
// module is read-only so the build can neither reach the network nor mutate
// go.mod or go.sum.
func buildFixture(out string) error {
	cmd := exec.Command("go", "build", "-o", out, "./fakeagent")
	cmd.Env = append(os.Environ(),
		"GOPROXY=off",
		"GOFLAGS=-mod=readonly",
	)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// isolateFixtureCommand gives each fixture a private workspace and XDG home.
// Remove parent values first so a fixture or child cannot touch user data.
func isolateFixtureCommand(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	root, err := os.MkdirTemp("", "asd-testagent-case-")
	if err != nil {
		t.Fatalf("create fixture temp root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	workspace := filepath.Join(root, "workspace")
	stateHome := filepath.Join(root, "home")
	for _, dir := range []string{workspace, filepath.Join(stateHome, ".config"), filepath.Join(stateHome, ".local", "state"), filepath.Join(stateHome, ".cache")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create fixture directory %s: %v", dir, err)
		}
	}
	cmd.Dir = workspace
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	filtered := make([]string, 0, len(env)+5)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "HOME", "PWD", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME":
			continue
		}
		filtered = append(filtered, entry)
	}
	cmd.Env = append(filtered,
		"HOME="+stateHome,
		"PWD="+workspace,
		"XDG_CONFIG_HOME="+filepath.Join(stateHome, ".config"),
		"XDG_STATE_HOME="+filepath.Join(stateHome, ".local", "state"),
		"XDG_CACHE_HOME="+filepath.Join(stateHome, ".cache"),
	)
}

// leftoverFixtures re-checks every recorded process identity and controlling PTY.
func leftoverFixtures() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	var out []string
	for key, e := range registry {
		if id, err := readProcIdentity(nil, key.pid); err == nil && id.StartTime == key.startTime {
			out = append(out, fmt.Sprintf("pid %d (%s/%s) still alive", key.pid, e.mode, e.label))
		}
		for _, child := range e.descendants {
			if id, err := readProcIdentity(nil, child.PID); err == nil && id.StartTime == child.StartTime {
				out = append(out, fmt.Sprintf("descendant pid %d (%s/%s) still alive", child.PID, e.mode, e.label))
			}
		}
		if e.slavePath != "" {
			if current, err := os.Stat(e.slavePath); err == nil && e.slaveInfo != nil && os.SameFile(e.slaveInfo, current) {
				out = append(out, fmt.Sprintf("pid %d PTY %s still present", key.pid, e.slavePath))
			}
		}
	}
	return out
}

// options configures a single fixture start.
type options struct {
	label    string
	extra    []string
	rows     uint16
	cols     uint16
	env      []string
	noCTTY   bool
	startPTY bool
}

type option func(*options)

func withLabel(l string) option   { return func(o *options) { o.label = l } }
func withArgs(a ...string) option { return func(o *options) { o.extra = append(o.extra, a...) } }
func withSize(rows, cols uint16) option {
	return func(o *options) { o.rows, o.cols = rows, cols }
}
func withEnv(kv ...string) option { return func(o *options) { o.env = append(o.env, kv...) } }

// fx is a running fixture under test.
type fx struct {
	t       *testing.T
	mode    string
	opts    options
	cmd     *exec.Cmd
	pid     int
	ptmx    *os.File
	control *controlEnd
	reader  *control.Reader
	sender  *control.Emitter

	mu      sync.Mutex
	term    bytes.Buffer
	overrun bool

	drainDone chan struct{}
	waitDone  chan struct{}
	waitState struct {
		ps  *os.ProcessState
		err error
	}

	shutdownOnce sync.Once
	info         *shutdownInfo
	slavePath    string
	slaveInfo    os.FileInfo
	descendants  []procIdentity
	registryKey  processKey
}

// shutdownInfo records what teardown observed so a test can assert on it.
type shutdownInfo struct {
	exited     bool
	exitCode   int
	signaled   bool
	signal     syscall.Signal
	stillAlive bool
	pids       []int
	slavePath  string
}

// start launches the fixture on a private PTY with a full-duplex control
// channel inherited as fd 3. The PTY slave is put in raw mode so terminal
// bytes are exactly what the fixture wrote.
func start(t *testing.T, mode string, opts ...option) *fx {
	t.Helper()
	o := options{rows: 24, cols: 80}
	for _, fn := range opts {
		fn(&o)
	}

	ptmx, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open: %v", err)
	}
	if err := pty.Setsize(slave, &pty.Winsize{Rows: o.rows, Cols: o.cols}); err != nil {
		t.Fatalf("pty.Setsize: %v", err)
	}
	if err := setRaw(slave); err != nil {
		t.Fatalf("setRaw on pty slave: %v", err)
	}

	ctlParent, ctlChild, err := socketPair()
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	defer ctlChild.Close()

	args := append([]string{"--mode=" + mode, "--label=" + o.label}, o.extra...)
	cmd := exec.Command(fixtureBin, args...)
	cmd.Env = append(os.Environ(), o.env...)
	isolateFixtureCommand(t, cmd)
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.ExtraFiles = []*os.File{ctlChild}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fixture %s: %v", mode, err)
	}
	slaveInfo, err := slave.Stat()
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("stat pty slave: %v", err)
	}
	identity, err := readProcIdentity(t, cmd.Process.Pid)
	if err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("read fixture identity: %v", err)
	}
	_ = slave.Close()

	f := &fx{
		t:           t,
		mode:        mode,
		opts:        o,
		cmd:         cmd,
		pid:         cmd.Process.Pid,
		ptmx:        ptmx,
		control:     ctlParent,
		reader:      control.NewReader(ctlParent),
		sender:      control.NewEmitter(ctlParent),
		drainDone:   make(chan struct{}),
		waitDone:    make(chan struct{}),
		slaveInfo:   slaveInfo,
		registryKey: processKey{pid: cmd.Process.Pid, startTime: identity.StartTime},
	}
	go f.wait()
	go f.drain()
	// slave() takes registryMu, so resolve the path before registering.
	f.slave()
	registryMu.Lock()
	registry[f.registryKey] = registryEntry{mode: mode, label: o.label, slavePath: f.slavePath, slaveInfo: slaveInfo}
	registryMu.Unlock()
	t.Cleanup(func() { f.shutdown() })
	return f
}

// slave reads the controlling terminal path of the fixture from procfs. It is
// captured while the process is alive so teardown can prove the PTY is gone.
func (f *fx) slave() string {
	if f.slavePath != "" {
		return f.slavePath
	}
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/0", f.pid))
	if err != nil {
		return ""
	}
	if !strings.HasPrefix(link, "/dev/pts/") {
		return ""
	}
	f.slavePath = link
	registryMu.Lock()
	e := registry[f.registryKey]
	e.slavePath = link
	registry[f.registryKey] = e
	registryMu.Unlock()
	return link
}

// drain keeps the PTY readable so the fixture never blocks on a full buffer,
// and records everything the fixture wrote to the terminal.
func (f *fx) drain() {
	defer close(f.drainDone)
	buf := make([]byte, 32*1024)
	for {
		n, err := f.ptmx.Read(buf)
		if n > 0 {
			f.mu.Lock()
			if f.term.Len()+n > drainChunkCap {
				f.overrun = true
			} else {
				f.term.Write(buf[:n])
			}
			f.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// terminal returns everything drained from the PTY so far.
func (f *fx) terminal() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.term.String()
}

// command sends one control command under a bounded write deadline.
func (f *fx) command(c control.Command) {
	f.t.Helper()
	if err := f.sendCommand(c); err != nil {
		f.t.Fatalf("send command %s: %v", c.Cmd, err)
	}
}

func (f *fx) sendCommand(c control.Command) error {
	if err := f.control.SetWriteDeadline(time.Now().Add(eventTimeout)); err != nil {
		return err
	}
	err := f.sender.EmitLine(c)
	clearErr := f.control.SetWriteDeadline(time.Time{})
	if err != nil {
		return err
	}
	return clearErr
}

// stop asks the fixture to exit cleanly with the given status.
func (f *fx) stop(code int) {
	f.t.Helper()
	f.command(control.Command{Cmd: control.CmdStop, Code: code})
}

// write sends bytes to the fixture's terminal.
func (f *fx) write(s string) {
	f.t.Helper()
	if _, err := io_WriteString(f.ptmx, s); err != nil {
		f.t.Fatalf("write to pty: %v", err)
	}
}

func io_WriteString(w *os.File, s string) (int, error) { return w.WriteString(s) }

// readEvent decodes the next event under a deadline. The persistent control
// reader retains any partial record when a timeout interrupts a line.
func (f *fx) readEvent(timeout time.Duration) (control.Event, error) {
	if err := f.control.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	return f.reader.Decode()
}

// next returns the next event, failing the test if none arrives in time.
func (f *fx) next() control.Event {
	f.t.Helper()
	return f.until("any event", func(control.Event) bool { return true })
}

// until consumes events until one satisfies pred, failing the test otherwise.
func (f *fx) until(what string, pred func(control.Event) bool) control.Event {
	f.t.Helper()
	deadline := time.Now().Add(eventTimeout)
	var seen []string
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			f.t.Fatalf("timed out waiting for %s; saw %v", what, seen)
		}
		ev, err := f.readEvent(remaining)
		if err != nil {
			f.t.Fatalf("waiting for %s: %v; saw %v", what, err, seen)
		}
		if pred(ev) {
			return ev
		}
		seen = append(seen, ev.EventName())
	}
}

// kind waits for the next event with the given name.
func (f *fx) kind(name string) control.Event {
	f.t.Helper()
	return f.until(name, func(ev control.Event) bool { return ev.EventName() == name })
}

// kindIs waits for the next named event and asserts its predicate.
func (f *fx) kindIs(name string, check func(control.Event) bool, what string) control.Event {
	f.t.Helper()
	ev := f.kind(name)
	if !check(ev) {
		f.t.Fatalf("%s: got %s: %+v", what, name, ev)
	}
	return ev
}

// ready waits for the start-up handshake and cross-checks it against the kernel.
func (f *fx) ready() *control.Ready {
	f.t.Helper()
	ev := f.kind(control.EventReady)
	r, ok := ev.(*control.Ready)
	if !ok {
		f.t.Fatalf("ready: got %T", ev)
	}
	if r.PID != f.pid {
		f.t.Fatalf("ready reported pid %d, want %d", r.PID, f.pid)
	}
	if r.Mode != f.mode {
		f.t.Fatalf("ready reported mode %q, want %q", r.Mode, f.mode)
	}
	if r.PGID != r.PID {
		f.t.Fatalf("fixture is not its own process group leader: pid %d pgid %d", r.PID, r.PGID)
	}
	if r.SID != r.PID {
		f.t.Fatalf("fixture is not a session leader: pid %d sid %d", r.PID, r.SID)
	}
	return r
}

// startSize waits for the terminal size reported at start-up.
func (f *fx) startSize(rows, cols uint16) *control.Size {
	f.t.Helper()
	ev := f.kindIs(control.EventSize, func(ev control.Event) bool {
		s := ev.(*control.Size)
		return s.Reason == "start"
	}, "start size")
	s := ev.(*control.Size)
	if s.Rows != int(rows) || s.Cols != int(cols) {
		f.t.Fatalf("start size = %dx%d, want %dx%d", s.Rows, s.Cols, rows, cols)
	}
	return s
}

// silence asserts that no event arrives within a short bounded window. It is
// used only to prove a start-up gate is actually closed, never to synchronise.
func (f *fx) silence(window time.Duration) {
	f.t.Helper()
	ev, err := f.readEvent(window)
	if err == nil {
		f.t.Fatalf("expected no event within %s, got %s: %+v", window, ev.EventName(), ev)
	}
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		f.t.Fatalf("expected a deadline while checking for silence, got %v", err)
	}
}

// awaitExit waits for the exit event and returns it.
func (f *fx) awaitExit() *control.Exit {
	f.t.Helper()
	ev := f.kind(control.EventExit)
	e, ok := ev.(*control.Exit)
	if !ok {
		f.t.Fatalf("exit: got %T", ev)
	}
	return e
}

// ident waits for an identity event with the given role and records the full
// kernel identity so teardown can detect leaked descendants without trusting a PID.
func (f *fx) ident(role string) *control.Ident {
	f.t.Helper()
	ev := f.kind(control.EventIdent)
	id, ok := ev.(*control.Ident)
	if !ok {
		f.t.Fatalf("ident: got %T", ev)
	}
	if id.Role != role {
		f.t.Fatalf("ident role = %q, want %q", id.Role, role)
	}
	identity, err := readProcIdentity(f.t, id.PID)
	if err != nil {
		f.t.Fatalf("read %s identity: %v", role, err)
	}
	if identity.PPID != id.PPID || identity.PGID != id.PGID {
		f.t.Fatalf("%s identity differs from procfs: event=%+v procfs=%+v", role, id, identity)
	}
	f.descendants = append(f.descendants, identity)
	registryMu.Lock()
	entry := registry[f.registryKey]
	entry.descendants = append(entry.descendants, identity)
	registry[f.registryKey] = entry
	registryMu.Unlock()
	return id
}

// shutdown tears the fixture down by exact pid and returns what it observed.
// It is idempotent and is also registered as test cleanup.
func (f *fx) shutdown() *shutdownInfo {
	f.shutdownOnce.Do(func() {
		f.info = f.teardown()
	})
	return f.info
}

func (f *fx) teardown() *shutdownInfo {
	info := &shutdownInfo{pids: []int{f.pid}, slavePath: f.slave()}
	for _, child := range f.descendants {
		info.pids = append(info.pids, child.PID)
	}

	// Ask nicely first so the fixture emits its exit event and reaps its own
	// descendants, then fall back to signals through the exec.Cmd process handle.
	_ = f.sendCommand(control.Command{Cmd: control.CmdStop})
	if ps, err := f.waitFor(exitTimeout); err == nil {
		info.exited = true
		info.exitCode = ps.ExitCode()
		if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			info.signaled = true
			info.signal = ws.Signal()
		}
	} else {
		for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
			if ps, werr := f.waitFor(shortSilence); werr == nil {
				info.exited = true
				info.exitCode = ps.ExitCode()
				break
			}
			select {
			case <-f.waitDone:
			default:
				_ = f.cmd.Process.Signal(sig)
			}
		}
	}

	_ = f.ptmx.Close()
	select {
	case <-f.drainDone:
	case <-time.After(drainTimeout):
	}
	_ = f.control.Close()

	if id, err := readProcIdentity(nil, f.pid); err == nil && id.StartTime == f.registryKey.startTime {
		info.stillAlive = true
	}
	for _, child := range f.descendants {
		if id, err := readProcIdentity(nil, child.PID); err == nil && id.StartTime == child.StartTime {
			info.stillAlive = true
		}
	}
	if current, err := os.Stat(f.slavePath); err == nil && f.slaveInfo != nil && os.SameFile(f.slaveInfo, current) {
		info.stillAlive = true
	}
	registryMu.Lock()
	if !info.stillAlive {
		delete(registry, f.registryKey)
	}
	registryMu.Unlock()
	return info
}

// wait reaps the fixture once and publishes its cached result to every waiter.
func (f *fx) wait() {
	waitErr := f.cmd.Wait()
	f.waitState.ps = f.cmd.ProcessState
	var exitErr *exec.ExitError
	if waitErr != nil && !errors.As(waitErr, &exitErr) {
		f.waitState.err = waitErr
	}
	if f.waitState.ps == nil && f.waitState.err == nil {
		f.waitState.err = errors.New("fixture wait returned no process state")
	}
	close(f.waitDone)
}

// waitFor blocks until the fixture exits or the deadline passes.
func (f *fx) waitFor(timeout time.Duration) (*os.ProcessState, error) {
	select {
	case <-f.waitDone:
		if f.waitState.err != nil {
			return nil, f.waitState.err
		}
		return f.waitState.ps, nil
	case <-time.After(timeout):
		return nil, errors.New("timed out waiting for the fixture to exit")
	}
}

// waitExit stops the fixture with the given status and returns its exit event
// and process state.
func (f *fx) waitExit(code int) (*control.Exit, *os.ProcessState) {
	f.t.Helper()
	f.stop(code)
	return f.awaitExitOnly()
}

// awaitExitOnly waits for an exit event without sending a stop command, for
// modes that terminate on their own and may already be gone.
func (f *fx) awaitExitOnly() (*control.Exit, *os.ProcessState) {
	f.t.Helper()
	ev := f.awaitExit()
	ps, err := f.waitFor(exitTimeout)
	if err != nil {
		f.t.Fatalf("wait for fixture after exit event: %v", err)
	}
	return ev, ps
}

// procIdentity parses /proc/<pid>/stat. The tests read process identity
// independently of the fixture so a wrong claim by the fixture cannot pass.
type procIdentity struct {
	PID       int
	PPID      int
	PGID      int
	SID       int
	StartTime uint64
}

func readProcIdentity(t *testing.T, pid int) (procIdentity, error) {
	if t != nil {
		t.Helper()
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return procIdentity{}, err
	}
	s := string(b)
	shut := strings.LastIndexByte(s, ')')
	if shut < 0 {
		return procIdentity{}, fmt.Errorf("unparsable /proc/%d/stat", pid)
	}
	fields := strings.Fields(s[shut+1:])
	if len(fields) < 20 {
		return procIdentity{}, fmt.Errorf("short /proc/%d/stat", pid)
	}
	ppid, err := jsonInt(fields[1])
	if err != nil {
		return procIdentity{}, err
	}
	pgid, err := jsonInt(fields[2])
	if err != nil {
		return procIdentity{}, err
	}
	sid, err := jsonInt(fields[3])
	if err != nil {
		return procIdentity{}, err
	}
	startTime, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return procIdentity{}, err
	}
	return procIdentity{PID: pid, PPID: ppid, PGID: pgid, SID: sid, StartTime: startTime}, nil
}

func jsonInt(s string) (int, error) {
	return strconv.Atoi(s)
}

// controlEnd is the test side of the control channel. It wraps a net.Conn
// because a raw descriptor wrapped with os.NewFile is not pollable, and a
// pollable endpoint is what makes deadline-bounded reads possible.
type controlEnd struct {
	net.Conn
}

// socketPair creates the full-duplex control channel. One end is inherited by
// the fixture as fd 3; the fixture both writes events to and reads commands
// from it. The returned child file is owned by the caller and must be closed
// after the fixture has been started.
func socketPair() (parent *controlEnd, child *os.File, err error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return nil, nil, err
	}
	rawParent := os.NewFile(uintptr(fds[0]), "control-parent")
	child = os.NewFile(uintptr(fds[1]), "control-child")
	// FileConn dups the descriptor and hands the duplicate to the poller, so
	// the original is no longer needed.
	conn, err := net.FileConn(rawParent)
	if err != nil {
		_ = rawParent.Close()
		_ = child.Close()
		return nil, nil, err
	}
	_ = rawParent.Close()
	return &controlEnd{Conn: conn}, child, nil
}

// setRaw puts a pty slave into raw mode with echo and output translation off,
// so what a test writes is what the fixture reads and vice versa.
func setRaw(f *os.File) error {
	var t syscall.Termios
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCGETS, uintptr(unsafe.Pointer(&t))); errno != 0 {
		return errno
	}
	t.Iflag &^= syscall.IGNBRK | syscall.BRKINT | syscall.PARMRK | syscall.ISTRIP |
		syscall.INLCR | syscall.IGNCR | syscall.ICRNL | syscall.IXON
	t.Oflag &^= syscall.OPOST
	t.Lflag &^= syscall.ECHO | syscall.ECHONL | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	t.Cflag &^= syscall.CSIZE | syscall.PARENB
	t.Cflag |= syscall.CS8
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TCSETS, uintptr(unsafe.Pointer(&t))); errno != 0 {
		return errno
	}
	return nil
}

// waitGone polls for a pid to disappear. It is only used after a bounded wait
// has already failed, to give the kernel a moment to reap.
func waitGone(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return errors.Is(err, syscall.ESRCH)
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}
