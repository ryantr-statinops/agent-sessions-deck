//go:build linux

package ipc

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/store"
)

func TestBootstrapTakesLifetimeLockBeforeBindingSocket(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{StateDir: filepath.Join(root, "state")}
	socket := filepath.Join(root, "runtime", "control.sock")
	owner, err := Bootstrap(paths, socket)
	if err != nil {
		t.Fatalf("first owner bootstrap: %v", err)
	}
	defer owner.Close()
	if owner.InstanceID() == "" {
		t.Fatal("owner instance ID is empty")
	}
	info, err := os.Stat(socket)
	if err != nil {
		t.Fatalf("owner socket stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("owner socket mode/type = %v, want socket mode 0600", info.Mode())
	}
	dir, err := os.Stat(filepath.Dir(socket))
	if err != nil {
		t.Fatalf("runtime directory stat: %v", err)
	}
	if dir.Mode().Perm() != 0o700 {
		t.Fatalf("runtime directory mode = %v, want 0700", dir.Mode())
	}

	second, err := Bootstrap(paths, socket)
	if second != nil {
		_ = second.Close()
		t.Fatal("second bootstrap acquired owner while first lifetime lock is held")
	}
	if !errors.Is(err, ErrOwnerLocked) || !errors.Is(err, store.ErrLocked) {
		t.Fatalf("second bootstrap error = %v, want owner-lock refusal", err)
	}
	if _, err := os.Stat(socket); err != nil {
		t.Fatalf("contending bootstrap removed the live owner's socket: %v", err)
	}
}

func TestBootstrapReleasesLockWhenSocketBindFails(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{StateDir: filepath.Join(root, "state")}
	socket := filepath.Join(root, "runtime", "not-a-socket")
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socket, []byte("do not unlink"), 0o600); err != nil {
		t.Fatal(err)
	}
	if owner, err := Bootstrap(paths, socket); err == nil {
		_ = owner.Close()
		t.Fatal("bootstrap replaced a pre-existing non-socket path")
	}
	if err := os.WriteFile(socket, []byte("still here"), 0o600); err != nil {
		t.Fatalf("socket-bind failure removed or replaced existing path: %v", err)
	}
	owner, err := Bootstrap(paths, filepath.Join(root, "runtime", "control.sock"))
	if err != nil {
		t.Fatalf("bootstrap after bind failure did not release lock: %v", err)
	}
	_ = owner.Close()
}

func TestConcurrentBootstrapHasOneOwner(t *testing.T) {
	root := t.TempDir()
	paths := config.Paths{StateDir: filepath.Join(root, "state")}
	socket := filepath.Join(root, "runtime", "control.sock")
	start := make(chan struct{})
	owners := make(chan *Owner, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			owner, err := Bootstrap(paths, socket)
			owners <- owner
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(owners)
	close(errs)
	winners := 0
	for owner := range owners {
		if owner != nil {
			winners++
			defer owner.Close()
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent bootstrap owners = %d, want exactly one", winners)
	}
	for err := range errs {
		if err != nil && !errors.Is(err, ErrOwnerLocked) {
			t.Fatalf("concurrent bootstrap returned unexpected error: %v", err)
		}
	}
}
