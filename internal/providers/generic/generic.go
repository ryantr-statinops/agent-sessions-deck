// Package generic is the fallback provider: it requires an explicit
// executable plus literal argv and runs it in the selected workspace. It
// never inspects the binary, never guesses vendor flags, and never runs a
// shell: every argument reaches the process byte-for-byte.
package generic

import (
	"context"
	"fmt"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
)

// Provider launches an arbitrary executable with literal arguments.
type Provider struct {
	id    agent.ID
	name  string
	exec  string
	args  []string
	caps  agent.Capabilities
}

// New validates the definition and copies the argv.
func New(id agent.ID, name, executable string, args []string) (*Provider, error) {
	if err := id.Validate(); err != nil {
		return nil, err
	}
	if executable == "" {
		return nil, fmt.Errorf("generic provider %q: executable is empty", id)
	}
	if name == "" {
		name = string(id)
	}
	caps, err := agent.CapabilitiesOf(agent.CapabilityLaunch, agent.CapabilityInteractive)
	if err != nil {
		return nil, err
	}
	argv := append([]string(nil), args...)
	for i, a := range argv {
		if a == "" {
			return nil, fmt.Errorf("generic provider %q: argument %d is empty", id, i)
		}
	}
	return &Provider{id: id, name: name, exec: executable, args: argv, caps: caps}, nil
}

// ID returns the stable provider id.
func (p *Provider) ID() agent.ID { return p.id }

// Capabilities reports core launch/interactive only; native vendor features
// are always off for a generic command.
func (p *Provider) Capabilities() agent.Capabilities { return p.caps }

// Resolve returns the literal command: configured argv first, then the
// caller's `-- <argv...>` tail appended verbatim. Nothing is shell-split,
// globbed or word-expanded; a literal metacharacter is inert.
func (p *Provider) Resolve(_ context.Context, req agent.ResolveRequest) (agent.Command, error) {
	argv := append([]string(nil), p.args...)
	argv = append(argv, req.ExtraArgs()...)
	cmd, err := agent.NewCommand(p.exec, argv)
	if err != nil {
		return agent.Command{}, err
	}
	if err := cmd.ValidateForLaunch(); err != nil {
		return agent.Command{}, err
	}
	return cmd, nil
}

// LaunchSpec pairs the frozen command with the workspace the runtime must
// use as its cwd. The workspace is authoritative: ASD never substitutes a
// repo root for the directory the user selected.
func (p *Provider) LaunchSpec(req agent.ResolveRequest) (LaunchSpec, error) {
	cmd, err := p.Resolve(context.Background(), req)
	if err != nil {
		return LaunchSpec{}, err
	}
	return LaunchSpec{Command: cmd, WorkDir: req.WorkspaceDir()}, nil
}

// LaunchSpec is the frozen, launch-ready form of a resolved agent.
type LaunchSpec struct {
	Command agent.Command
	WorkDir string
}
