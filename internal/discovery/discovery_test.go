package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func writeExe(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatal(err)
	}
}

// PATH precedence beats extra_paths; symlinked duplicates collapse; missing
// executables and directories never appear.
func TestScanPrecedenceDedupe(t *testing.T) {
	root := t.TempDir()
	p1 := filepath.Join(root, "p1")
	p2 := filepath.Join(root, "p2")
	x := filepath.Join(root, "extra")
	spaced := filepath.Join(root, "dir with spaces")
	for _, d := range []string{p1, p2, x, spaced} {
		os.MkdirAll(d, 0o755)
	}
	writeExe(t, filepath.Join(p2, "agentA"), 0o755)
	os.Symlink(filepath.Join(p1, "agentA"), filepath.Join(p2, "agentA-alias"))
	writeExe(t, filepath.Join(p1, "agentA"), 0o755) // p1 wins for name agentA
	writeExe(t, filepath.Join(p2, "agentA"), 0o755) // p2 canonical differs
	writeExe(t, filepath.Join(x, "agentA"), 0o755)  // extra path loses
	writeExe(t, filepath.Join(spaced, "agentB"), 0o755)
	writeExe(t, filepath.Join(x, "only-extra"), 0o755)
	os.WriteFile(filepath.Join(p1, "notexec"), []byte("#!/bin/sh\n"), 0o644)
	os.Mkdir(filepath.Join(p1, "adir"), 0o755)

	found := Scan(Options{Path: []string{p1, p2}, ExtraPaths: []string{x, spaced}})
	byName := map[string]Binary{}
	for _, b := range found {
		byName[b.Name] = b
	}
	if got := byName["agentA"]; got.Dir != p1 || got.Source != SourcePath {
		t.Errorf("agentA winner = %+v, want p1/path", got)
	}
	if _, ok := byName["agentA-alias"]; ok {
		t.Errorf("symlinked duplicate should be deduped by canonical path")
	}
	if _, ok := byName["notexec"]; ok {
		t.Errorf("non-executable must not be listed")
	}
	if _, ok := byName["adir"]; ok {
		t.Errorf("directory must not be listed")
	}
	if got := byName["only-extra"]; got.Source != SourceExtra {
		t.Errorf("only-extra source = %v, want extra_paths", got.Source)
	}
	if got := byName["agentB"]; got.Dir != spaced {
		t.Errorf("agentB dir = %v, want spaced dir", got.Dir)
	}

	b, ok, err := Find("agentA", Options{Path: []string{p1, p2}, ExtraPaths: []string{x}})
	if err != nil || !ok || b.Dir != p1 {
		t.Errorf("Find agentA = %+v %v %v", b, ok, err)
	}
	if _, ok, _ := Find("missing", Options{Path: []string{p1}}); ok {
		t.Errorf("missing binary must not be found")
	}
}
