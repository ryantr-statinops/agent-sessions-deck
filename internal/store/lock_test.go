package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireLockRefusesConcurrentHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer first.Release()
	if _, err := AcquireLock(path); err != ErrLocked {
		t.Fatalf("second acquire = %v, want ErrLocked", err)
	}
	if !first.Owned() {
		t.Fatal("first lock should still own the file")
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	second, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	second.Release()
}

func TestLockFilePermissionsPrivate(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".lock")
	l, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer l.Release()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock file mode = %o", info.Mode().Perm())
	}
}

func TestEnsurePrivateDirCreatesPrivateTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatalf("EnsurePrivateDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %o", info.Mode().Perm())
	}
}

func TestEnsurePrivateDirPreservesExistingMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "keep")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatalf("EnsurePrivateDir: %v", err)
	}
	info, _ := os.Stat(dir)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("existing mode must be preserved, got %o", info.Mode().Perm())
	}
}

func TestEnsurePrivateDirRefusesNonDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(path); err == nil {
		t.Fatal("expected error for non-directory")
	}
}

func TestReleaseClearsOwnershipAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".lock")
	l, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	if !l.Owned() {
		t.Fatal("Owned = false before release")
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if l.Owned() {
		t.Fatal("Owned = true after release")
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	var nilLock *Lock
	if nilLock.Owned() {
		t.Fatal("nil lock Owned = true")
	}
	if err := nilLock.Release(); err != nil {
		t.Fatalf("nil Release: %v", err)
	}
}
