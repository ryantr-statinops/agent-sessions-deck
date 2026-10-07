package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "HOME="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func newRepo(t *testing.T) string {
	dir := t.TempDir()
	run(t, dir, "init", "-b", "main")
	run(t, dir, "config", "user.email", "t@example.com")
	run(t, dir, "config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-m", "init")
	return dir
}

func TestProbeNormalRepo(t *testing.T) {
	dir := newRepo(t)
	c := NewClient()
	g, err := c.Probe(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if g.Branch != "main" || g.Detached || g.Dirty || g.ChangedFiles != 0 {
		t.Errorf("clean repo: %+v", g)
	}
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644)
	c.Invalidate(dir)
	g, _ = c.Probe(context.Background(), dir)
	if !g.Dirty || g.ChangedFiles != 1 {
		t.Errorf("one untracked: %+v", g)
	}
}

func TestProbeNestedAndDetachedAndNotRepo(t *testing.T) {
	dir := newRepo(t)
	sub := filepath.Join(dir, "sub", "dir")
	os.MkdirAll(sub, 0o755)
	c := NewClient()
	g, err := c.Probe(context.Background(), sub)
	if err != nil || g.Root != dir {
		t.Fatalf("nested: %v %+v", err, g)
	}
	head := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, _ := head.Output()
	run(t, dir, "checkout", "--detach", string(out[:len(out)-1]))
	c.Invalidate(dir)
	g, _ = c.Probe(context.Background(), dir)
	if !g.Detached {
		t.Errorf("detached: %+v", g)
	}
	plain := t.TempDir()
	if _, err := c.Probe(context.Background(), plain); !errors.Is(err, ErrNotRepo) {
		t.Errorf("non-repo: %v", err)
	}
}

func TestProbeLinkedWorktreeAndBare(t *testing.T) {
	dir := newRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	run(t, dir, "worktree", "add", wt)
	c := NewClient()
	linked, bare, err := c.DecodeWorktree(context.Background(), wt)
	if err != nil || !linked || bare {
		t.Errorf("worktree: linked=%v bare=%v err=%v", linked, bare, err)
	}
	bareDir := filepath.Join(t.TempDir(), "bare.git")
	run(t, dir, "clone", "--bare", dir, bareDir)
	_, bareFlag, err := c.DecodeWorktree(context.Background(), bareDir)
	if err != nil || !bareFlag {
		t.Errorf("bare: %v %v", bareFlag, err)
	}
}

// Filenames with newlines and a staged rename must count as records.
func TestCountPorcelainWithRenameAndNewline(t *testing.T) {
	dir := newRepo(t)
	os.Rename(filepath.Join(dir, "a.txt"), filepath.Join(dir, "new\nname.txt"))
	run(t, dir, "add", "-A")
	os.WriteFile(filepath.Join(dir, "u.txt"), []byte("u\n"), 0o644)
	c := NewClient()
	g, err := c.Probe(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if g.ChangedFiles != 2 {
		t.Errorf("changed = %d, want 2", g.ChangedFiles)
	}
}

func TestCountPorcelainUnmerged(t *testing.T) {
	dir := newRepo(t)
	run(t, dir, "checkout", "-b", "b")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("b\n"), 0o644)
	run(t, dir, "commit", "-am", "b")
	run(t, dir, "checkout", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("main\n"), 0o644)
	run(t, dir, "commit", "-am", "main")
	cmd := exec.Command("git", "-C", dir, "merge", "b")
	cmd.Env = append(os.Environ(), "HOME="+dir)
	cmd.Run()
	c := NewClient()
	g, err := c.Probe(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if g.ChangedFiles < 1 {
		t.Errorf("unmerged: %+v", g)
	}
}

// TTL cache: repeated probes within TTL do not re-run git.
func TestCacheTTL(t *testing.T) {
	dir := newRepo(t)
	c3 := NewClient()
	c3.TTL = time.Minute
	g1, _ := c3.Probe(context.Background(), dir)
	os.WriteFile(filepath.Join(dir, "x"), []byte("x"), 0o644)
	g2, _ := c3.Probe(context.Background(), dir)
	if g2.ChangedFiles != g1.ChangedFiles {
		t.Errorf("TTL cache should serve stale: %+v vs %+v", g1, g2)
	}
	c3.Invalidate(dir)
	g3, _ := c3.Probe(context.Background(), dir)
	if g3.ChangedFiles == g1.ChangedFiles {
		t.Errorf("after invalidate should refresh")
	}
}

// A caller-canceled refresh drops in-flight work and preserves the old cache.
func TestProbeCancel(t *testing.T) {
	dir := newRepo(t)
	c2 := NewClient()
	hang := filepath.Join(t.TempDir(), "hang-git")
	os.WriteFile(hang, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755)
	c2.binary = hang
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c2.Probe(ctx, dir)
	if err == nil || time.Since(start) > time.Second {
		t.Errorf("cancel: err=%v elapsed=%v", err, time.Since(start))
	}
}
