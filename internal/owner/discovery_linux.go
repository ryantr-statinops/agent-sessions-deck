//go:build linux

package owner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/discovery"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/providers/claude"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/providers/codex"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/providers/generic"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/providers/opencode"
)

// AgentProbe is the bounded discovery result exposed by asd scan.
type AgentProbe struct {
	ID     agent.ID         `json:"id"`
	Status discovery.Status `json:"status"`
	Path   string           `json:"path,omitempty"`
	Source string           `json:"source,omitempty"`
	Reason string           `json:"reason,omitempty"`
}

type builtinFactory struct {
	id     agent.ID
	name   string
	make   func(string) (agent.Provider, error)
	verify func(context.Context, *discovery.Prober, string) discovery.Result
}

func builtins() []builtinFactory {
	return []builtinFactory{
		{id: "claude", name: claude.DefaultExecutable, make: func(path string) (agent.Provider, error) { return claude.New(path, nil) }, verify: func(ctx context.Context, p *discovery.Prober, path string) discovery.Result {
			return claude.Verify(ctx, p, path)
		}},
		{id: "codex", name: codex.DefaultExecutable, make: func(path string) (agent.Provider, error) { return codex.New(path, nil) }, verify: func(ctx context.Context, p *discovery.Prober, path string) discovery.Result {
			return codex.Verify(ctx, p, path)
		}},
		{id: "opencode", name: opencode.DefaultExecutable, make: func(path string) (agent.Provider, error) { return opencode.New(path, nil) }, verify: func(ctx context.Context, p *discovery.Prober, path string) discovery.Result {
			return opencode.Verify(ctx, p, path)
		}},
	}
}

func probeAgents(ctx context.Context, cfg config.Config) ([]AgentProbe, []agent.Provider, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	home, _ := os.UserHomeDir()
	search := discovery.Options{Path: discovery.SplitPathList(os.Getenv("PATH")), ExtraPaths: cfg.Discovery.ExtraPaths, Home: home}
	prober := discovery.NewProber(discovery.ProbeOptions{})
	factories := builtins()
	allBuiltin := make([]agent.Provider, 0, len(factories))
	for _, factory := range factories {
		p, err := factory.make(factory.name)
		if err != nil {
			return nil, nil, err
		}
		allBuiltin = append(allBuiltin, p)
	}
	builtinRegistry, err := agent.NewMapRegistry(allBuiltin...)
	if err != nil {
		return nil, nil, err
	}
	if err := discovery.ValidateConfiguredAgents(cfg.Agents, builtinRegistry); err != nil {
		return nil, nil, err
	}
	probes := make([]AgentProbe, 0, len(factories)+len(cfg.Agents))
	providers := make([]agent.Provider, 0, len(factories)+len(cfg.Agents))
	for _, factory := range factories {
		binary, found, findErr := discovery.Find(factory.name, search)
		if findErr != nil {
			return nil, nil, findErr
		}
		if !found {
			probes = append(probes, AgentProbe{ID: factory.id, Status: discovery.StatusNotFound, Reason: "executable was not found on PATH or configured extra_paths"})
			continue
		}
		result := factory.verify(ctx, prober, binary.Canonical)
		probes = append(probes, AgentProbe{ID: factory.id, Status: result.Status, Path: binary.Canonical, Source: string(binary.Source), Reason: result.Reason})
		if result.Status == discovery.StatusAvailable {
			p, err := factory.make(binary.Canonical)
			if err != nil {
				return nil, nil, err
			}
			providers = append(providers, p)
		}
	}
	for _, definition := range cfg.Agents {
		path := definition.Executable
		source := "config"
		if !filepath.IsAbs(path) && !strings.ContainsRune(path, filepath.Separator) {
			binary, found, findErr := discovery.Find(path, search)
			if findErr != nil {
				return nil, nil, findErr
			}
			if !found {
				probes = append(probes, AgentProbe{ID: agent.ID(definition.ID), Status: discovery.StatusNotFound, Source: source, Reason: "configured executable was not found on PATH or configured extra_paths"})
				p, err := generic.New(agent.ID(definition.ID), definition.Name, definition.Executable, definition.Args)
				if err != nil {
					return nil, nil, err
				}
				providers = append(providers, p)
				continue
			}
			path, source = binary.Canonical, string(binary.Source)
		} else if !filepath.IsAbs(path) {
			absolute, err := filepath.Abs(path)
			if err == nil {
				path = absolute
			}
		}
		result := prober.Probe(ctx, path)
		probes = append(probes, AgentProbe{ID: agent.ID(definition.ID), Status: result.Status, Path: path, Source: source, Reason: result.Reason})
		p, err := generic.New(agent.ID(definition.ID), definition.Name, definition.Executable, definition.Args)
		if err != nil {
			return nil, nil, err
		}
		providers = append(providers, p)
	}
	sort.Slice(probes, func(i, j int) bool { return probes[i].ID < probes[j].ID })
	return probes, providers, nil
}

// ProbeAgents discovers built-in executables and configured commands with the
// Stage 04 bounded version-probe policy; it never starts an interactive child.
func ProbeAgents(ctx context.Context, cfg config.Config) ([]AgentProbe, error) {
	probes, _, err := probeAgents(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("scan agents: %w", err)
	}
	return probes, nil
}
