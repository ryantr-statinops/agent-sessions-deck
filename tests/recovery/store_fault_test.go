//go:build linux

package recovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestCorruptStoreRefusesStartupWithoutResettingFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	paths := config.Paths{
		StateDir:   t.TempDir(),
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
	cfg := config.Default()

	// Write garbage / malformed JSON to sessions file
	corruptContent := []byte(`{"version": 1, "sessions": [{"invalid_json": `)
	if err := os.WriteFile(paths.SessionsFile(), corruptContent, 0644); err != nil {
		t.Fatalf("failed to write corrupt state file: %v", err)
	}

	// Attempt owner startup: must refuse with CORRUPT_STATE
	rt, startErr := owner.Start(ctx, paths, cfg)
	if startErr == nil {
		_ = rt.Close()
		t.Fatal("owner started successfully over corrupt state, want refusal")
	}

	if code := session.CodeOf(startErr); code != session.CodeCorruptState {
		t.Fatalf("startup error code = %s, want %s: %v", code, session.CodeCorruptState, startErr)
	}

	// Verify that ASD NEVER overwrote or reset the corrupt file to make the error disappear
	afterContent, err := os.ReadFile(paths.SessionsFile())
	if err != nil {
		t.Fatalf("failed to read back state file: %v", err)
	}
	if string(afterContent) != string(corruptContent) {
		t.Fatalf("ASD modified corrupt file: got %q, want %q", string(afterContent), string(corruptContent))
	}
}

func TestReadOnlyStoreLatchesMutationsWhileReadsStayAvailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	paths := config.Paths{
		StateDir:   t.TempDir(),
		ConfigDir:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
	cfg := config.Default()
	cfg.Agents = []config.Agent{
		{
			ID:         "sh-agent",
			Name:       "Shell Agent",
			Executable: "/bin/sh",
		},
	}

	rt, err := owner.Start(ctx, paths, cfg)
	if err != nil {
		t.Fatalf("failed to start owner: %v", err)
	}
	defer func() { _ = rt.Close() }()

	c := rt.Client()
	ws := t.TempDir()

	// 1. Create initial session successfully while writable
	res, err := c.Create(ctx, app.CreateRequest{
		Agent:         agent.ID("sh-agent"),
		WorkspacePath: ws,
		Name:          "session-initial",
		ExtraArgs:     []string{"-c", "exit 0"},
	})
	if err != nil {
		t.Fatalf("initial create failed: %v", err)
	}
	id := string(res.Session.ID)

	// Wait for session to finish
	time.Sleep(100 * time.Millisecond)

	// 2. Simulate disk-full / read-only state dir by removing write permission
	if err := os.Chmod(paths.StateDir, 0555); err != nil {
		t.Fatalf("failed to chmod state dir: %v", err)
	}
	defer func() {
		_ = os.Chmod(paths.StateDir, 0755)
		// Clean up files in state dir if needed
		_ = os.Chmod(filepath.Join(paths.StateDir, "sessions.json"), 0644)
	}()

	// Also make the sessions file read-only
	_ = os.Chmod(paths.SessionsFile(), 0444)

	// 3. Attempt mutation: must fail and latch write fault
	_, renameErr := c.Rename(ctx, app.RenameRequest{Ref: id, Name: "new-name"})
	if renameErr == nil {
		t.Fatal("Rename succeeded on read-only store, want write failure")
	}

	// 4. Any subsequent mutation must be refused with OWNER_UNAVAILABLE latch
	_, secondMutationErr := c.Rename(ctx, app.RenameRequest{Ref: id, Name: "another-name"})
	if secondMutationErr == nil {
		t.Fatal("Second mutation succeeded, want OWNER_UNAVAILABLE latch refusal")
	}
	if code := session.CodeOf(secondMutationErr); code != session.CodeOwnerUnavailable {
		t.Fatalf("second mutation code = %s, want %s: %v", code, session.CodeOwnerUnavailable, secondMutationErr)
	}

	// 5. CRITICAL INVARIANT: Reads MUST stay available!
	// An operator needs to read the sessions to inspect what happened.
	listRes, listErr := c.List(ctx, app.ListRequest{})
	if listErr != nil {
		t.Fatalf("List failed on latched owner: %v", listErr)
	}
	if len(listRes.Snapshot.Sessions) != 1 {
		t.Fatalf("List returned %d sessions, want 1", len(listRes.Snapshot.Sessions))
	}

	getRes, getErr := c.Get(ctx, app.GetRequest{Ref: id})
	if getErr != nil {
		t.Fatalf("Get failed on latched owner: %v", getErr)
	}
	// The stored record that really exists on disk is untouched (still session-initial)
	if getRes.Stored.Name != "session-initial" {
		t.Fatalf("stored session name = %q, want session-initial (must not be corrupted on disk)", getRes.Stored.Name)
	}
	// The live session in memory holds the unpersisted attempt
	if getRes.Session.Name != "new-name" {
		t.Fatalf("live session name = %q, want new-name", getRes.Session.Name)
	}
}
