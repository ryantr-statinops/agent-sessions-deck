// Package config resolves the XDG layout and parses the user configuration
// for Agent Session Deck. It never spawns a process, never persists sessions
// and never falls back to implementation outside its files: the store owns
// durability, the runtime owns processes.
package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Paths is the resolved XDG layout for one user. Every file a Stage 03
// component reads or writes hangs off exactly one of these directories, so
// nothing writes outside the XDG split ADR 0003 froze.
type Paths struct {
	// ConfigDir holds the user-edited config.yaml.
	ConfigDir string
	// StateDir holds sessions.json, state.json and the owner lock.
	StateDir string
	// RuntimeDir holds the control socket, or is empty when XDG_RUNTIME_DIR
	// is unset; the offline fallback path is a Stage 06 decision (ADR 0003).
	RuntimeDir string
}

// ConfigFile is the strict-parsed user configuration.
func (p Paths) ConfigFile() string { return filepath.Join(p.ConfigDir, "config.yaml") }

// SessionsFile is the versioned session snapshot store.
func (p Paths) SessionsFile() string { return filepath.Join(p.StateDir, "sessions.json") }

// StateFile is the versioned recent-workspace store.
func (p Paths) StateFile() string { return filepath.Join(p.StateDir, "state.json") }

// LockPath is the state-home lock file the owner holds for its lifetime.
func (p Paths) LockPath() string { return filepath.Join(p.StateDir, ".lock") }

// HasRuntimeDir reports whether a private runtime directory resolved.
func (p Paths) HasRuntimeDir() bool { return p.RuntimeDir != "" }

// ResolvePaths resolves the XDG layout from getenv. Tests pass a fake getenv
// and never mutate the shell's HOME: one variable override never rewrites the
// working shell's real home (stage checklist constraint).
//
// XDG_CONFIG_HOME, XDG_STATE_HOME and XDG_RUNTIME_DIR must be absolute when
// set; an empty or relative value fails loudly instead of writing into an
// unexpected directory. When unset they fall back to the ADR 0003 defaults.
func ResolvePaths(getenv func(string) string) (Paths, error) {
	home := strings.TrimSpace(getenv("HOME"))
	if home == "" {
		return Paths{}, fmt.Errorf("HOME is not set: cannot resolve XDG directories")
	}
	if !filepath.IsAbs(home) {
		return Paths{}, fmt.Errorf("HOME %q is not an absolute path", home)
	}
	configHome, err := xdgDir(getenv, "XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err != nil {
		return Paths{}, err
	}
	stateHome, err := xdgDir(getenv, "XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	if err != nil {
		return Paths{}, err
	}
	paths := Paths{
		ConfigDir: filepath.Join(configHome, "asd"),
		StateDir:  filepath.Join(stateHome, "asd"),
	}
	if runtimeDir, ok := lookupXDG(getenv, "XDG_RUNTIME_DIR"); ok {
		if !filepath.IsAbs(runtimeDir) {
			return Paths{}, fmt.Errorf("XDG_RUNTIME_DIR must be absolute, got %q", runtimeDir)
		}
		paths.RuntimeDir = filepath.Join(runtimeDir, "asd")
	}
	return paths, nil
}

// lookupXDG reports an explicitly set XDG directory and validates it.
func lookupXDG(getenv func(string) string, key string) (string, bool) {
	value := strings.TrimSpace(getenv(key))
	if value == "" {
		return "", false
	}
	return value, true
}

// xdgDir resolves one XDG directory, applying the default when unset and
// refusing relative values.
func xdgDir(getenv func(string) string, key, fallback string) (string, error) {
	value, ok := lookupXDG(getenv, key)
	if !ok {
		return fallback, nil
	}
	if !filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be absolute, got %q", key, value)
	}
	return value, nil
}

// EnvFromMap adapts a lookup map into the getenv shape ResolvePaths takes,
// so tests build an isolated environment without touching process env.
func EnvFromMap(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
