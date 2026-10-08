//go:build linux

package pty

import (
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestStartUsesControllingTTYWorkingDirectoryInheritedEnvironmentAndLiteralArgv(t *testing.T) {
	cwd := t.TempDir()
	t.Setenv("ASD_PTY_TEST_ENV", "inherited value")
	script := `if (exec 9<>/dev/tty) 2>/dev/null; then control=yes; else control=no; fi
printf 'control=%s cwd=%s env=%s arg=%s\n' "$control" "$PWD" "$ASD_PTY_TEST_ENV" "$1"
IFS= read -r line
printf 'input=%s\n' "$line"`
	child, err := Start("/bin/sh", []string{"-c", script, "sh", "literal ; $HOME"}, cwd, Size{Columns: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer child.Close()

	pid := child.PID()
	if pid <= 0 {
		t.Fatalf("child PID = %d, want positive", pid)
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatalf("Getpgid(%d) error = %v", pid, err)
	}
	if pgid != pid {
		t.Fatalf("child process group = %d, want new session leader pid %d", pgid, pid)
	}
	sid, _, errno := syscall.Syscall(syscall.SYS_GETSID, uintptr(pid), 0, 0)
	if errno != 0 {
		t.Fatalf("getsid(%d) error = %v", pid, errno)
	}
	if int(sid) != pid {
		t.Fatalf("child session id = %d, want child pid %d", sid, pid)
	}

	if n, err := child.Write([]byte("literal line input\n")); err != nil || n != len("literal line input\n") {
		t.Fatalf("Write() = %d, %v; want full line", n, err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	output, err := io.ReadAll(child)
	if err != nil {
		t.Fatalf("ReadAll(PTY) error = %v", err)
	}
	for _, want := range []string{
		"control=yes",
		"cwd=" + cwd,
		"env=inherited value",
		"arg=literal ; $HOME",
		"input=literal line input",
	} {
		if !strings.Contains(string(output), want) {
			t.Errorf("PTY output %q does not contain %q", output, want)
		}
	}
	if err := child.Wait(); err != ErrAlreadyWaited {
		t.Fatalf("second Wait() error = %v, want ErrAlreadyWaited", err)
	}
}

func TestStartRejectsInvalidSizeBeforeSpawning(t *testing.T) {
	child, err := Start("/bin/sh", []string{"-c", "exit 0"}, t.TempDir(), Size{})
	if err == nil {
		if child != nil {
			_ = child.Close()
		}
		t.Fatal("Start() accepted a zero terminal size")
	}
}

func TestReadAfterSlaveExitNormalizesLinuxEIOToEOF(t *testing.T) {
	child, err := Start("/bin/sh", []string{"-c", "printf done"}, t.TempDir(), Size{Columns: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer child.Close()
	if err := child.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	output, err := io.ReadAll(child)
	if err != nil {
		t.Fatalf("ReadAll(PTY) error = %v", err)
	}
	if string(output) != "done" {
		t.Fatalf("PTY output = %q, want done", output)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	child, err := Start("/bin/sh", []string{"-c", "exit 0"}, t.TempDir(), Size{Columns: 80, Rows: 24})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if err := child.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if err := child.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestClosedChildReadAndWriteReturnErrClosed(t *testing.T) {
	var child *Child
	if _, err := child.Read(make([]byte, 1)); err != os.ErrClosed {
		t.Fatalf("nil child Read() error = %v, want os.ErrClosed", err)
	}
	if _, err := child.Write([]byte("x")); err != os.ErrClosed {
		t.Fatalf("nil child Write() error = %v, want os.ErrClosed", err)
	}
}
