package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestAtomicWriteFaultKeepsPriorImage(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.Commit(ctx, []session.Session{sampleSession(t)}, 0); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(st.path)
	for _, inj := range []*faults{
		{write: errors.New("disk full")},
		{sync: errors.New("fsync failed")},
		{rename: errors.New("rename failed")},
	} {
		activeFaults = inj
		_, err := st.Commit(ctx, []session.Session{}, 1)
		activeFaults = nil
		if err == nil {
			t.Fatalf("commit with %+v must fail", inj)
		}
		after, _ := os.ReadFile(st.path)
		if string(after) != string(before) {
			t.Fatalf("prior image must survive %+v", inj)
		}
		if _, _, err := st.Load(ctx); err != nil {
			t.Fatalf("store must remain loadable after %+v: %v", inj, err)
		}
	}
}

func TestAtomicWriteFaultMidWriteLeavesNoHalfJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.json")
	data := strings.Repeat("abc", 4096) + "{}"
	activeFaults = &faults{write: nil, sync: errors.New("sync failed")}
	if err := writeFileAtomic(dir, path, 0o600, []byte(data)); err == nil {
		t.Fatal("expected fault")
	}
	activeFaults = nil
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".asd-tmp-") || strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("temp file leaked: %s", e.Name())
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no file should appear: %v", err)
	}
}

func TestAtomicWriteConcurrentWritersRefused(t *testing.T) {
	st := newTestStore(t)
	preLock, err := AcquireLock(st.lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer preLock.Release()
	if _, err := st.Commit(context.Background(), []session.Session{}, 0); err != ErrLocked {
		t.Fatalf("sessions commit = %v, want ErrLocked", err)
	}
	if _, err := newTestStateStore(t).Commit(context.Background(), StatePayload{}, 0); err != nil {
		// different lock file; should succeed
		t.Fatalf("separate state store = %v", err)
	}
}

func TestAtomicWriteProducesCompleteValidImage(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.Commit(ctx, []session.Session{sampleSession(t)}, 0); err != nil {
		t.Fatal(err)
	}
	loaded, rev, err := st.Load(ctx)
	if err != nil || rev != 1 || len(loaded) != 1 {
		t.Fatalf("load = %v, %d, %v", loaded, rev, err)
	}
	dir := filepath.Dir(st.path)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("temp file leaked: %s", e.Name())
		}
	}
}

func TestAtomicRenameIsFaultTolerantOnWriteFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	good := `{"schema_version":1,"revision":1,"recent_workspaces":[]}`
	if err := writeFileAtomic(dir, path, 0o600, []byte(good)); err != nil {
		t.Fatal(err)
	}
	activeFaults = &faults{write: errors.New("disk full")}
	err := writeFileAtomic(dir, path, 0o600, []byte("{broken"))
	activeFaults = nil
	if err == nil {
		t.Fatal("injected failure must surface")
	}
	data, _ := os.ReadFile(path)
	if string(data) != good {
		t.Fatalf("prior image = %q", data)
	}
}

func TestAtomicWritePartialFailureCleansTempAndKeepsOldImage(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.Commit(ctx, []session.Session{sampleSession(t)}, 0); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(st.path)
	activeFaults = &faults{write: errors.New("short write")}
	_, err := st.Commit(ctx, []session.Session{}, 1)
	activeFaults = nil
	if err == nil {
		t.Fatal("injected partial write must fail")
	}
	after, _ := os.ReadFile(st.path)
	if string(after) != string(before) {
		t.Fatal("old image must survive a partial write")
	}
	entries, _ := os.ReadDir(filepath.Dir(st.path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") || strings.HasPrefix(e.Name(), ".asd-tmp-") {
			t.Fatalf("temp file leaked: %s", e.Name())
		}
	}
	if _, _, err := st.Load(ctx); err != nil {
		t.Fatalf("store loadable after partial write: %v", err)
	}
}

func TestAtomicWriteConcurrentSeparateHomes(t *testing.T) {
	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			dir := t.TempDir()
			st := NewSessionsStore(filepath.Join(dir, "sessions.json"), filepath.Join(dir, ".lock"))
			st2 := NewStateStore(filepath.Join(dir, "state.json"), filepath.Join(dir, ".lock"))
			ctx := context.Background()
			for rev := uint64(0); rev < 3; rev++ {
				if _, err := st.Commit(ctx, []session.Session{}, rev); err != nil {
					t.Errorf("commit: %v", err)
					return
				}
				if _, err := st2.AddRecent(ctx, filepath.Join(dir, "ws"), time.Now().UTC()); err != nil {
					t.Errorf("addrecent: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}
