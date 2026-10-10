//go:build linux

package ipc

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
)

const maxUnixSocketPath = 100

var ErrSocketActive = errors.New("an owner socket is already accepting connections")

// SocketPath returns one short socket path per canonical state home. The
// XDG_RUNTIME_DIR location is preferred; when absent the fallback is a
// per-effective-UID private directory under the system temporary directory.
func SocketPath(paths config.Paths) (string, error) {
	return socketPath(paths, os.TempDir())
}

func socketPath(paths config.Paths, tempDir string) (string, error) {
	if paths.StateDir == "" || !filepath.IsAbs(paths.StateDir) {
		return "", errors.New("socket path requires an absolute state directory")
	}
	root := paths.RuntimeDir
	if root == "" {
		root = filepath.Join(tempDir, "asd-"+strconv.Itoa(os.Geteuid()))
	}
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("runtime directory %q is not absolute", root)
	}
	if err := ensurePrivateDirectory(root); err != nil {
		return "", fmt.Errorf("secure owner runtime directory: %w", err)
	}
	state := filepath.Clean(paths.StateDir)
	hash := sha256.Sum256([]byte(state))
	path := filepath.Join(root, fmt.Sprintf("%x.sock", hash[:8]))
	if len(path) > maxUnixSocketPath {
		return "", fmt.Errorf("owner socket path is too long (%d bytes)", len(path))
	}
	return path, nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%s is not a real directory", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("%s is not owned by effective uid %d", path, os.Geteuid())
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	return nil
}

// removeStaleSocket is called only while the state-home lifetime lock is held.
// It never unlinks on timeout, permission failure, or successful connection.
func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing owner socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket path %s", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("refusing to replace socket not owned by effective uid %d", os.Geteuid())
	}
	conn, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return ErrSocketActive
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, syscall.ENOENT) {
		return fmt.Errorf("owner socket liveness is inconclusive; refusing to unlink %s: %w", path, dialErr)
	}
	current, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("recheck stale socket before unlink: %w", err)
	}
	if !os.SameFile(info, current) || current.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("owner socket changed during stale-path check; refusing to unlink %s", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale owner socket: %w", err)
	}
	return nil
}

// VerifyPeerUID enforces the same-user Unix-socket boundary on accepted
// connections. Linux SO_PEERCRED is checked before protocol bytes are read.
func VerifyPeerUID(conn *net.UnixConn) error {
	if conn == nil {
		return errors.New("nil Unix connection")
	}
	raw, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("get Unix socket descriptor: %w", err)
	}
	var cred *unix.Ucred
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return fmt.Errorf("read Unix peer credentials: %w", err)
	}
	if sockErr != nil {
		return fmt.Errorf("read Unix peer credentials: %w", sockErr)
	}
	if cred == nil || cred.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("Unix peer uid does not match effective uid %d", os.Geteuid())
	}
	return nil
}
