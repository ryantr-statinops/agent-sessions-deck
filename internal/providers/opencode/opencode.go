// Package opencode is the OpenCode adapter, with the same bounded-probe
// identity policy as every provider: the marker in version output proves
// identity, the binary name does not.
package opencode

import (
	"context"
	"fmt"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/discovery"
)

// Marker must appear in the bounded `--version` output for a binary to be
// accepted as OpenCode.
const Marker = "opencode"

// DefaultExecutable is the conventional PATH name; it is a lookup hint,
// not an identity.
const DefaultExecutable = "opencode"

// Verify probes path for the OpenCode marker under bounded options.
func Verify(ctx context.Context, p *discovery.Prober, path string) discovery.Result {
	return discovery.RequireMarker(p.Probe(ctx, path), Marker)
}

// Provider resolves OpenCode launches with the literal argv only.
type Provider struct {
	exec string
	args []string
}

// New validates the definition.
func New(executable string, args []string) (*Provider, error) {
	if executable == "" {
		return nil, fmt.Errorf("opencode provider: executable is empty")
	}
	return &Provider{exec: executable, args: append([]string(nil), args...)}, nil
}

// ID returns the stable agent id.
func (p *Provider) ID() agent.ID { return "opencode" }

// Capabilities returns core capabilities only.
func (p *Provider) Capabilities() agent.Capabilities {
	return agent.MustCapabilitiesOf(agent.CapabilityLaunch, agent.CapabilityInteractive)
}

// Resolve freezes the literal command.
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
