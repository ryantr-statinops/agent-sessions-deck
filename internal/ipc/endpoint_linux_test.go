//go:build linux

package ipc

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
)

func TestSocketPathUsesStateScopedPrivateDirectoryAndFallback(t *testing.T) {
	root := t.TempDir()
	stateA := filepath.Join(root, "state-a")
	stateB := filepath.Join(root, "state-b")
	xdgRoot, err := os.MkdirTemp(os.TempDir(), "ipc")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(xdgRoot)
	runtimeDir := filepath.Join(xdgRoot, "asd")
	first, err := socketPath(config.Paths{StateDir: stateA, RuntimeDir: runtimeDir}, filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := socketPath(config.Paths{StateDir: stateB, RuntimeDir: runtimeDir}, filepath.Join(root, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) > maxUnixSocketPath {
		t.Fatalf("state-scoped socket paths = %q and %q", first, second)
	}
	fallback, err := socketPath(config.Paths{StateDir: stateA}, xdgRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(xdgRoot, "asd-"+strconv.Itoa(os.Geteuid()))); err != nil {
		t.Fatalf("private fallback directory was not created: %v", err)
	}
	if filepath.Dir(fallback) == filepath.Dir(first) {
		t.Fatalf("XDG and fallback paths unexpectedly share a directory: %q", fallback)
	}
}

func TestRemoveStaleSocketRequiresDefinitiveRefusal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleSocket(path); err != nil {
		t.Fatalf("remove refused stale socket: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale socket still exists, lstat error = %v", err)
	}

	live, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	if err := removeStaleSocket(path); !errors.Is(err, ErrSocketActive) {
		t.Fatalf("live socket cleanup error = %v, want ErrSocketActive", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("live socket was removed: %v", err)
	}
}

func TestVerifyPeerUIDAcceptsSameUserConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			accepted <- err
			return
		}
		defer conn.Close()
		accepted <- VerifyPeerUID(conn)
	}()
	client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("same-user peer rejected: %v", err)
	}
}
