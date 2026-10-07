package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func legacyFixture(t *testing.T, dir string) (string, *session.Session) {
	t.Helper()
	s, err := session.New("s-legacy", "legacy-demo", agent.ID("opencode"), workspace.ID("/tmp/ws"), time.Now().UTC().Truncate(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	end := time.Now().UTC().Truncate(time.Second)
	attempt := session.NewAttempt(1, end.Add(-time.Minute))
	attempt.ExitCode = 3
	attempt.HasExitCode = true
	attempt.Lifecycle = session.LifecycleExited
	attempt.EndedAt = end
	attempt.Reason = session.Reason{Kind: session.ReasonNaturalExit, ExitCode: &attempt.ExitCode}
	s.Attempts = []session.Attempt{attempt}
	s.Generation = 1
	var buf bytes.Buffer
	if err := session.EncodeSessions(&buf, []session.Session{s}); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(dir, "sessions.json")
	if err := os.WriteFile(legacy, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return legacy, &s
}

func TestMigrateLegacyImportsOnlyWhenDestinationAbsent(t *testing.T) {
	dir := t.TempDir()
	legacy, original := legacyFixture(t, dir)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "sessions.json")
	lock := filepath.Join(destDir, ".lock")
	imported, err := MigrateLegacySessions(context.Background(), legacy, dest, lock)
	if err != nil || !imported {
		t.Fatalf("import = %v, %v", imported, err)
	}
	sessions, rev, err := newTestStoreAt(dest, lock).Load(context.Background())
	if err != nil || rev != 1 {
		t.Fatalf("load = %v %d", err, rev)
	}
	if len(sessions) != 1 || sessions[0].ID != original.ID || sessions[0].Name != original.Name {
		t.Fatalf("round-trip lost identity: %+v", sessions)
	}
	if len(sessions[0].Attempts) != 1 || sessions[0].Attempts[0].ExitCode != 3 || !sessions[0].Attempts[0].HasExitCode {
		t.Fatalf("exit metadata lost: %+v", sessions[0].Attempts)
	}
	if _, err := os.Stat(legacy + ".backup"); err != nil {
		t.Fatal("backup must be kept")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal("source must be preserved")
	}
	// Second run: destination exists now, so no re-import and no merge.
	imported, err = MigrateLegacySessions(context.Background(), legacy, dest, lock)
	if err != nil || imported {
		t.Fatalf("second import = %v, %v", imported, err)
	}
}

func TestMigrateLegacyLeavesValidDestinationAlone(t *testing.T) {
	dir := t.TempDir()
	legacy, _ := legacyFixture(t, dir)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "sessions.json")
	lock := filepath.Join(destDir, ".lock")
	if _, err := newTestStoreAt(dest, lock).Commit(context.Background(), []session.Session{}, 0); err != nil {
		t.Fatal(err)
	}
	imported, err := MigrateLegacySessions(context.Background(), legacy, dest, lock)
	if err != nil || imported {
		t.Fatalf("import = %v, %v", imported, err)
	}
	data, _ := os.ReadFile(dest)
	if !bytes.Contains(data, []byte(`"revision": 1`)) {
		t.Fatalf("destination must be authoritative: %s", data)
	}
}

func TestMigrateLegacyCorruptSourceFailsSafe(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "sessions.json")
	os.WriteFile(legacy, []byte(`[{"id":"s-1",`), 0o600)
	dest := filepath.Join(t.TempDir(), "sessions.json")
	if _, err := MigrateLegacySessions(context.Background(), legacy, dest, filepath.Join(t.TempDir(), ".lock")); err == nil {
		t.Fatal("corrupt legacy must fail")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatal("dest must not be created from corrupt source")
	}
	// Source preserved
	if data, _ := os.ReadFile(legacy); !bytes.Contains(data, []byte(`"id":"s-1"`)) {
		t.Fatal("source must be preserved")
	}
}

func newTestStoreAt(path, lockPath string) *SessionsStore {
	return NewSessionsStore(path, lockPath)
}

func TestMigrateLegacyBackupIsPrivateAndFresh(t *testing.T) {
	dir := t.TempDir()
	legacy, _ := legacyFixture(t, dir)
	dest := filepath.Join(t.TempDir(), "sessions.json")
	imported, err := MigrateLegacySessions(context.Background(), legacy, dest, filepath.Join(t.TempDir(), ".lock"))
	if err != nil || !imported {
		t.Fatalf("import = %v, %v", imported, err)
	}
	info, err := os.Stat(legacy + ".backup")
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestMigrateLegacyDoesNotOverwriteDifferentExistingBackup(t *testing.T) {
	dir := t.TempDir()
	legacy, _ := legacyFixture(t, dir)
	if err := os.WriteFile(legacy+".backup", []byte("different backup content"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "sessions.json")
	imported, err := MigrateLegacySessions(context.Background(), legacy, dest, filepath.Join(t.TempDir(), ".lock"))
	if err != nil || !imported {
		t.Fatalf("import = %v, %v", imported, err)
	}
	data, err := os.ReadFile(legacy + ".backup")
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "different backup content" {
		t.Fatal("existing backup must not be overwritten")
	}
	if _, err := os.Stat(legacy + ".backup.1"); err != nil {
		t.Fatalf("fresh backup should land at .backup.1: %v", err)
	}
}

func TestMigrateLegacyDanglingDestinationSymlinkIsPresent(t *testing.T) {
	dir := t.TempDir()
	legacy, _ := legacyFixture(t, dir)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "sessions.json")
	if err := os.Symlink(filepath.Join(destDir, "no-such-target"), dest); err != nil {
		t.Fatal(err)
	}
	imported, err := MigrateLegacySessions(context.Background(), legacy, dest, filepath.Join(destDir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	if imported {
		t.Fatal("dangling destination symlink must count as present")
	}
	if _, err := os.Lstat(legacy + ".backup"); err == nil {
		t.Fatal("no backup should be created when the destination exists")
	}
}

func TestMigrateLegacyUnderOwnerLockUsesHeldLock(t *testing.T) {
	dir := t.TempDir()
	legacy, _ := legacyFixture(t, dir)
	destDir := t.TempDir()
	dest := filepath.Join(destDir, "sessions.json")
	lock, err := AcquireLock(filepath.Join(destDir, ".lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	imported, err := MigrateLegacySessionsWithLock(context.Background(), legacy, dest, lock)
	if err != nil || !imported {
		t.Fatalf("import = %v, %v", imported, err)
	}
	sessions, rev, err := NewOwnerSessionsStore(dest, lock).Load(context.Background())
	if err != nil || rev != 1 || len(sessions) != 1 {
		t.Fatalf("load = %v %d %v", err, rev, sessions)
	}
}
