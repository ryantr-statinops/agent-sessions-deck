package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// The domain ports the application layer needs are named here as aliases rather
// than as new interfaces. One contract means one implementation: a Stage 03
// store, a Stage 04 resolver, a Stage 05 runtime or a Stage 04 provider registry
// satisfies the domain port directly, and a test fake satisfies it without
// declaring that it is a fake.
type (
	// Provider resolves one agent definition into a launchable command.
	Provider = agent.Provider
	// ProviderRegistry indexes providers by stable agent id.
	ProviderRegistry = agent.Registry
	// WorkspaceResolver turns a workspace candidate into a verified directory.
	WorkspaceResolver = workspace.Resolver
	// SessionStore is the single-writer session metadata port.
	SessionStore = session.Store
	// ChildRuntime owns the child process and is the only component allowed to
	// spawn, signal or probe it. It is the confirmed port, not the narrow one: a
	// recorded kill needs the reap confirmation ForceKill returns, and the single
	// waiter per child needs WaitExit.
	ChildRuntime = session.ChildRuntime
	// InteractiveLease is one client's claim on a session's interactive I/O.
	InteractiveLease = session.InteractiveLease
	// LeaseBroker grants and releases interactive leases, one holder per session
	// in V1.
	LeaseBroker = session.LeaseBroker
)

// TerminalSubscription is the interactive stream claim the owner hands to an
// attached client.
//
// It is deliberately separate from the domain: the domain describes a session
// and an interactive lease, and it never sees a byte stream. The runtime owner
// owns the PTY, and the terminal view owns reading and writing it. The
// application layer acquires the subscription, records who holds it and closes
// it on detach; it never calls Read or Write itself, and it never publishes what
// crosses them.
type TerminalSubscription interface {
	// SessionID is the session the subscription streams.
	SessionID() session.ID
	// Generation is the attempt the subscription is bound to, so a stream cannot
	// outlive its attempt across a restart.
	Generation() session.Generation
	// Holder names the client that owns the stream.
	Holder() string
	// Read and Write carry terminal bytes between the agent's PTY and the
	// client. Only the terminal view calls them.
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
	// Resize reports a new terminal size in cells.
	Resize(width, height int) error
	// Close ends the stream. It must not terminate the session: leaving the
	// interactive view detaches, it never kills the agent.
	Close() error
}

// TerminalSource hands out the stream for one leased attempt. The owner depends
// on the port, never on a PTY implementation.
type TerminalSource interface {
	// Subscribe returns the stream for a granted lease, or a typed failure when
	// the attempt has no usable PTY.
	Subscribe(ctx context.Context, lease InteractiveLease) (TerminalSubscription, error)
}

// MetadataPublisher publishes committed metadata events at an authoritative
// revision. *events.Publisher satisfies it; a test double may capture instead.
type MetadataPublisher interface {
	// PublishRevisioned publishes spec stamped with the revision a committed
	// store mutation produced.
	PublishRevisioned(revision events.Revision, spec events.Spec) (events.Event, error)
}

// Clock is the service's only time source. Injected so that every recorded
// instant, lease timestamp and event timestamp is deterministic under test.
type Clock interface {
	// Now returns the current instant in UTC.
	Now() time.Time
}

// IDMinter mints the stable identity of a new session.
//
// The identity must be stable across restarts and must not encode a version, a
// path or a name, which is why minting is a port and not a string format inside
// the service.
type IDMinter interface {
	// MintID returns a fresh session identity that no live session uses.
	MintID(ctx context.Context) (session.ID, error)
}

// SystemClock reads the host clock. It is the production time source and is
// never swapped out in production code.
type SystemClock struct{}

// Now returns the current instant in UTC.
func (SystemClock) Now() time.Time { return time.Now().UTC() }

// RandomIDMinter mints identities from crypto/rand, so a session id stays stable
// for the life of the session and unique across owners without a counter, a
// hostname or a path leaking into it.
type RandomIDMinter struct {
	// Prefix is prepended to the random suffix. It must satisfy the session-id
	// rules: printable and free of spaces.
	Prefix string
}

// MintID returns a fresh identity from 8 random bytes.
func (m RandomIDMinter) MintID(context.Context) (session.ID, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("mint session id: %w", err)
	}
	return session.ID(m.Prefix + hex.EncodeToString(buf[:])), nil
}

// compile-time proof that the two production helpers are usable ports.
var (
	_ Clock    = SystemClock{}
	_ IDMinter = RandomIDMinter{}
)
