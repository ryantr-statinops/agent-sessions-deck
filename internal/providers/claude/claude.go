// Package claude is the Claude Code adapter. It only claims launch and
// interactive capabilities: native vendor features stay unclaimed until an
// adapter plus evidence exists (plan decision 4). Its identity marker is
// verified through a bounded probe — never inferred from the binary name.
package claude

import (
	"context"
	"fmt"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/discovery"
)

// Marker must appear in the bounded `--version` output for a binary to be
// accepted as Claude Code. The binary name alone never proves identity.
const Marker = "claude"

// DefaultExecutable is the conventional PATH name; it is a lookup hint,
// not an identity.
const DefaultExecutable = "claude"

// Verify probes path for the Claude Code marker under bounded options.
func Verify(ctx context.Context, p *discovery.Prober, path string) discovery.Result {
	return discovery.RequireMarker(p.Probe(ctx, path), Marker)
}

// Provider resolves Claude Code launches with the literal argv only; no
// session/resume flags are guessed.
type Provider struct {
	exec string
	args []string
}

// New validates the definition.
func New(executable string, args []string) (*Provider, error) {
	if executable == "" {
		return nil, fmt.Errorf("claude provider: executable is empty")
	}
	return &Provider{exec: executable, args: append([]string(nil), args...)}, nil
}

// ID returns the stable agent id.
func (p *Provider) ID() agent.ID { return "claude" }

// Capabilities returns core capabilities only.
func (p *Provider) Capabilities() agent.Capabilities {
	return agent.MustCapabilitiesOf(agent.CapabilityLaunch, agent.CapabilityInteractive)
}

// Resolve freezes the literal command; the workspace directory is the cwd
// recorded by the caller's launch spec path.
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
