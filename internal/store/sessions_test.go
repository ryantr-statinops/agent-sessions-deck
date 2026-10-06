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
