package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// ResolveRequest is the immutable input a Provider needs to resolve a launchable
// command. ExtraArgs comes from the CLI's `-- <argv...>` tail and is copied on
// the way in and out, so a provider cannot mutate the caller's slice and a caller
// cannot mutate what the provider already resolved.
type ResolveRequest struct {
	workspaceDir string
	name         string
	extraArgs    []string
}

// NewResolveRequest validates and copies a resolve request.
func NewResolveRequest(workspaceDir, name string, extraArgs []string) (ResolveRequest, error) {
	if strings.ContainsRune(workspaceDir, 0) {
		return ResolveRequest{}, &InvalidRequestError{Problem: "workspace directory contains a NUL byte"}
	}
	if strings.ContainsRune(name, 0) {
		return ResolveRequest{}, &InvalidRequestError{Problem: "session name contains a NUL byte"}
	}
	for i, arg := range extraArgs {
		if strings.ContainsRune(arg, 0) {
			return ResolveRequest{}, &InvalidRequestError{Problem: fmt.Sprintf("extra argument %d contains a NUL byte", i)}
		}
	}
	return ResolveRequest{workspaceDir: workspaceDir, name: name, extraArgs: slices.Clone(extraArgs)}, nil
}

// WorkspaceDir returns the directory the agent will run in.
func (r ResolveRequest) WorkspaceDir() string { return r.workspaceDir }

// Name returns the requested session name, empty when ASD should generate one.
func (r ResolveRequest) Name() string { return r.name }

// ExtraArgs returns a copy of the caller-supplied argument tail.
func (r ResolveRequest) ExtraArgs() []string { return slices.Clone(r.extraArgs) }

// Provider resolves the launchable command for one Agent definition.
//
// A Provider is a pure resolver in this contract: PATH lookup, symlink
// canonicalization, version probes and vendor argument shapes belong to Stage 04
// adapters behind this port, and nothing here spawns a process or inspects a
// terminal.
type Provider interface {
	// ID is the stable agent id this provider is registered under.
	ID() ID
	// Capabilities reports what this agent supports, split into ASD core
	// lifecycle and native vendor capabilities.
	Capabilities() Capabilities
	// Resolve turns the request into the immutable command the runtime freezes
	// into the Attempt. It must validate the result for launch.
	Resolve(ctx context.Context, req ResolveRequest) (Command, error)
}

// Registry indexes providers by stable agent id. Stage 04 implements discovery
// on top of this port; the session layer only needs id lookup.
type Registry interface {
	Lookup(id ID) (Provider, bool)
	IDs() []ID
}

// MapRegistry is an in-memory Registry. It exists so the registry contract is
// testable without discovery; Stage 04 owns the real registry.
type MapRegistry struct {
	providers []Provider
}

// NewMapRegistry builds a registry, rejecting duplicate or invalid ids so a
// configured agent cannot shadow a built-in one silently (Stage 04 acceptance:
// configured overrides must be reported, not silently applied).
func NewMapRegistry(providers ...Provider) (*MapRegistry, error) {
	reg := &MapRegistry{}
	for _, p := range providers {
		if p == nil {
			return nil, &InvalidRequestError{Problem: "provider is nil"}
		}
		if err := p.ID().Validate(); err != nil {
			return nil, err
		}
		if _, exists := reg.Lookup(p.ID()); exists {
			return nil, &InvalidRequestError{Problem: fmt.Sprintf("duplicate provider id %q", string(p.ID()))}
		}
		reg.providers = append(reg.providers, p)
	}
	return reg, nil
}

// Lookup returns the provider registered for id.
func (r *MapRegistry) Lookup(id ID) (Provider, bool) {
	for _, p := range r.providers {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}

// IDs returns the registered agent ids in registration order.
func (r *MapRegistry) IDs() []ID {
	ids := make([]ID, 0, len(r.providers))
	for _, p := range r.providers {
		ids = append(ids, p.ID())
	}
	return ids
}

// Compile-time check that the in-memory registry satisfies the port.
var _ Registry = (*MapRegistry)(nil)

// InvalidRequestError reports a provider resolve request that failed validation.
// The session layer maps it to the INVALID_CONFIGURATION code.
type InvalidRequestError struct {
	Problem string
}

func (e *InvalidRequestError) Error() string { return "invalid provider request: " + e.Problem }
