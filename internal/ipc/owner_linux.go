//go:build linux

package ipc

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/store"
)

var ErrOwnerLocked = errors.New("an ASD foreground owner already holds the state home lock")

// Owner holds the state-home lock and listener for one foreground runtime.
// Bootstrap always acquires the lock before touching the socket path.
type Owner struct {
	mu         sync.Mutex
	lock       *store.Lock
	listener   *net.UnixListener
	instanceID string
	socketPath string
	closed     bool
}

// BootstrapPaths selects the secure per-state endpoint, then claims ownership.
func BootstrapPaths(paths config.Paths) (*Owner, error) {
	return BootstrapPathsMode(paths, "v1-owner")
}

// BootstrapPathsMode selects the secure per-state endpoint, then claims ownership with the specified mode.
func BootstrapPathsMode(paths config.Paths, mode string) (*Owner, error) {
	socketPath, err := SocketPath(paths)
	if err != nil {
		return nil, err
	}
	return BootstrapMode(paths, socketPath, mode)
}

// Bootstrap atomically claims the state home before inspecting or binding its
// socket. It removes only an owned socket that refuses a connection.
func Bootstrap(paths config.Paths, socketPath string) (*Owner, error) {
	return BootstrapMode(paths, socketPath, "v1-owner")
}

// BootstrapMode atomically claims the state home with a mode tag before inspecting or binding its socket.
func BootstrapMode(paths config.Paths, socketPath string, mode string) (*Owner, error) {
	if paths.StateDir == "" || !filepath.IsAbs(paths.StateDir) {
		return nil, errors.New("owner bootstrap requires an absolute state directory")
	}
	if socketPath == "" || !filepath.IsAbs(socketPath) {
		return nil, errors.New("owner bootstrap requires an absolute socket path")
	}
	lock, err := store.AcquireLock(paths.LockPath())
	if err != nil {
		if errors.Is(err, store.ErrLocked) {
			tokenData, _ := os.ReadFile(paths.LockPath())
			tokenStr := strings.TrimSpace(string(tokenData))
			if mode == "daemon" && strings.Contains(tokenStr, "mode=v1-owner") {
				return nil, fmt.Errorf("%w: a V1 foreground owner already holds this state home; stop it before starting agentd", ErrOwnerLocked)
			}
			if mode == "v1-owner" && strings.Contains(tokenStr, "mode=daemon") {
				return nil, fmt.Errorf("%w: agentd daemon already holds this state home; connect as a client instead", ErrOwnerLocked)
			}
			return nil, fmt.Errorf("%w: %w", ErrOwnerLocked, err)
		}
		return nil, fmt.Errorf("acquire state-home owner lock: %w", err)
	}
	claimed := false
	defer func() {
		if !claimed {
			_ = lock.Release()
		}
	}()
	instanceID, err := newInstanceID()
	if err != nil {
		return nil, fmt.Errorf("mint owner instance ID: %w", err)
	}
	if mode == "" {
		mode = "v1-owner"
	}
	token := fmt.Sprintf("%s pid=%d mode=%s", instanceID, os.Getpid(), mode)
	if err := lock.WriteToken(token); err != nil {
		return nil, fmt.Errorf("write owner lock token: %w", err)
	}
	if err := ensurePrivateDirectory(filepath.Dir(socketPath)); err != nil {
		return nil, fmt.Errorf("secure socket directory: %w", err)
	}
	if err := removeStaleSocket(socketPath); err != nil {
		if errors.Is(err, ErrSocketActive) {
			return nil, session.NewError(session.CodeOwnerUnavailable, "owner", "an active owner is serving control socket "+socketPath, "connect to the existing owner or stop it first")
		}
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("bind owner control socket: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("secure owner control socket: %w", err)
	}
	claimed = true
	return &Owner{lock: lock, listener: listener, instanceID: instanceID, socketPath: socketPath}, nil
}

// InstanceID identifies this foreground owner and fences replacement sockets.
func (o *Owner) InstanceID() string {
	if o == nil {
		return ""
	}
	return o.instanceID
}

// Listener returns the owner socket. The owner retains responsibility for its
// lifetime and must call Close after all accepted connections have stopped.
func (o *Owner) Listener() *net.UnixListener {
	if o == nil {
		return nil
	}
	return o.listener
}

// SessionsStore returns a store bound to this owner's lifetime lock.
func (o *Owner) SessionsStore(path string) *store.SessionsStore {
	if o == nil {
		return nil
	}
	return store.NewOwnerSessionsStore(path, o.lock)
}

// Close stops accepting new connections before releasing the owner lock.
func (o *Owner) Close() error {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.closed = true
	listenerErr := o.listener.Close()
	if errors.Is(listenerErr, net.ErrClosed) {
		listenerErr = nil
	}
	token, _ := o.lock.ReadToken()
	if strings.HasPrefix(token, o.instanceID) {
		_ = os.Remove(o.socketPath)
	}
	lockErr := o.lock.Release()
	return errors.Join(listenerErr, lockErr)
}

func newInstanceID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
