package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePathsDefaultsFromHome(t *testing.T) {
	paths, err := ResolvePaths(EnvFromMap(map[string]string{
		"HOME": "/tmp/asd-home",
	}))
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if paths.ConfigDir != "/tmp/asd-home/.config/asd" {
		t.Fatalf("ConfigDir = %q", paths.ConfigDir)
	}
	if paths.StateDir != "/tmp/asd-home/.local/state/asd" {
		t.Fatalf("StateDir = %q", paths.StateDir)
	}
	if paths.HasRuntimeDir() {
		t.Fatalf("RuntimeDir should stay empty without XDG_RUNTIME_DIR, got %q", paths.RuntimeDir)
	}
	if paths.ConfigFile() != "/tmp/asd-home/.config/asd/config.yaml" {
		t.Fatalf("ConfigFile = %q", paths.ConfigFile())
	}
	if paths.SessionsFile() != "/tmp/asd-home/.local/state/asd/sessions.json" {
		t.Fatalf("SessionsFile = %q", paths.SessionsFile())
	}
	if paths.StateFile() != "/tmp/asd-home/.local/state/asd/state.json" {
		t.Fatalf("StateFile = %q", paths.StateFile())
	}
	if paths.LockPath() != "/tmp/asd-home/.local/state/asd/.lock" {
		t.Fatalf("LockPath = %q", paths.LockPath())
	}
}

func TestResolvePathsXDGOverrides(t *testing.T) {
	paths, err := ResolvePaths(EnvFromMap(map[string]string{
		"HOME":            "/tmp/asd-home",
		"XDG_CONFIG_HOME": "/xdg/config",
		"XDG_STATE_HOME":  "/xdg/state",
		"XDG_RUNTIME_DIR": "/xdg/runtime",
	}))
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if paths.ConfigDir != "/xdg/config/asd" || paths.StateDir != "/xdg/state/asd" || paths.RuntimeDir != "/xdg/runtime/asd" {
		t.Fatalf("paths = %+v", paths)
	}
}

func TestResolvePathsRejectsRelativeAndMissing(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"missing home":     {},
		"relative home":    {"HOME": "tmp/home"},
		"relative config":  {"HOME": "/tmp", "XDG_CONFIG_HOME": "relative"},
		"relative state":   {"HOME": "/tmp", "XDG_STATE_HOME": "relative"},
		"relative runtime": {"HOME": "/tmp", "XDG_RUNTIME_DIR": "relative"},
	} {
		if _, err := ResolvePaths(EnvFromMap(env)); err == nil {
			t.Fatalf("%s: expected error", name)
		}
	}
}

func TestResolvePathsDoesNotWriteUnderRealHome(t *testing.T) {
	fake := t.TempDir()
	paths, err := ResolvePaths(EnvFromMap(map[string]string{"HOME": fake}))
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	for _, candidate := range []string{paths.ConfigDir, paths.StateDir, paths.ConfigFile(), paths.SessionsFile(), paths.StateFile(), paths.LockPath()} {
		if !strings.HasPrefix(candidate, fake+string(filepath.Separator)) {
			t.Fatalf("%q is not under the fake home %q", candidate, fake)
		}
	}
}
