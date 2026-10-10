package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadAbsentConfigUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "config.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.General.RefreshInterval != "2s" || cfg.Terminal.Scrollback != 10000 || cfg.Terminal.MaxInputQueue != 65536 {
		t.Fatalf("defaults = %+v", cfg)
	}
	if len(cfg.Agents) != 0 || len(cfg.Workspaces) != 0 {
		t.Fatalf("defaults should declare nothing: %+v", cfg)
	}
}

func TestParseValidConfig(t *testing.T) {
	cfg, err := Parse([]byte(`
general:
  refresh_interval: 3s
  session_backend: pty
discovery:
  extra_paths:
    - /opt/agents/bin
agents:
  - id: opencode
    name: OpenCode
    executable: /usr/local/bin/opencode
    args: ["--serve", "--model", "default"]
workspaces:
  - path: ~/Kestrel
terminal:
  scrollback: 5000
  max_input_queue: 4096
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.General.RefreshInterval != "3s" || cfg.Terminal.Scrollback != 5000 {
		t.Fatalf("cfg = %+v", cfg)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].ID != "opencode" || cfg.Agents[0].Args[1] != "--model" {
		t.Fatalf("agents = %+v", cfg.Agents)
	}
	d, err := cfg.RefreshDuration()
	if err != nil || d != 3*time.Second {
		t.Fatalf("RefreshDuration = %v, %v", d, err)
	}
}

func TestParseRejectsUnknownKey(t *testing.T) {
	_, err := Parse([]byte("refresh_interval: 2s\nunknown_key: true\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown_key") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsDuplicateYAMLDocument(t *testing.T) {
	_, err := Parse([]byte("general:\n  refresh_interval: 2s\n---\ngeneral:\n  refresh_interval: 3s\n"))
	if err == nil || !strings.Contains(err.Error(), "multiple documents") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsDuplicateKeys(t *testing.T) {
	_, err := Parse([]byte("general:\n  refresh_interval: 2s\n  refresh_interval: 3s\n"))
	if err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("err = %v", err)
	}
	nested := `
agents:
  - id: a
    id: b
    name: n
    executable: /bin/true
`
	if _, err := Parse([]byte(nested)); err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("nested err = %v", err)
	}
}

func TestParseRejectsInvalidDuration(t *testing.T) {
	_, err := Parse([]byte("general:\n  refresh_interval: soon\n"))
	if err == nil || !strings.Contains(err.Error(), "refresh_interval") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsDuplicateAgentIDs(t *testing.T) {
	_, err := Parse([]byte(`
agents:
  - id: opencode
    name: A
    executable: /bin/a
  - id: opencode
    name: B
    executable: /bin/b
`))
	if err == nil || !strings.Contains(err.Error(), "duplicate agent id") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsEmptyExecutable(t *testing.T) {
	_, err := Parse([]byte(`
agents:
  - id: opencode
    name: A
    executable: ""
`))
	if err == nil || !strings.Contains(err.Error(), "executable is empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRejectsInvalidAgentID(t *testing.T) {
	_, err := Parse([]byte(`
agents:
  - id: "OpenCode!!"
    name: A
    executable: /bin/a
`))
	if err == nil || !strings.Contains(err.Error(), "agents[0].id") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseErrorNamesFileAndKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("terminal:\n  scrollback: -3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error should name the file: %v", err)
	}
}

func TestLoadPresentInvalidConfigFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("general:\n  refresh_interval: nope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("invalid config must not silently fall back to defaults")
	}
}

func TestParseRejectsNonPositiveRefreshInterval(t *testing.T) {
	for _, d := range []string{"0s", "-1s"} {
		_, err := Parse([]byte("general:\n  refresh_interval: " + d + "\n"))
		if err == nil || !strings.Contains(err.Error(), "refresh_interval") {
			t.Fatalf("%s: err = %v", d, err)
		}
	}
}

func TestParseRejectsMalformedTrailingYAMLDocument(t *testing.T) {
	_, err := Parse([]byte("general:\n  refresh_interval: 2s\n---\n: [unclosed\n"))
	if err == nil || !strings.Contains(err.Error(), "malformed trailing") {
		t.Fatalf("err = %v", err)
	}
}
