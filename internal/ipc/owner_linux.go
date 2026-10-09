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
	"sync"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/store"
)

var ErrOwnerLocked = errors.New("an ASD foreground owner already holds the state home lock")

// Owner is the lifetime claim for one foreground runtime. Bootstrap acquires the
// state-home lock before binding the control socket; socket policy is applied by
// the caller until the dedicated endpoint/path layer is installed.
type Owner struct {
	mu         sync.Mutex
	lock       *store.Lock
	listener   *net.UnixListener
	instanceID string
	closed     bool
}

// Bootstrap atomically claims the state home and binds its Unix listener. It
// never unlinks an existing socket: stale-socket classification is a separate
// checked operation after the lock has been acquired.
func Bootstrap(paths config.Paths, socketPath string) (*Owner, error) {
	if paths.StateDir == "" || !filepath.IsAbs(paths.StateDir) {
		return nil, errors.New("owner bootstrap requires an absolute state directory")
	}
	if socketPath == "" || !filepath.IsAbs(socketPath) {
		return nil, errors.New("owner bootstrap requires an absolute socket path")
	}
	lock, err := store.AcquireLock(paths.LockPath())
	if err != nil {
		if errors.Is(err, store.ErrLocked) {
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
	if err := os.MkdirAll(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	if err := os.Chmod(filepath.Dir(socketPath), 0o700); err != nil {
		return nil, fmt.Errorf("secure socket directory: %w", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("bind owner control socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("secure owner control socket: %w", err)
	}
	claimed = true
	return &Owner{lock: lock, listener: listener, instanceID: instanceID}, nil
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
