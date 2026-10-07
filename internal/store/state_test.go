package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestStateStore(t *testing.T) *StateStore {
	t.Helper()
	dir := t.TempDir()
	return NewStateStore(filepath.Join(dir, "state.json"), filepath.Join(dir, ".lock"))
}

func TestStateStoreEmptyDefaults(t *testing.T) {
	st := newTestStateStore(t)
	loaded, err := st.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 0 || loaded.SchemaVersion != StateSchemaVersion || len(loaded.RecentWorkspaces) != 0 {
		t.Fatalf("empty state = %+v", loaded)
	}
}

func TestStateStoreRecentAccumulatesAndBumpsRevision(t *testing.T) {
	st := newTestStateStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	rev, err := st.AddRecent(ctx, "/tmp/a", now)
	if err != nil || rev != 1 {
		t.Fatalf("AddRecent = %d, %v", rev, err)
	}
	rev, err = st.AddRecent(ctx, "/tmp/b", now.Add(time.Minute))
	if err != nil || rev != 2 {
		t.Fatalf("AddRecent = %d, %v", rev, err)
	}
	rev, err = st.AddRecent(ctx, "/tmp/a", now.Add(2*time.Minute))
	if err != nil || rev != 3 {
		t.Fatalf("AddRecent = %d, %v", rev, err)
	}
	loaded, err := st.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.RecentWorkspaces) != 2 || loaded.RecentWorkspaces[0].Path != "/tmp/a" || loaded.RecentWorkspaces[1].Path != "/tmp/b" {
		t.Fatalf("recents = %+v", loaded.RecentWorkspaces)
	}
	if loaded.Revision != 3 {
		t.Fatalf("revision = %d", loaded.Revision)
	}
}

func TestStateStoreConflict(t *testing.T) {
	st := newTestStateStore(t)
	ctx := context.Background()
	if _, err := st.AddRecent(ctx, "/tmp/a", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	stale := StatePayload{SchemaVersion: StateSchemaVersion, Revision: 0}
	if _, err := st.Commit(ctx, stale, 0); err == nil {
		t.Fatal("stale revision must conflict")
	} else {
		var conflict *RevisionConflictError
		if !errors.As(err, &conflict) {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestStateStoreCorruptAndFutureVersion(t *testing.T) {
	st := newTestStateStore(t)
	os.WriteFile(st.path, []byte(`{"schema_version":1,"revision":1,"recent_workspaces":[{"path":""`), 0o600)
	if _, err := st.Load(context.Background()); err == nil {
		t.Fatal("corrupt state must fail")
	}
	os.WriteFile(st.path, []byte(`{"schema_version":42,"revision":1,"recent_workspaces":[]}`), 0o600)
	if _, err := st.Load(context.Background()); err == nil {
		t.Fatal("future version must fail")
	} else {
		var future *FutureSchemaError
		if !errors.As(err, &future) {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestStateStoreBoundedRecentList(t *testing.T) {
	st := newTestStateStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < maxRecentWorkspaces+5; i++ {
		if _, err := st.AddRecent(ctx, filepath.Join("/tmp", string(rune('a'+i%26)), "x")+string(rune(i)), now.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	loaded, _ := st.Load(ctx)
	if len(loaded.RecentWorkspaces) > maxRecentWorkspaces {
		t.Fatalf("recent list = %d", len(loaded.RecentWorkspaces))
	}
}

func TestStateStoreRejectsTrailingJSON(t *testing.T) {
	st := newTestStateStore(t)
	if err := os.WriteFile(st.path, []byte(`{"schema_version":1,"revision":1,"recent_workspaces":[]} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Load(context.Background()); err == nil {
		t.Fatal("trailing JSON value must be rejected")
	}
}

func TestStateStoreRejectsNullOrMissingRecentWorkspaces(t *testing.T) {
	st := newTestStateStore(t)
	for _, image := range []string{
		`{"schema_version":1,"revision":1,"recent_workspaces":null}`,
		`{"schema_version":1,"revision":1}`,
	} {
		if err := os.WriteFile(st.path, []byte(image), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Load(context.Background()); err == nil {
			t.Fatalf("invalid state image accepted: %s", image)
		}
	}
}

func TestStateStoreRejectsEmptyRecentPathAndPersistsUTC(t *testing.T) {
	st := newTestStateStore(t)
	if _, err := st.AddRecent(context.Background(), "", time.Now()); err == nil {
		t.Fatal("empty recent path must be rejected before writing")
	}
	zone := time.FixedZone("plus-two", 2*60*60)
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, zone)
	if _, err := st.AddRecent(context.Background(), "/tmp/workspace", at); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.RecentWorkspaces[0].LastUsed.Location(); got != time.UTC {
		t.Fatalf("LastUsed location = %v, want UTC", got)
	}
}
