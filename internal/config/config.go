package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"go.yaml.in/yaml/v3"
)

// Config is the user-edited, strict-parsed configuration. Unknown keys,
// duplicate keys/IDs, bad durations and empty executables are refused with
// file/key errors; nothing in here is an environment dump and nothing here
// is persisted as session state (that belongs to state.json).
type Config struct {
	// General holds the dashboard-wide knobs.
	General General `yaml:"general"`
	// Discovery holds PATH-adjacent knobs.
	Discovery Discovery `yaml:"discovery"`
	// Agents are the declared launchable agents, explicitly listed.
	Agents []Agent `yaml:"agents"`
	// Workspaces are the explicitly declared workspaces.
	Workspaces []Workspace `yaml:"workspaces"`
	// Terminal bounds the interactive terminal resources.
	Terminal Terminal `yaml:"terminal"`
}

// General holds the dashboard-wide runtime knobs.
type General struct {
	// RefreshInterval is how often the dashboard re-reads metadata, e.g. "2s".
	RefreshInterval string `yaml:"refresh_interval"`
	// SessionBackend selects the terminal backend; only "pty" is supported.
	SessionBackend string `yaml:"session_backend"`
}

// Discovery configures how Stage 04 probes for agent executables.
type Discovery struct {
	// ExtraPaths are additional directories scanned after PATH.
	ExtraPaths []string `yaml:"extra_paths"`
}

// Agent is one explicitly declared agent command.
type Agent struct {
	// ID is the stable agent identity (internal/agent grammar).
	ID string `yaml:"id"`
	// Name is the user-visible label.
	Name string `yaml:"name"`
	// Executable is the binary path or PATH name; literal, never a shell string.
	Executable string `yaml:"executable"`
	// Args is a literal argv array; it is never shell-split or eval'd.
	Args []string `yaml:"args"`
}

// Workspace is one explicitly declared workspace.
type Workspace struct {
	// Path is the workspace directory; `~` expansion happens at resolve time.
	Path string `yaml:"path"`
}

// Terminal bounds the interactive terminal resources for one session.
type Terminal struct {
	// Scrollback bounds the retained terminal history lines.
	Scrollback int `yaml:"scrollback"`
	// MaxInputQueue bounds queued input bytes before back-pressure.
	MaxInputQueue int `yaml:"max_input_queue"`
}

// Default returns the small documented defaults used when config.yaml is
// absent. Nothing in the default writes secrets or points at a real state
// directory: it is inert until a store opens under it.
func Default() Config {
	return Config{
		General:    General{RefreshInterval: "2s", SessionBackend: "pty"},
		Discovery:  Discovery{ExtraPaths: []string{}},
		Agents:     []Agent{},
		Workspaces: []Workspace{},
		Terminal:   Terminal{Scrollback: 10000, MaxInputQueue: 65536},
	}
}

// RefreshDuration parses the refresh setting.
func (c Config) RefreshDuration() (time.Duration, error) {
	d, err := time.ParseDuration(c.General.RefreshInterval)
	if err != nil {
		return 0, fmt.Errorf("general.refresh_interval %q is not a valid duration (example: \"2s\"): %w", c.General.RefreshInterval, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("general.refresh_interval must be positive, got %q", c.General.RefreshInterval)
	}
	return d, nil
}

// Validate checks the schema semantically: durations, agent IDs, duplicate
// IDs, empty executables, terminal bounds. It names the offending key and a
// fix so a bad config fails before any agent runs.
func (c Config) Validate() error {
	if _, err := c.RefreshDuration(); err != nil {
		return err
	}
	if c.General.SessionBackend != "pty" {
		return fmt.Errorf("general.session_backend must be \"pty\", got %q", c.General.SessionBackend)
	}
	if c.Terminal.Scrollback <= 0 {
		return fmt.Errorf("terminal.scrollback must be positive, got %d", c.Terminal.Scrollback)
	}
	if c.Terminal.MaxInputQueue <= 0 {
		return fmt.Errorf("terminal.max_input_queue must be positive, got %d", c.Terminal.MaxInputQueue)
	}
	seen := map[string]int{}
	for i, a := range c.Agents {
		if err := agent.ID(a.ID).Validate(); err != nil {
			return fmt.Errorf("agents[%d].id: %w", i, err)
		}
		if first, dup := seen[a.ID]; dup {
			return fmt.Errorf("agents[%d].id: duplicate agent id %q (first defined at agents[%d]); use a unique stable id", i, a.ID, first)
		}
		seen[a.ID] = i
		if strings.TrimSpace(a.Name) == "" {
			return fmt.Errorf("agents[%d].name: name is empty; set the user-visible label", i)
		}
		if strings.TrimSpace(a.Executable) == "" {
			return fmt.Errorf("agents[%d].executable: executable is empty; set the agent binary path", i)
		}
		for j, arg := range a.Args {
			if strings.ContainsRune(arg, 0) {
				return fmt.Errorf("agents[%d].args[%d]: argument contains a NUL byte", i, j)
			}
		}
	}
	for i, w := range c.Workspaces {
		if strings.TrimSpace(w.Path) == "" {
			return fmt.Errorf("workspaces[%d].path: path is empty", i)
		}
	}
	return nil
}

// ParseError reports a config file that failed to parse or validate. It
// names the file, the key path and the fix, so the failure message is
// actionable without re-reading the schema docs.
type ParseError struct {
	File string
	Key  string
	Err  error
}

func (e *ParseError) Error() string {
	if e.Key == "" {
		return fmt.Sprintf("%s: %v", e.File, e.Err)
	}
	return fmt.Sprintf("%s: %s: %v", e.File, e.Key, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// Load reads the config file: absent means defaults, present means strict
// parse + validate. A present-but-invalid file never yields defaults: it
// fails so a broken config can never silently run an agent.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Default(), nil
		}
		return Config{}, &ParseError{File: path, Err: err}
	}
	cfg, err := Parse(data)
	if err != nil {
		return Config{}, &ParseError{File: path, Err: err}
	}
	return cfg, nil
}

// Parse decodes one YAML document strictly: unknown fields, duplicate keys,
// extra documents, bad durations, duplicate agent IDs, empty executables are
// all refused. Args stay a literal array — no shell word-splitting.
func Parse(data []byte) (Config, error) {
	if err := rejectDuplicateYAMLKeys(data); err != nil {
		return Config{}, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	cfg := Default()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return Config{}, fmt.Errorf("config must be exactly one YAML document, but contains multiple documents")
	} else if err != io.EOF {
		return Config{}, fmt.Errorf("config has a malformed trailing YAML document: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// rejectDuplicateYAMLKeys walks the YAML node tree and refuses duplicate
// keys at any mapping level, which the decoder itself does not check.
func rejectDuplicateYAMLKeys(data []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}
	return checkDuplicateKeysInNode(&node)
}

func checkDuplicateKeysInNode(node *yaml.Node) error {
	switch node.Kind {
	case yaml.MappingNode:
		seen := map[string]int{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i].Value
			if first, dup := seen[key]; dup {
				return fmt.Errorf("duplicate key %q at line %d (first seen at line %d); keys must be unique", key, node.Content[i].Line, first)
			}
			seen[key] = node.Content[i].Line
			if err := checkDuplicateKeysInNode(node.Content[i+1]); err != nil {
				return err
			}
		}
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, child := range node.Content {
			if err := checkDuplicateKeysInNode(child); err != nil {
				return err
			}
		}
	}
	return nil
}
