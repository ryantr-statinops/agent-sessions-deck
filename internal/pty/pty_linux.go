//go:build linux

package pty

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	unixpty "github.com/creack/pty"
)

var ErrAlreadyWaited = errors.New("pty child already waited")

// Size is a terminal size in cells. Columns map to PTY columns; Rows map to rows.
type Size struct {
	Columns int
	Rows    int
}

// Child owns the PTY master and the process started behind it. Closing the master can
// send SIGHUP to a foreground child; the runtime closes it after exit, never to detach.
type Child struct {
	cmd    *exec.Cmd
	master *os.File

	waitMu sync.Mutex
	waited bool

	closeOnce sync.Once
	closeErr  error
}

// Start starts an executable directly, with literal argv, the supplied working
// directory, and the caller's inherited environment. The child receives a new
// session and controlling terminal through creack/pty.
func Start(executable string, args []string, cwd string, size Size) (*Child, error) {
	winsize, err := size.winsize()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(executable, args...)
	cmd.Dir = cwd
	// A nil Cmd.Env intentionally inherits the runtime owner's environment; it is
	// never copied into persisted session metadata.
	master, err := unixpty.StartWithSize(cmd, winsize)
	if err != nil {
		return nil, err
	}
	return &Child{cmd: cmd, master: master}, nil
}

func (s Size) winsize() (*unixpty.Winsize, error) {
	if s.Columns < 1 || s.Columns > 1<<16-1 || s.Rows < 1 || s.Rows > 1<<16-1 {
		return nil, errors.New("terminal size must be between 1 and 65535 cells in each dimension")
	}
	return &unixpty.Winsize{Cols: uint16(s.Columns), Rows: uint16(s.Rows)}, nil
}

// PID returns the spawned child process id, or zero when the child is unavailable.
func (c *Child) PID() int {
	if c == nil || c.cmd == nil || c.cmd.Process == nil {
		return 0
	}
	return c.cmd.Process.Pid
}

// Read reads bytes from the PTY master. Linux reports EIO when the final slave
// descriptor closes; that condition is the terminal stream's EOF.
func (c *Child) Read(p []byte) (int, error) {
	if c == nil || c.master == nil {
		return 0, os.ErrClosed
	}
	n, err := c.master.Read(p)
	if errors.Is(err, syscall.EIO) {
		return n, io.EOF
	}
	return n, err
}

// Write writes literal bytes to the PTY master.
func (c *Child) Write(p []byte) (int, error) {
	if c == nil || c.master == nil {
		return 0, os.ErrClosed
	}
	return c.master.Write(p)
}

// Resize updates the PTY window size, causing the kernel to notify the foreground
// process group with SIGWINCH when it changes.
func (c *Child) Resize(size Size) error {
	if c == nil || c.master == nil {
		return os.ErrClosed
	}
	winsize, err := size.winsize()
	if err != nil {
		return err
	}
	return unixpty.Setsize(c.master, winsize)
}

// Wait reaps the child once. A second caller is refused rather than racing or
// performing another wait.
func (c *Child) Wait() error {
	if c == nil || c.cmd == nil {
		return errors.New("pty child is unavailable")
	}
	c.waitMu.Lock()
	if c.waited {
		c.waitMu.Unlock()
		return ErrAlreadyWaited
	}
	c.waited = true
	c.waitMu.Unlock()
	return c.cmd.Wait()
}

// ProcessState returns the wait status after Wait completes.
func (c *Child) ProcessState() *os.ProcessState {
	if c == nil || c.cmd == nil {
		return nil
	}
	return c.cmd.ProcessState
}

// Close closes the PTY master only. It is safe to call more than once.
func (c *Child) Close() error {
	if c == nil || c.master == nil {
		return nil
	}
	c.closeOnce.Do(func() { c.closeErr = c.master.Close() })
	return c.closeErr
}

// Validate verifies that a requested terminal size fits the kernel PTY interface.
func (s Size) Validate() error {
	_, err := s.winsize()
	return err
}
