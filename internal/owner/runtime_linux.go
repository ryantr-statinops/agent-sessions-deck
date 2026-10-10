//go:build linux

package owner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/git"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/process"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/providers/generic"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/store"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/terminal"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// Runtime is the single-writer foreground composition for one state home.
type Runtime struct {
	claim  *ipc.Owner
	app    *app.Service
	server *ipc.Server
}

// InstanceID returns the unique identity claimed before socket binding.
func (r *Runtime) InstanceID() string { return r.claim.InstanceID() }

// Client exposes the in-process application client to the command that bootstrapped this owner.
func (r *Runtime) Client() app.Client { return r.app }

// Close releases an owner that failed before its server entered Serve.
func (r *Runtime) Close() error { return r.claim.Close() }

// Start claims the owner lock before opening its store or serving the socket.
func Start(ctx context.Context, paths config.Paths, cfg config.Config) (*Runtime, error) {
	claim, err := ipc.BootstrapPaths(paths)
	if err != nil {
		if errors.Is(err, ipc.ErrOwnerLocked) {
			return nil, session.WrapError(session.CodeOwnerUnavailable, "owner", "another foreground owner already holds this state home", "use that owner's client socket or stop it before starting another owner", err)
		}
		return nil, err
	}
	service, err := buildService(ctx, paths, cfg, claim.SessionsStore(paths.SessionsFile()), claim.InstanceID(), session.AuthorityLive)
	if err != nil {
		_ = claim.Close()
		return nil, err
	}
	server, err := ipc.NewServer(claim, service)
	if err != nil {
		_ = claim.Close()
		return nil, err
	}
	return &Runtime{claim: claim, app: service, server: server}, nil
}

// Offline builds a stored-authority reader. Its runtime has no adopted children,
// so callers must use it only for read operations.
func Offline(ctx context.Context, paths config.Paths, cfg config.Config) (*app.Service, error) {
	id, err := randomOwnerID()
	if err != nil {
		return nil, err
	}
	st := store.NewSessionsStore(paths.SessionsFile(), paths.LockPath())
	return buildService(ctx, paths, cfg, st, id, session.AuthorityStored)
}

// Serve runs the foreground owner until shutdown and releases its lock last.
func (r *Runtime) Serve(ctx context.Context) error { return r.server.Serve(ctx) }

func buildService(ctx context.Context, paths config.Paths, cfg config.Config, st app.SessionStore, ownerID string, authority session.Authority) (*app.Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var providers []agent.Provider
	if authority == session.AuthorityLive {
		_, discovered, err := probeAgents(ctx, cfg)
		if err != nil {
			return nil, err
		}
		providers = discovered
	} else {
		providers = make([]agent.Provider, 0, len(cfg.Agents))
		for _, definition := range cfg.Agents {
			p, err := generic.New(agent.ID(definition.ID), definition.Name, definition.Executable, definition.Args)
			if err != nil {
				return nil, err
			}
			providers = append(providers, p)
		}
	}
	registry, err := agent.NewMapRegistry(providers...)
	if err != nil {
		return nil, err
	}
	home, _ := os.UserHomeDir()
	gitClient := git.NewClient()
	resolver := workspace.NewPathResolver(home, gitClient.Probe)
	terminalOptions := terminal.DefaultOptions()
	terminalOptions.ScrollbackLines = cfg.Terminal.Scrollback
	terminalOptions.MaxInputBytes = cfg.Terminal.MaxInputQueue
	runtime, err := process.NewRuntime(ownerID, terminalOptions)
	if err != nil {
		return nil, err
	}
	return app.NewService(ctx, app.Deps{
		Registry:   registry,
		Workspaces: resolver,
		Store:      st,
		Runtime:    runtime,
		Leases:     newLeaseBroker(),
		Terminals:  runtime.Terminals(),
		Events:     events.NewPublisher(),
		Clock:      app.SystemClock{},
		IDs:        app.RandomIDMinter{},
	}, app.WithAuthority(authority))
}

func randomOwnerID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

type leaseBroker struct {
	mu      sync.Mutex
	holders map[session.ID]session.InteractiveLease
}

func newLeaseBroker() *leaseBroker {
	return &leaseBroker{holders: make(map[session.ID]session.InteractiveLease)}
}
func (b *leaseBroker) Acquire(ctx context.Context, request session.Lease) (session.InteractiveLease, error) {
	if err := ctx.Err(); err != nil {
		return session.InteractiveLease{}, err
	}
	if err := request.Validate(); err != nil {
		return session.InteractiveLease{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if held, ok := b.holders[request.SessionID]; ok {
		if held.Generation == request.Generation && held.Holder == request.Holder {
			return held, nil
		}
		return session.InteractiveLease{}, session.NewError(session.CodeConflict, string(request.SessionID), "interactive lease is held by "+held.Holder, "detach that client before attaching another")
	}
	lease := session.InteractiveLease{SessionID: request.SessionID, Generation: request.Generation, Holder: request.Holder, AcquiredAt: request.At.UTC()}
	b.holders[request.SessionID] = lease
	return lease, nil
}
func (b *leaseBroker) Release(ctx context.Context, lease session.InteractiveLease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	held, ok := b.holders[lease.SessionID]
	if !ok {
		return nil
	}
	if held.Generation != lease.Generation || held.Holder != lease.Holder || !held.AcquiredAt.Equal(lease.AcquiredAt) {
		return errors.New("interactive lease token does not match the held lease")
	}
	delete(b.holders, lease.SessionID)
	return nil
}

var _ session.LeaseBroker = (*leaseBroker)(nil)
