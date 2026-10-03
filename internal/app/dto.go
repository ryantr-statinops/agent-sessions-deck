package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// Every use case takes a typed request and returns a typed result, so a caller
// in any layer (CLI, TUI, IPC client, daemon) sees the same vocabulary and never
// a bag of loosely typed fields.
//
// Requests are values with an explicit Validate, results are values that carry
// their own authority: a snapshot says whether it was observed by the live owner
// or read back from persisted metadata, and every mutation result carries the
// store revision it produced.

// ScanRequest asks which agent definitions the owner knows about.
type ScanRequest struct {
	// Agents restricts the scan to these agent ids. An empty list scans every
	// registered provider, which is the offline-capable `asd scan` behaviour.
	Agents []agent.ID
}

// AgentSummary is one discovered agent definition.
//
// It reports capabilities and nothing else: a scan never resolves a command,
// never probes a version and never spawns anything, so it stays offline-safe.
type AgentSummary struct {
	// ID is the stable agent id.
	ID agent.ID
	// Capabilities is the provider's declared capability set.
	Capabilities agent.Capabilities
	// Launchable reports whether the provider declares the core launch
	// capability, which is what resolving a command needs.
	Launchable bool
	// Interactive reports whether the provider declares the core interactive
	// capability.
	Interactive bool
	// Native lists the declared native vendor capabilities: list, logs, resume
	// and kill through the vendor's own API. V1 ships no adapter for them.
	Native []agent.Capability
}

// Core returns the declared ASD core lifecycle capabilities.
func (a AgentSummary) Core() []agent.Capability {
	return a.Capabilities.Core().List()
}

// ScanResult is the outcome of a scan.
type ScanResult struct {
	// Agents is every scanned definition, ordered by agent id.
	Agents []AgentSummary
	// Authority is the authority of the observation.
	Authority session.Authority
	// ObservedAt is when the scan ran, UTC.
	ObservedAt time.Time
	// Revision is the store revision the owner was working at.
	Revision uint64
}

// ListRequest filters the session list. Filters combine, and none of them
// infers state that was not observed: an activity filter would need provider
// evidence, which is why there is none.
type ListRequest struct {
	// Lifecycles keeps only these lifecycle values. Empty keeps every lifecycle.
	Lifecycles []session.Lifecycle
	// AgentID keeps only sessions of one agent definition. Empty keeps all.
	AgentID agent.ID
	// Workspace keeps only sessions in one workspace. Empty keeps all.
	Workspace workspace.ID
}

// Validate checks the filters before the service reads any state.
func (r ListRequest) Validate() error {
	for _, l := range r.Lifecycles {
		if !l.Valid() {
			return session.NewError(session.CodeInvalidConfiguration, string(l),
				"lifecycle filter "+string(l)+" is not a defined value",
				"use one of "+lifecycleFilterNames())
		}
	}
	if r.AgentID != "" {
		if err := r.AgentID.Validate(); err != nil {
			return session.WrapError(session.CodeInvalidConfiguration, string(r.AgentID),
				"agent filter is not a valid agent id", "list the agents to see valid ids", err)
		}
	}
	if r.Workspace != "" {
		if err := r.Workspace.Validate(); err != nil {
			return session.WrapError(session.CodeInvalidConfiguration, r.Workspace.Path(),
				"workspace filter is not a valid workspace identity",
				"pass a canonical absolute directory", err)
		}
	}
	return nil
}

func lifecycleFilterNames() string {
	names := make([]string, 0, len(session.AllLifecycles()))
	for _, l := range session.AllLifecycles() {
		names = append(names, l.String())
	}
	return strings.Join(names, ", ")
}

// ListResult is the outcome of a list. The snapshot keeps the authority, the
// store revision and the observation instant of every row, so an offline reader
// can tell a stored running row from a live one.
type ListResult struct {
	// Snapshot holds the filtered rows in session-id order.
	Snapshot session.Snapshot
}

// GetRequest asks for one session.
type GetRequest struct {
	// Ref is a full session id or an unambiguous id prefix. An ambiguous prefix
	// is refused with CONFLICT and the candidates rather than guessed.
	Ref string
}

// GetResult carries both readings of one session.
type GetResult struct {
	// Session is the row at the owner's authority: live when the owner holds the
	// session, stored otherwise.
	Session session.SessionSnapshot
	// Stored is the row read back from the store, so it is the metadata that
	// really exists. It never claims liveness, however it was recorded, and it is
	// the reading an offline reader shows.
	Stored session.SessionSnapshot
	// Persisted reports whether the store still holds metadata for this session.
	// It is false exactly when the owner's state is ahead of the store - after a
	// failed write - which is the only way the two readings can disagree, and the
	// reason a caller must not treat Session as if it were persisted.
	Persisted bool
	// Revision is the store revision the row was read at.
	Revision uint64
	// ObservedAt is when the row was observed, UTC.
	ObservedAt time.Time
}

// CreateRequest launches a new session.
type CreateRequest struct {
	// Agent is the agent definition to launch.
	Agent agent.ID
	// WorkspacePath is the workspace candidate, exactly as the user wrote it.
	// Tilde expansion and relative-path resolution belong to the resolver.
	WorkspacePath string
	// WorkspaceSource records where the candidate came from. An empty value means
	// SourceExplicit, because a path the user typed is the strongest evidence.
	WorkspaceSource workspace.Source
	// Name is the user-visible label. An empty value asks the service to derive
	// one from the agent and the workspace.
	Name string
	// ExtraArgs is the `-- <argv...>` tail. It is appended to the provider's
	// resolved command as literal arguments: never re-split, never globbed and
	// never expanded.
	ExtraArgs []string
}

// Validate checks the request before any port is called.
func (r CreateRequest) Validate() error {
	if err := r.Agent.Validate(); err != nil {
		return session.WrapError(session.CodeInvalidConfiguration, string(r.Agent),
			"the agent id is not valid", "run a scan to see valid agent ids", err)
	}
	if strings.TrimSpace(r.WorkspacePath) == "" {
		return session.NewError(session.CodeInvalidConfiguration, r.WorkspacePath,
			"no workspace path was given", "pass a directory to run the agent in")
	}
	if r.WorkspaceSource != "" && !r.WorkspaceSource.Valid() {
		return session.NewError(session.CodeInvalidConfiguration, r.WorkspacePath,
			"workspace source "+string(r.WorkspaceSource)+" is not a defined value",
			"use one of "+sourceNames())
	}
	return nil
}

func (r CreateRequest) source() workspace.Source {
	if r.WorkspaceSource == "" {
		return workspace.SourceExplicit
	}
	return r.WorkspaceSource
}

func sourceNames() string {
	names := make([]string, 0, len(workspace.AllSources()))
	for _, s := range workspace.AllSources() {
		names = append(names, s.String())
	}
	return strings.Join(names, ", ")
}

// CreateResult is the outcome of a launch.
type CreateResult struct {
	// Session is the created session's row.
	Session session.SessionSnapshot
	// Revision is the store revision the creation committed at.
	Revision uint64
	// Event is the metadata event published for the committed creation.
	Event events.Event
}

// OpenRequest attaches a client to a running session.
type OpenRequest struct {
	// Ref is a full session id or an unambiguous id prefix.
	Ref string
	// Holder names the attaching client. It is recorded in the lease, so a
	// second attach can name who holds the session.
	Holder string
}

// Validate checks the request.
func (r OpenRequest) Validate() error {
	if err := validateRef(r.Ref); err != nil {
		return err
	}
	if r.Holder == "" {
		return session.NewError(session.CodeInvalidConfiguration, r.Ref,
			"no client name was given", "attach as a named client so a second attach can report the holder")
	}
	return nil
}

// OpenResult is the outcome of an attach.
type OpenResult struct {
	// Session is the row after attaching.
	Session session.SessionSnapshot
	// Lease is the granted interactive lease.
	Lease InteractiveLease
	// Terminal is the stream claim for the attached client. The client drives
	// it; the application layer never reads or writes it.
	Terminal TerminalSubscription
	// Revision is the store revision the attachment committed at.
	Revision uint64
	// Event is the metadata event published for the committed attachment.
	Event events.Event
}

// DetachRequest releases an interactive lease.
type DetachRequest struct {
	// Ref is a full session id or an unambiguous id prefix.
	Ref string
}

// Validate checks the request.
func (r DetachRequest) Validate() error { return validateRef(r.Ref) }

// DetachResult is the outcome of a detach. Detaching changes the attachment axis
// only: it never touches the lifecycle, so leaving the view cannot terminate the
// agent.
type DetachResult struct {
	// Session is the row after detaching.
	Session session.SessionSnapshot
	// Revision is the store revision the detachment committed at.
	Revision uint64
	// Event is the metadata event published for the committed detachment.
	Event events.Event
}

// RenameRequest edits a session's user-visible label.
type RenameRequest struct {
	// Ref is a full session id or an unambiguous id prefix.
	Ref string
	// Name is the new label.
	Name string
}

// Validate checks the request.
func (r RenameRequest) Validate() error {
	if err := validateRef(r.Ref); err != nil {
		return err
	}
	if r.Name == "" {
		return session.NewError(session.CodeInvalidConfiguration, r.Ref,
			"the new name is empty", "pass a name for the session")
	}
	return nil
}

// RenameResult is the outcome of a rename.
type RenameResult struct {
	// Session is the row after renaming.
	Session session.SessionSnapshot
	// Revision is the store revision the rename committed at.
	Revision uint64
	// Event is the metadata event published for the committed rename.
	Event events.Event
}

// RestartRequest starts a new attempt of an existing session.
type RestartRequest struct {
	// Ref is a full session id or an unambiguous id prefix.
	Ref string
	// Force confirms restarting a live attempt. Without it a live attempt is
	// refused, because restarting one interrupts work in progress. Forcing does
	// not skip identity verification.
	Force bool
}

// Validate checks the request.
func (r RestartRequest) Validate() error { return validateRef(r.Ref) }

// RestartResult is the outcome of a restart.
type RestartResult struct {
	// Session is the row after restarting.
	Session session.SessionSnapshot
	// PreviousGeneration is the attempt generation the restart replaced. The new
	// attempt is PreviousGeneration+1 and the session id is unchanged.
	PreviousGeneration session.Generation
	// Revision is the store revision the restart committed at.
	Revision uint64
	// Event is the metadata event published for the committed restart.
	Event events.Event
}

// StopRequest asks for a graceful stop.
type StopRequest struct {
	// Ref is a full session id or an unambiguous id prefix.
	Ref string
	// Grace is how long to wait for the child to be reaped. Zero uses the
	// owner's configured window.
	Grace time.Duration
}

// Validate checks the request.
func (r StopRequest) Validate() error { return validateRef(r.Ref) }

// StopResult is the outcome of a graceful stop.
type StopResult struct {
	// Stopped reports whether the child was reaped inside the grace window.
	Stopped bool
	// TimedOut reports the T9 outcome: the child was still alive when the window
	// closed. The session is still running, and the error carries the kill
	// guidance, because escalation to SIGKILL is an explicit separate action.
	TimedOut bool
	// Session is the row after the stop.
	Session session.SessionSnapshot
	// Revision is the store revision the stop committed at.
	Revision uint64
	// Event is the metadata event published for the committed stop.
	Event events.Event
}

// KillRequest asks for an explicit forced termination.
type KillRequest struct {
	// Ref is a full session id or an unambiguous id prefix.
	Ref string
	// Signal is the signal to deliver to the verified owned process group. Only
	// session.SignalKill is a kill; an empty value means the same thing. A
	// graceful termination is the separate Stop use case, because the record has
	// to say which of the two happened.
	Signal session.SignalKind
}

// Validate checks the request.
func (r KillRequest) Validate() error {
	if err := validateRef(r.Ref); err != nil {
		return err
	}
	switch r.Signal {
	case "", session.SignalKill:
		return nil
	case session.SignalTerm:
		return session.NewError(session.CodeInvalidConfiguration, r.Ref,
			"a kill delivers SIGKILL; SIGTERM is a graceful stop, which is a different operation and is recorded differently",
			"stop the session gracefully, or kill it with the default SIGKILL")
	default:
		return session.NewError(session.CodeInvalidConfiguration, r.Ref,
			"signal "+string(r.Signal)+" is not one this owner delivers",
			"use SIGKILL to kill a session, or SIGTERM through a graceful stop")
	}
}

// KillResult is the outcome of an explicit kill.
type KillResult struct {
	// Session is the row after the kill.
	Session session.SessionSnapshot
	// Signal is the signal that was delivered to the verified owned group.
	Signal session.SignalKind
	// Revision is the store revision the kill committed at.
	Revision uint64
	// Event is the metadata event published for the committed kill.
	Event events.Event
}

// DeleteRequest removes one session's metadata.
type DeleteRequest struct {
	// Ref is a full session id or an unambiguous id prefix.
	Ref string
}

// Validate checks the request.
func (r DeleteRequest) Validate() error { return validateRef(r.Ref) }

// DeleteResult is the outcome of a metadata deletion.
type DeleteResult struct {
	// ID is the session whose metadata was removed.
	ID session.ID
	// Revision is the store revision the deletion committed at.
	Revision uint64
	// Event is the metadata event published for the committed deletion.
	Event events.Event
}

// ReportKind names an asynchronous observation the runtime owner hands back to
// the application layer.
type ReportKind string

// The reportable observations. Each one maps to a reducer row, and each carries
// only the evidence that row needs.
const (
	// ReportChildExited reports a reaped child with its terminal reason.
	ReportChildExited ReportKind = "child-exited"
	// ReportPTYLost reports that the managed PTY went away, together with the
	// liveness reading that distinguishes an orphan from a dead process.
	ReportPTYLost ReportKind = "pty-lost"
	// ReportProbeInconclusive reports a probe that could not decide.
	ReportProbeInconclusive ReportKind = "probe-inconclusive"
	// ReportReconciled reports positive or negative liveness evidence for a
	// stored running attempt.
	ReportReconciled ReportKind = "reconciled"
)

// AllReportKinds returns every reportable observation.
func AllReportKinds() []ReportKind {
	return []ReportKind{ReportChildExited, ReportPTYLost, ReportProbeInconclusive, ReportReconciled}
}

// Validate reports whether the kind is one of the defined observations.
func (k ReportKind) Validate() bool {
	return slices.Contains(AllReportKinds(), k)
}

func (k ReportKind) String() string { return string(k) }

// ReportRequest is one asynchronous observation from the runtime owner.
//
// It is not a user-facing request: the CLI and the TUI never report liveness,
// only the process owner does. The observation names the attempt it belongs to,
// because a callback that arrives after a restart still refers to the attempt
// that created it, and the reducer fences it out instead of letting it overwrite
// the attempt that replaced it.
type ReportRequest struct {
	// Ref is a full session id or an unambiguous id prefix.
	Ref string
	// Attempt is the attempt generation the observation was made for. Zero means
	// the session's current attempt.
	Attempt session.Generation
	// Kind selects which observation this is.
	Kind ReportKind
	// At is the observation instant. Zero uses the owner's clock.
	At time.Time
	// Reason is the terminal cause, required by ReportChildExited.
	Reason session.Reason
	// Liveness is the liveness reading, required by ReportPTYLost,
	// ReportProbeInconclusive and ReportReconciled.
	Liveness session.LivenessObservation
}

// Validate checks the request against the payload its kind requires.
func (r ReportRequest) Validate() error {
	if err := validateRef(r.Ref); err != nil {
		return err
	}
	if !r.Kind.Validate() {
		return session.NewError(session.CodeInvalidConfiguration, r.Ref,
			"report kind "+string(r.Kind)+" is not a defined observation", "report a child exit, a lost pty, an inconclusive probe or a reconciliation")
	}
	switch r.Kind {
	case ReportChildExited:
		if err := r.Reason.Validate(); err != nil || !r.Reason.Kind.Terminal() {
			return session.NewError(session.CodeInvalidConfiguration, r.Ref,
				"a child exit needs a terminal reason such as a natural exit, a stop or a kill",
				"report the reason the process actually ended with")
		}
	default:
		if err := r.Liveness.Validate(); err != nil {
			return session.WrapError(session.CodeInvalidConfiguration, r.Ref,
				"the liveness reading is incomplete", "report an outcome, a timestamp and the observed identity or a probe detail", err)
		}
	}
	return nil
}

func (r ReportRequest) at(clock Clock) time.Time {
	if !r.At.IsZero() {
		return r.At.UTC()
	}
	return clock.Now()
}

// ReportResult is the outcome of a reported observation.
type ReportResult struct {
	// Session is the row after the observation.
	Session session.SessionSnapshot
	// Row is the contract row that was applied.
	Row string
	// Stale reports that the observation belonged to an older attempt and was
	// dropped without touching the current attempt. That is the designed outcome
	// of a late callback, not a failure, so it is reported rather than raised.
	Stale bool
	// Revision is the store revision the observation committed at, zero when
	// nothing was committed.
	Revision uint64
	// Event is the metadata event published for the committed observation.
	Event events.Event
}

// Client is the application boundary shared by every caller.
//
// The CLI, the TUI and a future daemon depend on this interface rather than on a
// concrete service, so a caller cannot reach past the use cases into the ports.
type Client interface {
	// Scan lists the known agent definitions and their capabilities.
	Scan(ctx context.Context, req ScanRequest) (ScanResult, error)
	// List returns the session rows the owner knows about, with their authority.
	List(ctx context.Context, req ListRequest) (ListResult, error)
	// Get returns one session's live and stored readings.
	Get(ctx context.Context, req GetRequest) (GetResult, error)
	// Create resolves a workspace and a command, launches one attempt and
	// persists the result.
	Create(ctx context.Context, req CreateRequest) (CreateResult, error)
	// Open attaches one client to a session through a single interactive lease.
	Open(ctx context.Context, req OpenRequest) (OpenResult, error)
	// Detach releases an interactive lease and changes only the attachment.
	Detach(ctx context.Context, req DetachRequest) (DetachResult, error)
	// Rename edits a session's user-visible label.
	Rename(ctx context.Context, req RenameRequest) (RenameResult, error)
	// Restart opens a new attempt on the same session, incrementing the attempt
	// generation and keeping the session id.
	Restart(ctx context.Context, req RestartRequest) (RestartResult, error)
	// Stop asks for a graceful stop and never escalates on its own.
	Stop(ctx context.Context, req StopRequest) (StopResult, error)
	// Kill terminates through an explicit signal to the verified owned group.
	Kill(ctx context.Context, req KillRequest) (KillResult, error)
	// Delete removes a finished session's metadata and refuses while it is active.
	Delete(ctx context.Context, req DeleteRequest) (DeleteResult, error)
}

func validateRef(ref string) error {
	if ref == "" {
		return session.NewError(session.CodeInvalidConfiguration, ref,
			"no session id was given", "list the sessions to see valid ids")
	}
	return nil
}
