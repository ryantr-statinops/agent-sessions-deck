//go:build linux

package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/ryantr-statinops/agent-sessions-deck/tests/testagent/control"
)

// Fixture modes. Every mode is deterministic: it advances only on an explicit
// control command, an explicit signal, or explicit terminal input.
const (
	modeEcho        = "echo"
	modeLine        = "line"
	modeANSI        = "ansi"
	modeExitCode    = "exit-code"
	modeSlowStart   = "slow-start"
	modeIgnoredTerm = "ignored-term"
	modeChild       = "child"
	modeGrandchild  = "grandchild"
	modeFlood       = "flood"
	modeUnicode     = "unicode"
	modeQuery       = "query"
	modeSize        = "size"
	modeHelperRun   = modeHelper
)

// goAwayFD is the inherited descriptor a helper reads to learn that its parent
// is done with it. EOF on it makes the helper exit.
const goAwayFD = 4

// Bounded waits inside the fixture. The fixture never relies on these for
// ordering; they only bound reaping so a stuck descendant cannot wedge a test.
const (
	identTimeout = 10 * time.Second
	reapGrace    = 5 * time.Second
	reapKillWait = 2 * time.Second
)

// releaseMessage is the explicit line a parent writes to release a descendant.
const releaseMessage = "release"

var knownModes = []string{
	modeEcho, modeLine, modeANSI, modeExitCode, modeSlowStart,
	modeIgnoredTerm, modeChild, modeGrandchild, modeFlood, modeUnicode,
	modeQuery, modeSize,
}

// Modes returns the public mode names the fixture accepts.
func knownModeNames() []string {
	out := make([]string, len(knownModes))
	copy(out, knownModes)
	return out
}

var signalNames = map[syscall.Signal]string{
	syscall.SIGHUP:   "SIGHUP",
	syscall.SIGINT:   "SIGINT",
	syscall.SIGQUIT:  "SIGQUIT",
	syscall.SIGTERM:  "SIGTERM",
	syscall.SIGUSR1:  "SIGUSR1",
	syscall.SIGUSR2:  "SIGUSR2",
	syscall.SIGKILL:  "SIGKILL",
	syscall.SIGWINCH: "SIGWINCH",
}

func signalName(s os.Signal) string {
	if s, ok := s.(syscall.Signal); ok {
		if n, ok := signalNames[s]; ok {
			return n
		}
		return s.String()
	}
	return s.String()
}

// descendant is a process the fixture spawned itself. Every id is exact and
// kernel-resolved; cleanup uses the exec.Cmd handle, never a process name or
// a recycled pid.
type descendant struct {
	role    string
	pid     int
	cmd     *exec.Cmd
	id      *os.File
	decoder *json.Decoder
	goAway  *os.File
	waited  chan error
}

// spawnRole starts a helper that reports its identity on control.IdentFD and
// exits when the returned go-away pipe is closed. spawn names an optional role
// for the helper to spawn in turn, which is how the grandchild chain is built.
func spawnRole(exe, role, label, spawn string) (*descendant, error) {
	idR, idW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	goR, goW, err := os.Pipe()
	if err != nil {
		_ = idR.Close()
		_ = idW.Close()
		return nil, err
	}
	args := []string{"--mode=" + modeHelper, "--role=" + role, "--label=" + label}
	if spawn != "" {
		args = append(args, "--spawn="+spawn)
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.ExtraFiles = []*os.File{idW, goR}
	if err := cmd.Start(); err != nil {
		_ = idR.Close()
		_ = idW.Close()
		_ = goR.Close()
		_ = goW.Close()
		return nil, err
	}
	_ = idW.Close()
	_ = goR.Close()
	d := &descendant{
		role:    role,
		pid:     cmd.Process.Pid,
		cmd:     cmd,
		id:      idR,
		decoder: json.NewDecoder(idR),
		goAway:  goW,
		waited:  make(chan error, 1),
	}
	go func() { d.waited <- cmd.Wait() }()
	return d, nil
}

// ident reads one identity report with a deadline using a persistent decoder so
// buffered bytes from one record cannot be lost before the next read.
func (d *descendant) ident(timeout time.Duration) (control.IdentReport, error) {
	var rep control.IdentReport
	if err := d.id.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return rep, err
	}
	var raw json.RawMessage
	if err := d.decoder.Decode(&raw); err != nil {
		return rep, err
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		return rep, err
	}
	return rep, nil
}

// release asks the descendant to exit. Releasing is an explicit message, not
// merely closing the pipe: a helper that exited on EOF would also exit when the
// parent died without cleaning up, which would hide a reaping bug.
func (d *descendant) release() {
	_, _ = d.goAway.WriteString(releaseMessage + "\n")
	_ = d.goAway.Close()
}

// reap waits for the descendant to exit, escalating through its exec.Cmd process
// handle if the bounded grace period expires.
func (d *descendant) reap() {
	d.release()
	select {
	case <-d.waited:
	case <-time.After(reapGrace):
		_ = d.cmd.Process.Kill()
		select {
		case <-d.waited:
		case <-time.After(reapKillWait):
		}
	}
	_ = d.id.Close()
}

// runHelper is the identity leaf. It has no control channel: it writes one
// identity line per role it owns to control.IdentFD and then waits for its
// parent to close the go-away pipe.
func runHelper(opt options) {
	id := os.NewFile(control.IdentFD, "ident")
	goAway := os.NewFile(goAwayFD, "goaway")
	if id == nil || goAway == nil {
		fmt.Fprintln(os.Stderr, "fakeagent: helper requires inherited ident and go-away descriptors")
		os.Exit(3)
	}
	role := opt.role
	if role == "" {
		role = "child"
	}
	emitOwnIdent(id, role)

	var grandchild *descendant

	if opt.spawn != "" {
		exe, err := os.Executable()
		if err != nil {
			_ = id.Close()
			_ = goAway.Close()
			os.Exit(3)
		}
		grand, err := spawnRole(exe, opt.spawn, opt.label, "")
		if err != nil {
			_ = id.Close()
			_ = goAway.Close()
			os.Exit(3)
		}
		rep, err := grand.ident(identTimeout)
		if err != nil {
			grand.reap()
			_ = id.Close()
			_ = goAway.Close()
			os.Exit(3)
		}
		// Relay the grandchild's own identity. Re-deriving it here would
		// report this process under the grandchild's role. The grandchild
		// stays alive for as long as this process does, so a test can still
		// inspect the whole tree.
		emitReport(id, rep)
		grandchild = grand
	}

	buf := make([]byte, 64)
	waitForGoAway(goAway, buf)
	if grandchild != nil {
		grandchild.reap()
	}
	_ = id.Close()
	_ = goAway.Close()
	os.Exit(0)
}

// waitForGoAway blocks until the parent closes the go-away pipe. A helper also
// ignores SIGHUP and SIGTERM, so it cannot be killed incidentally when the pty
// or the fixture goes away: it ends only when its parent releases it, or when
// the parent escalates to SIGKILL against its exact pid. That is what makes a
// "descendant was reaped" assertion meaningful.
func waitForGoAway(goAway *os.File, buf []byte) {
	caught := make(chan os.Signal, 8)
	signal.Notify(caught, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(caught)
	released := make(chan struct{})
	go func() {
		defer close(released)
		// Only an explicit release line ends this process. EOF, a closed pipe
		// and a caught signal are all ignored: this process ends when its
		// parent asks, or when the parent escalates to SIGKILL against this
		// process handle. Dying implicitly would mask a missing reap.
		for {
			n, err := goAway.Read(buf)
			if n > 0 && strings.Contains(string(buf[:n]), releaseMessage) {
				return
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					// The pipe is unusable; wait for the SIGKILL escalation.
					select {}
				}
				select {}
			}
		}
	}()
	for {
		select {
		case <-released:
			return
		case <-caught:
		}
	}
}

// identity is the kernel-reported identity of a process.
type identity struct {
	PID  int
	PPID int
	PGID int
	SID  int
}

// readIdentity parses /proc/<pid>/stat. Process identity in the fixture comes
// from here and nowhere else: nothing is inferred from argv or from what a
// process claims about itself.
func readIdentity(pid int) (identity, error) {
	var id identity
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return id, err
	}
	s := string(raw)
	open := strings.IndexByte(s, '(')
	shut := strings.LastIndexByte(s, ')')
	if open < 0 || shut < 0 || shut <= open {
		return id, fmt.Errorf("unparsable /proc/%d/stat", pid)
	}
	if id.PID, err = strconv.Atoi(strings.TrimSpace(s[:open])); err != nil {
		return id, fmt.Errorf("unparsable pid in /proc/%d/stat: %w", pid, err)
	}
	fields := strings.Fields(s[shut+1:])
	if len(fields) < 4 {
		return id, fmt.Errorf("short /proc/%d/stat", pid)
	}
	// fields are state, ppid, pgrp, session.
	if id.PPID, err = strconv.Atoi(fields[1]); err != nil {
		return id, err
	}
	if id.PGID, err = strconv.Atoi(fields[2]); err != nil {
		return id, err
	}
	if id.SID, err = strconv.Atoi(fields[3]); err != nil {
		return id, err
	}
	return id, nil
}

// emitOwnIdent writes this process's identity line, resolved from the kernel
// rather than taken from anything the process claims about itself.
func emitOwnIdent(w *os.File, role string) {
	id, err := readIdentity(os.Getpid())
	if err != nil {
		return
	}
	emitReport(w, control.IdentReport{Role: role, PID: id.PID, PPID: id.PPID, PGID: id.PGID})
}

// emitReport forwards a resolved identity line unchanged. A helper uses this to
// relay its child's own identity; re-deriving it locally would misreport the
// process.
func emitReport(w *os.File, rep control.IdentReport) {
	b, err := json.Marshal(rep)
	if err != nil {
		return
	}
	_, _ = w.Write(append(b, '\n'))
}

// line is one newline-stripped line of terminal input.
type line struct {
	n    int
	data string
}

// fixture is the running top-level agent process.
type fixture struct {
	opt  options
	conn *control.Conn
	file *os.File
	out  *os.File
	in   *os.File

	cmds  chan control.Command
	winch chan os.Signal
	sigCh chan os.Signal
	lines chan line

	children []*descendant
	frameN   int
	lineN    int

	emitMu    sync.Mutex
	ignCount  map[string]int
	startOnce sync.Once
}

func newFixture(opt options) (*fixture, error) {
	if !isKnownMode(opt.mode) {
		return nil, fmt.Errorf("unknown mode %q (known: %s)", opt.mode, strings.Join(knownModeNames(), ", "))
	}
	fd := control.ControlFD
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0); errno != 0 {
		return nil, fmt.Errorf("control descriptor %d is not open: %v", control.ControlFD, errno)
	}
	sockType, err := syscall.GetsockoptInt(fd, syscall.SOL_SOCKET, syscall.SO_TYPE)
	if err != nil {
		return nil, fmt.Errorf("control descriptor %d is not a socket: %w", fd, err)
	}
	if sockType != syscall.SOCK_STREAM {
		return nil, fmt.Errorf("control descriptor %d must be a stream socket", fd)
	}
	sockAddr, err := syscall.Getsockname(fd)
	if err != nil {
		return nil, fmt.Errorf("control descriptor %d has no socket address: %w", fd, err)
	}
	if _, ok := sockAddr.(*syscall.SockaddrUnix); !ok {
		return nil, fmt.Errorf("control descriptor %d must be an AF_UNIX socket", fd)
	}
	file := os.NewFile(uintptr(fd), "control")
	// The control channel belongs to this process only. Descendants get their
	// own inherited descriptors, so mark it close-on-exec.
	syscall.CloseOnExec(fd)

	f := &fixture{
		opt:      opt,
		file:     file,
		conn:     control.Dial(file, file),
		out:      os.Stdout,
		in:       os.Stdin,
		cmds:     make(chan control.Command, 64),
		winch:    make(chan os.Signal, 8),
		sigCh:    make(chan os.Signal, 8),
		lines:    make(chan line, 256),
		ignCount: make(map[string]int),
	}
	return f, nil
}

func isKnownMode(mode string) bool {
	for _, m := range knownModes {
		if m == mode {
			return true
		}
	}
	return false
}

// readsLines reports whether a mode consumes terminal input as lines. Query
// mode reads the same descriptor itself so it can see reply bytes, so it must
// not share the line reader.
func readsLines(mode string) bool {
	switch mode {
	case modeEcho, modeLine, modeANSI, modeSlowStart, modeIgnoredTerm,
		modeChild, modeGrandchild, modeUnicode, modeSize:
		return true
	default:
		return false
	}
}

// run emits the start-up events and dispatches to the selected mode.
func (f *fixture) run() {
	self, err := readIdentity(os.Getpid())
	if err != nil {
		_ = f.emit(&control.Exit{Code: 2, Reason: "identity: " + err.Error()})
		os.Exit(2)
	}

	rows, cols := f.winsize()
	f.emit(&control.Ready{PID: self.PID, PGID: self.PGID, SID: self.SID, Mode: f.opt.mode, Label: f.opt.label, Arg: describe(f.opt)})
	f.emit(&control.Size{Rows: rows, Cols: cols, Reason: "start"})

	go f.readCommands()
	if readsLines(f.opt.mode) {
		go f.readInput()
	}

	if err := f.dispatch(); err != nil {
		f.emit(&control.Exit{Code: 2, Reason: err.Error()})
		os.Exit(2)
	}
}

// dispatch runs the selected mode. Each mode returns only when the fixture
// should exit; finish is the single exit path.
func (f *fixture) dispatch() error {
	switch f.opt.mode {
	case modeEcho:
		return f.runEcho()
	case modeLine:
		return f.runLine()
	case modeANSI:
		return f.runANSI()
	case modeExitCode:
		return f.runExitCode()
	case modeSlowStart:
		return f.runSlowStart()
	case modeIgnoredTerm:
		return f.runIgnoredTerm()
	case modeChild:
		return f.runChild("")
	case modeGrandchild:
		return f.runChild("grandchild")
	case modeFlood:
		return f.runFlood()
	case modeUnicode:
		return f.runUnicode()
	case modeQuery:
		return f.runQuery()
	case modeSize:
		return f.runSize()
	default:
		return fmt.Errorf("unhandled mode %q", f.opt.mode)
	}
}

func (f *fixture) emit(ev control.Event) error { return f.conn.Emit(ev) }

// started announces that a mode is running. It is emitted at most once per run
// so shared mode bodies do not have to coordinate it.
func (f *fixture) started() {
	f.startOnce.Do(func() { _ = f.emit(&control.Started{Mode: f.opt.mode}) })
}

// write emits terminal bytes. The mode goroutine is the only writer, so
// terminal output never interleaves with itself.
func (f *fixture) write(s string) error {
	_, err := f.out.WriteString(s)
	return err
}

// finish is the single exit path: descendants are reaped first, the exit event
// is always last, and then the process exits with the requested status.
func (f *fixture) finish(code int, reason string) {
	for _, d := range f.children {
		d.reap()
	}
	_ = f.emit(&control.Exit{Code: code, Reason: reason})
	_ = f.file.Close()
	os.Exit(code)
}

// readCommands decodes control commands until the channel closes.
func (f *fixture) readCommands() {
	defer close(f.cmds)
	for {
		c, err := f.conn.Reader.DecodeCommand()
		if err != nil {
			return
		}
		f.cmds <- c
	}
}

// readInput splits terminal input into lines. The PTY is in raw mode, so no
// echo or CR translation is expected here.
func (f *fixture) readInput() {
	defer close(f.lines)
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for {
		n, err := f.in.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				idx := indexByte(buf, '\n')
				if idx < 0 {
					break
				}
				raw := string(buf[:idx])
				buf = buf[idx+1:]
				f.lineN++
				f.lines <- line{n: f.lineN, data: strings.TrimSuffix(raw, "\r")}
			}
		}
		if err != nil {
			return
		}
	}
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

// handleCommand applies a control command that is meaningful for every mode.
// It returns ok=false when the mode loop must stop with the given status.
func (f *fixture) handleCommand(c control.Command, def int) (code int, ok bool) {
	switch c.Cmd {
	case control.CmdStop:
		return def, false
	case control.CmdExit:
		return c.Code, false
	case control.CmdWrite:
		_ = f.write(c.Data)
	case control.CmdGetsize:
		rows, cols := f.winsize()
		_ = f.emit(&control.Size{Rows: rows, Cols: cols, Reason: "getsize"})
	}
	return 0, true
}

// winsize reads the terminal size from stdout, which is the PTY when the
// fixture is spawned on one.
func (f *fixture) winsize() (rows, cols int) {
	var ws struct {
		Row, Col, X, Y uint16
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.out.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0, 0
	}
	return int(ws.Row), int(ws.Col)
}

// reportSignal records a caught signal.
func (f *fixture) reportSignal(s os.Signal) {
	f.emitMu.Lock()
	name := signalName(s)
	f.ignCount[name]++
	count := f.ignCount[name]
	f.emitMu.Unlock()
	_ = f.emit(&control.Signal{Name: name, Count: count})
}

// resolve verifies a reported pid against the kernel and emits the ident event
// with kernel-resolved parent and process group.
func (f *fixture) resolve(role string, pid int) error {
	if err := processExists(pid); err != nil {
		return fmt.Errorf("reported %s pid %d is not alive: %v", role, pid, err)
	}
	id, err := readIdentity(pid)
	if err != nil {
		return err
	}
	return f.emit(&control.Ident{Role: role, PID: id.PID, PPID: id.PPID, PGID: id.PGID})
}

// processExists checks whether the reported exact pid is still present.
func processExists(pid int) error {
	if pid <= 0 {
		return errors.New("refusing to inspect a non-positive pid")
	}
	return syscall.Kill(pid, 0)
}

func hexOf(s string) string { return hex.EncodeToString([]byte(s)) }
