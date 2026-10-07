// Package discovery locates agent executables on the filesystem. It never
// scans beyond the user's PATH and configured extra paths, never follows the
// filesystem recursively, and never trusts a binary name as identity: a
// candidate is only "available" after a bounded probe says so (see probe.go).
package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Source records whether a candidate came from PATH or from the user's
// configured extra_paths. PATH always wins over extra_paths for the same name.
type Source string

const (
	SourcePath  Source = "path"
	SourceExtra Source = "extra_paths"
)

// Binary is one executable candidate on a search directory.
type Binary struct {
	// Name is the file base name as found.
	Name string
	// Dir is the search directory that yielded it (cleaned, as given).
	Dir string
	// Path joins Dir and Name.
	Path string
	// Canonical is the symlink-resolved absolute path used for dedupe and
	// identity; it may equal Path when there is no symlink.
	Canonical string
	// Source records the winning search list.
	Source Source
}

// Options bounds the search. Empty Path means "use the process PATH".
type Options struct {
	Path       []string
	ExtraPaths []string
	// Home expands a leading "~" in ExtraPaths; empty disables it.
	Home string
}

// SplitPathList splits a PATH-style list using the OS separator; it is the
// only place a PATH string is parsed so the precedence rule stays one place.
func SplitPathList(list string) []string {
	if list == "" {
		return nil
	}
	return dedup(filterEmpty(filepath.SplitList(list)))
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

func filterEmpty(in []string) []string {
	out := in[:0]
	for _, v := range in {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Executable reports whether path names a regular, executable file.
func Executable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return info.Mode().Perm()&0o111 != 0
}

// Find locates the first executable named name across PATH then ExtraPaths.
// The winner is the first (highest-precedence) search directory; later
// entries with the same canonical target are dedupes, not overrides.
func Find(name string, opts Options) (Binary, bool, error) {
	if strings.ContainsRune(name, 0) {
		return Binary{}, false, fmt.Errorf("binary name contains a NUL byte")
	}
	for _, b := range Scan(opts) {
		if b.Name == name {
			return b, true, nil
		}
	}
	return Binary{}, false, nil
}

// Scan walks every search directory non-recursively and returns the
// executables found, deduplicated two ways: the first directory in
// precedence order wins a name, and a canonical file reachable through
// different symlinked paths appears once. Non-executable and directory
// entries are skipped so a later full filesystem scan is never needed.
func Scan(opts Options) []Binary {
	byName := map[string]Binary{}
	byCanonical := map[string]string{}
	var order []string
	seenDir := map[string]bool{}

	add := func(dir string, src Source) {
		if dir == "" {
			return
		}
		if strings.HasPrefix(dir, "~") && opts.Home != "" {
			dir = filepath.Join(opts.Home, strings.TrimPrefix(dir, "~"))
		}
		dir = filepath.Clean(dir)
		if seenDir[dir] {
			return
		}
		seenDir[dir] = true
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			full := filepath.Join(dir, e.Name())
			if !Executable(full) {
				continue
			}
			canon, err := filepath.EvalSymlinks(full)
			if err != nil {
				canon = full
			}
			if _, dup := byCanonical[canon]; dup {
				continue
			}
			if _, dup := byName[e.Name()]; dup {
				continue
			}
			b := Binary{Name: e.Name(), Dir: dir, Path: full, Canonical: canon, Source: src}
			byName[e.Name()] = b
			byCanonical[canon] = e.Name()
			order = append(order, e.Name())
		}
	}

	for _, d := range opts.Path {
		add(d, SourcePath)
	}
	for _, d := range opts.ExtraPaths {
		add(d, SourceExtra)
	}
	out := make([]Binary, 0, len(order))
	for _, n := range order {
		out = append(out, byName[n])
	}
	return out
}
