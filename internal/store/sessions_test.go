package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func sampleSession(t *testing.T) session.Session {
	t.Helper()
	s, err := session.New("s-1", "demo", agent.ID("opencode"), workspace.ID("/tmp/ws-1"), time.Now().UTC().Truncate(time.Second))
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	return s
}

func newTestStore(t *testing.T) *SessionsStore {
	t.Helper()
	dir := t.TempDir()
	return NewSessionsStore(filepath.Join(dir, "sessions.json"), filepath.Join(dir, ".lock"))
}

func TestSessionsStoreRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	s1 := sampleSession(t)
	rev, err := st.Commit(ctx, []session.Session{s1}, 0)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if rev != 1 {
		t.Fatalf("rev = %d", rev)
	}
	loaded, loadedRev, err := st.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loadedRev != 1 || len(loaded) != 1 || loaded[0].ID != "s-1" || loaded[0].Name != "demo" {
		t.Fatalf("loaded = %+v rev=%d", loaded, loadedRev)
	}
}

func TestSessionsStoreRevisionConflict(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.Commit(ctx, []session.Session{sampleSession(t)}, 0); err != nil {
		t.Fatal(err)
	}
	_, err := st.Commit(ctx, []session.Session{}, 0)
	if err == nil {
		t.Fatal("stale revision must conflict")
	}
	var conflict *RevisionConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v", err)
	}
}

func TestSessionsStoreFutureSchemaFailsSafe(t *testing.T) {
	st := newTestStore(t)
	if err := os.WriteFile(st.path, []byte(`{"schema_version":99,"revision":1,"sessions":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := st.Load(ctxNoop())
	if err == nil {
		t.Fatal("future schema must fail safe")
	}
	var future *FutureSchemaError
	if !errors.As(err, &future) {
		t.Fatalf("err = %v", err)
	}
	data, _ := os.ReadFile(st.path)
	if !strings.Contains(string(data), "schema_version\":99") {
		t.Fatal("file must be preserved, not reset")
	}
}

func TestSessionsStoreCorruptImagePreserved(t *testing.T) {
	st := newTestStore(t)
	os.WriteFile(st.path, []byte(`{"schema_version":1,"revision":4,"sessions":[{"id":"x"`), 0o600)
	_, _, err := st.Load(ctxNoop())
	if err == nil {
		t.Fatal("corrupt JSON must fail")
	}
	var corrupt *CorruptStateError
	if !errors.As(err, &corrupt) {
		t.Fatalf("err = %v", err)
	}
	data, _ := os.ReadFile(st.path)
	if !strings.Contains(string(data), `"id":"x"`) {
		t.Fatal("corrupt image must be preserved as-is")
	}
}

func TestSessionsStoreUnknownEnvelopeFieldRefused(t *testing.T) {
	st := newTestStore(t)
	os.WriteFile(st.path, []byte(`{"schema_version":1,"revision":1,"sessions":[],"extra":1}`), 0o600)
	if _, _, err := st.Load(ctxNoop()); err == nil {
		t.Fatal("unknown envelope field must be refused")
	}
}

func TestSessionsStoreDeleteEnforcesT20(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	s := sampleSession(t)
	s.Attempts = []session.Attempt{session.NewAttempt(1, time.Now().UTC().Truncate(time.Second))}
	s.Generation = 1
	if _, err := st.Commit(ctx, []session.Session{s}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Delete(ctx, s.ID, 1); err == nil {
		t.Fatal("delete of an active (created/starting) session must be refused")
	}
}

func TestSessionsStoreCommitTakesLock(t *testing.T) {
	st := newTestStore(t)
	preLock, err := AcquireLock(st.lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer preLock.Release()
	if _, err := st.Commit(context.Background(), []session.Session{}, 0); err != ErrLocked {
		t.Fatalf("commit under held lock = %v, want ErrLocked", err)
	}
}

func ctxNoop() context.Context { return context.Background() }

func TestSessionsStoreCommitCreatesMissingStateHome(t *testing.T) {
	root := t.TempDir()
	stateDir := filepath.Join(root, ".local", "state", "asd")
	st := NewSessionsStore(filepath.Join(stateDir, "sessions.json"), filepath.Join(stateDir, ".lock"))
	revision, err := st.Commit(context.Background(), []session.Session{}, 0)
	if err != nil {
		t.Fatalf("first Commit in absent state home: %v", err)
	}
	if revision != 1 {
		t.Fatalf("revision = %d, want 1", revision)
	}
}

func TestOwnerSessionsStoreOperationsRunWhileLockHeld(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock")
	lock, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	st := NewOwnerSessionsStore(filepath.Join(dir, "sessions.json"), lock)
	ctx := context.Background()
	if _, _, err := st.Load(ctx); err != nil {
		t.Fatalf("owner Load: %v", err)
	}
	exited := sampleSession(t)
	end := time.Now().UTC().Truncate(time.Second)
	attempt := session.NewAttempt(1, end.Add(-time.Minute))
	attempt.ExitCode = 0
	attempt.HasExitCode = true
	attempt.Lifecycle = session.LifecycleExited
	attempt.EndedAt = end
	attempt.Reason = session.Reason{Kind: session.ReasonNaturalExit, ExitCode: &attempt.ExitCode}
	exited.Attempts = []session.Attempt{attempt}
	exited.Generation = 1
	rev, err := st.Commit(ctx, []session.Session{exited}, 0)
	if err != nil || rev != 1 {
		t.Fatalf("owner Commit: %v %d", err, rev)
	}
	if _, _, err := st.Load(ctx); err != nil {
		t.Fatalf("owner Load after Commit: %v", err)
	}
	next, err := st.Delete(ctx, exited.ID, 1)
	if err != nil || next != 2 {
		t.Fatalf("owner Delete: %v %d", err, next)
	}
}

func TestOfflineStoreRefusedWhileOwnerLive(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock")
	lock, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	sessions := NewSessionsStore(filepath.Join(dir, "sessions.json"), lockPath)
	if _, _, err := sessions.Load(context.Background()); err != ErrLocked {
		t.Fatalf("offline Load = %v, want ErrLocked", err)
	}
	if _, err := sessions.Commit(context.Background(), []session.Session{}, 0); err != ErrLocked {
		t.Fatalf("offline Commit = %v, want ErrLocked", err)
	}
	st := NewStateStore(filepath.Join(dir, "state.json"), lockPath)
	if _, err := st.Load(context.Background()); err != ErrLocked {
		t.Fatalf("offline state Load = %v, want ErrLocked", err)
	}
	if _, err := st.AddRecent(context.Background(), "/tmp/ws", time.Now()); err != ErrLocked {
		t.Fatalf("offline AddRecent = %v, want ErrLocked", err)
	}
}

func TestOwnerStateStoreOperationsRunWhileLockHeld(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireLock(filepath.Join(dir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	st := NewOwnerStateStore(filepath.Join(dir, "state.json"), lock)
	ctx := context.Background()
	if _, err := st.Load(ctx); err != nil {
		t.Fatalf("owner Load: %v", err)
	}
	rev, err := st.AddRecent(ctx, "/tmp/ws", time.Now().UTC())
	if err != nil || rev != 1 {
		t.Fatalf("owner AddRecent: %v %d", err, rev)
	}
	loaded, err := st.Load(ctx)
	if err != nil || len(loaded.RecentWorkspaces) != 1 {
		t.Fatalf("owner Load: %v %+v", err, loaded)
	}
	rev, err = st.Commit(ctx, StatePayload{}, 1)
	if err != nil || rev != 2 {
		t.Fatalf("owner Commit: %v %d", err, rev)
	}
}

func TestSessionsStoreRejectsTrailingJSON(t *testing.T) {
	st := newTestStore(t)
	data := []byte(`{"schema_version":1,"revision":1,"sessions":[]} {}`)
	if err := os.WriteFile(st.path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Load(context.Background()); err == nil {
		t.Fatal("trailing JSON value must be rejected")
	}
}

func TestSessionsStoreRejectsNullPayload(t *testing.T) {
	st := newTestStore(t)
	if err := os.WriteFile(st.path, []byte(`{"schema_version":1,"revision":1,"sessions":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Load(context.Background()); err == nil {
		t.Fatal("sessions payload must be an array, not null")
	}
}

func TestOwnerStoresRefuseOperationsAfterRelease(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock")
	lock, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	sessions := NewOwnerSessionsStore(filepath.Join(dir, "sessions.json"), lock)
	state := NewOwnerStateStore(filepath.Join(dir, "state.json"), lock)
	ctx := context.Background()
	if _, _, err := sessions.Load(ctx); err != nil {
		t.Fatalf("owner Load before release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, _, err := sessions.Load(ctx); err != ErrLockReleased {
		t.Fatalf("owner Load after release = %v, want ErrLockReleased", err)
	}
	if _, err := sessions.Commit(ctx, []session.Session{}, 0); err != ErrLockReleased {
		t.Fatalf("owner Commit after release = %v, want ErrLockReleased", err)
	}
	if _, err := sessions.Delete(ctx, "s1", 0); err != ErrLockReleased {
		t.Fatalf("owner Delete after release = %v, want ErrLockReleased", err)
	}
	if _, err := state.Load(ctx); err != ErrLockReleased {
		t.Fatalf("owner state Load after release = %v, want ErrLockReleased", err)
	}
	if _, err := state.AddRecent(ctx, "/tmp/ws", time.Now()); err != ErrLockReleased {
		t.Fatalf("owner AddRecent after release = %v, want ErrLockReleased", err)
	}
	if _, err := state.Commit(ctx, StatePayload{}, 0); err != ErrLockReleased {
		t.Fatalf("owner state Commit after release = %v, want ErrLockReleased", err)
	}
}

func TestMigrateLegacyWithLockRefusedAfterRelease(t *testing.T) {
	dir := t.TempDir()
	lock, err := AcquireLock(filepath.Join(dir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "legacy.json")
	if err := os.WriteFile(legacy, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	_, err = MigrateLegacySessionsWithLock(context.Background(), legacy, filepath.Join(dir, "sessions.json"), lock)
	if err != ErrLockReleased {
		t.Fatalf("migration after release = %v, want ErrLockReleased", err)
	}
}

func TestLockLifetimeSmokeOwnerCommitReloadOfflineRefusalRelease(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".lock")
	sessionsPath := filepath.Join(dir, "sessions.json")
	statePath := filepath.Join(dir, "state.json")
	ctx := context.Background()

	lock, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Log("SMOKE: owner lock acquired")
	owner := NewOwnerSessionsStore(sessionsPath, lock)
	ownerState := NewOwnerStateStore(statePath, lock)
	exited := sampleSession(t)
	if rev, err := owner.Commit(ctx, []session.Session{exited}, 0); err != nil || rev != 1 {
		t.Fatalf("owner commit: %v %d", err, rev)
	}
	t.Log("SMOKE: owner commit revision 1")
	if got, rev, err := owner.Load(ctx); err != nil || rev != 1 || len(got) != 1 {
		t.Fatalf("owner reload: %v %d %d", err, rev, len(got))
	}
	if _, err := ownerState.AddRecent(ctx, "/tmp/ws", time.Now().UTC()); err != nil {
		t.Fatalf("owner AddRecent: %v", err)
	}
	t.Log("SMOKE: owner commit reloaded with 1 session and 1 recent workspace")

	offline := NewSessionsStore(sessionsPath, lockPath)
	if _, _, err := offline.Load(ctx); err != ErrLocked {
		t.Fatalf("offline load = %v, want ErrLocked", err)
	}
	if _, err := NewStateStore(statePath, lockPath).Load(ctx); err != ErrLocked {
		t.Fatalf("offline state load = %v, want ErrLocked", err)
	}
	t.Log("SMOKE: offline stores refused while owner live (ErrLocked)")

	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	t.Log("SMOKE: lock released, Owned =", lock.Owned())
	if _, _, err := owner.Load(ctx); err != ErrLockReleased {
		t.Fatalf("owner load after release = %v, want ErrLockReleased", err)
	}
	if rev, err := offline.Commit(ctx, []session.Session{}, 1); err != nil || rev != 2 {
		t.Fatalf("offline commit after release: %v %d", err, rev)
	}
	t.Log("SMOKE: offline mutation succeeds after release (expected revision 2)")
	offlineState := NewStateStore(statePath, lockPath)
	if got, err := offlineState.Load(ctx); err != nil || len(got.RecentWorkspaces) != 1 {
		t.Fatalf("offline reload after release: %v %d", err, len(got.RecentWorkspaces))
	}
	t.Log("SMOKE: offline reload after release sees persisted state")
	relocked, err := AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	defer relocked.Release()
	t.Log("SMOKE: lock re-acquirable after release")
}
