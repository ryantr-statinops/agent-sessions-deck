package store

import (
	"fmt"
	"os"
)

// faults lets white-box tests inject disk failures at each step of the
// atomic replace, proving the previous image survives a failed step.
type faults struct {
	write   error
	sync    error
	rename  error
	chmod   error
}

// activeFaults is reset by tests; nil in production.
var activeFaults *faults

// writeFileAtomic writes data through a temp file in the same directory:
// exclusive-create temp, write, chmod 0600, fsync, atomic rename, directory
// sync. A failure at any step keeps the previous file untouched; a failure
// never presents a truncated file as current.
func writeFileAtomic(dir, path string, perm os.FileMode, data []byte) error {
	if err := EnsurePrivateDir(dir); err != nil {
		return err
	}
	tmp, err := CreatePrivateFile(path + ".tmp." + tmpSuffix())
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if activeFaults != nil && activeFaults.write != nil {
		cleanup()
		return fmt.Errorf("injected write fault: %w", activeFaults.write)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if activeFaults != nil && activeFaults.sync != nil {
		cleanup()
		return fmt.Errorf("injected sync fault: %w", activeFaults.sync)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("fsync %s: %w", tmpName, err)
	}
	if activeFaults != nil && activeFaults.chmod != nil {
		cleanup()
		return fmt.Errorf("injected chmod fault: %w", activeFaults.chmod)
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if activeFaults != nil && activeFaults.rename != nil {
		os.Remove(tmpName)
		return fmt.Errorf("injected rename fault: %w", activeFaults.rename)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	// Directory fsync is best-effort: a dirsync failure cannot roll back the
	// rename, so it never fails the write. The rename itself is what must
	// survive; the directory entry is recovered on the next sync.
	syncDir(dir)
	return nil
}

// syncDir fsyncs the directory so the rename itself is durable. It is
// best-effort: some filesystems refuse directory fsync, and that must not
// fail the write.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer d.Close()
	d.Sync()
}

// tmpSuffix returns a per-attempt uniqueness tag so a failed previous
// attempt's temp file never collides.
var tmpCounter uint64

func tmpSuffix() string {
	tmpCounter++
	return fmt.Sprintf("%d", tmpCounter)
}
