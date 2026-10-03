package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// defaultStopGrace is the window a graceful stop waits for the child to be
// reaped before the owner reports a stop timeout and leaves the session running.
const defaultStopGrace = 5 * time.Second

// Deps are the ports a Service needs. Every dependency is injected, which is why
// the application layer owns no backend of its own.
type Deps struct {
	// Registry resolves agent definitions into launchable commands.
	Registry ProviderRegistry
	// Workspaces turns a workspace candidate into a verified directory.
	Workspaces WorkspaceResolver
	// Store persists session metadata as the single writer.
	Store SessionStore
	// Runtime owns the child process. It is the confirmed process port, not the
	// narrow one: a kill may only be recorded from a confirmed reap, so the owner
	// needs ForceKill, and ADR 0003's single waiter per child needs WaitExit.
	Runtime ChildRuntime
	// Leases grants the single interactive lease per session.
	Leases LeaseBroker
	// Terminals hands out the interactive stream for a leased attempt.
	Terminals TerminalSource
	// Events publishes committed metadata events.
	Events MetadataPublisher
	// Clock is the service's only time source.
	Clock Clock
	// IDs mints stable session identities.
	IDs IDMinter
}

// validate refuses an incomplete dependency set rather than letting a use case
// fail later with a nil port.
func (d Deps) validate() error {
	missing := make([]string, 0, 8)
	if d.Registry == nil {
		missing = append(missing, "provider registry")
	}
	if d.Workspaces == nil {
		missing = append(missing, "workspace resolver")
	}
	if d.Store == nil {
		missing = append(missing, "session store")
	}
	if d.Runtime == nil {
		missing = append(missing, "process runtime")
	}
	if d.Leases == nil {
		missing = append(missing, "lease broker")
	}
	if d.Terminals == nil {
		missing = append(missing, "terminal source")
	}
	if d.Events == nil {
		missing = append(missing, "event publisher")
	}
	if d.Clock == nil {
		missing = append(missing, "clock")
	}
	if d.IDs == nil {
		missing = append(missing, "id minter")
	}
	if len(missing) > 0 {
		return session.NewError(session.CodeInvalidConfiguration, "application service",
			"missing injected port(s): "+strings.Join(missing, ", "),
			"wire every port before starting the owner")
	}
	return nil
}

// Option configures a Service.
type Option func(*Service)

// WithAuthority sets the authority of the rows this owner reports.
//
// The default is session.AuthorityLive, because an owner that holds the process
// runtime has live access. A reader without the runtime uses
// session.AuthorityStored, so a persisted running row is never presented as a
// live process.
func WithAuthority(authority session.Authority) Option {
	return func(s *Service) { s.authority = authority }
}

// WithStopGrace sets the window a graceful stop waits before it reports a stop
// timeout. It must be positive.
func WithStopGrace(grace time.Duration) Option {
	return func(s *Service) { s.grace = grace }
}

// Service is the application layer: one owner of sessions, workspaces, children
// and interactive leases.
//
// It is safe for concurrent use, and the concurrency model is deliberately
// conservative for Stage 02. Reads - Scan, List and Get - run concurrently
// against the engine, which serializes each session's lifecycle itself. Every
// mutating use case, Report included, runs under one owner-wide mutation lock
// instead of a per-session keyed lock: a use case spans several steps (authorize,
// apply, act through a port, commit, publish) and no keyed lock can be held across
// all of them without a second abstraction this stage has no use for. One lock is
// also what makes two guarantees checkable rather than hopeful:
//
//   - a store commit and the metadata event that announces it happen in the same
//     critical section, so the store's revision order and the event stream's
//     revision order are the same order and a committed mutation can never lose
//     its event;
//   - delete, restart, attach and detach are serialized against each other, so a
//     multi-step use case can never authorize against a state another use case
//     replaces halfway through.
//
// The price is that a mutation blocks other mutations for as long as it acts
// through a port (a graceful stop waits out its whole window). That is the
// intended trade for this stage: correctness of the record first, throughput
// later.
type Service struct {
	registry   ProviderRegistry
	workspaces WorkspaceResolver
	store      SessionStore
	runtime    ChildRuntime
	leases     LeaseBroker
	terminals  TerminalSource
	events     MetadataPublisher
	clock      Clock
	ids        IDMinter

	authority session.Authority
	grace     time.Duration

	// mutMu serializes every mutating use case. It is held across the reducer
	// applies, the port calls, the store commit and the event publish, so a
	// use case observes and commits one state.
	mutMu sync.Mutex
	// writeFault latches the first store write failure. It is read and written
	// only under mutMu.
	writeFault error

	// storeMu guards storeRevision, which is the revision the owner believes the
	// store is at. A read only takes it to stamp a row, so a long-running
	// mutation never blocks a reader.
	storeMu       sync.RWMutex
	storeRevision uint64

	// engine is the in-memory lifecycle authority. It is built once from the
	// stored sessions and never replaced, and it serializes each session's
	// lifecycle itself.
	engine *session.Engine

	// ioMu guards the interactive lease bookkeeping. The owner has to remember
	// which lease it granted, and to which attempt it is bound, so the exact
	// lease of a finished attempt can be released.
	ioMu sync.Mutex
	held map[attemptKey]heldLease
}

// attemptKey identifies one attempt's interactive claim. Keying by attempt as
// well as by session is what keeps a lease from outliving its generation: a stale
// lease is released when its attempt ends and can never block the next one.
type attemptKey struct {
	session    session.ID
	generation session.Generation
}

type heldLease struct {
	lease        InteractiveLease
	subscription TerminalSubscription
}

// NewService builds the application service and loads the stored sessions.
//
// Loading here is what makes the owner's in-memory state and the store agree
// before the first use case runs: a stored record that fails validation is a
// CORRUPT_STATE failure that blocks the owner, because repairing it is a manual
// step and silently resetting metadata is not an option.
func NewService(ctx context.Context, deps Deps, opts ...Option) (*Service, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	s := &Service{
		registry:   deps.Registry,
		workspaces: deps.Workspaces,
		store:      deps.Store,
		runtime:    deps.Runtime,
		leases:     deps.Leases,
		terminals:  deps.Terminals,
		events:     deps.Events,
		clock:      deps.Clock,
		ids:        deps.IDs,
		authority:  session.AuthorityLive,
		grace:      defaultStopGrace,
		held:       make(map[attemptKey]heldLease),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	if !s.authority.Valid() {
		return nil, session.NewError(session.CodeInvalidConfiguration, "application service",
			"authority "+string(s.authority)+" is not live or stored", "use live or stored")
	}
	if s.grace <= 0 {
		return nil, session.NewError(session.CodeInvalidConfiguration, "application service",
			"the graceful stop window must be positive", "configure a stop window such as 5s")
	}

	stored, revision, err := s.store.Load(ctx)
	if err != nil {
		return nil, storeError("read the session metadata", revision, err)
	}
	engine, err := session.NewEngine(stored...)
	if err != nil {
		// A stored record that no longer validates is corrupt metadata, not a bad
		// request: the operator repairs it by hand, and asd never resets session
		// metadata to make the problem disappear.
		return nil, session.WrapError(session.CodeCorruptState, "session store",
			"the stored session metadata is not valid: "+err.Error(),
			"repair the stored record by hand; asd never resets session metadata for you", err)
	}
	s.engine = engine
	s.storeRevision = revision
	return s, nil
}

// Authority reports the authority of the rows this owner returns.
func (s *Service) Authority() session.Authority { return s.authority }

// Revision returns the store revision the owner is working at.
func (s *Service) Revision() uint64 {
	s.storeMu.RLock()
	defer s.storeMu.RUnlock()
	return s.storeRevision
}

func (s *Service) now() time.Time { return s.clock.Now() }

// ---------------------------------------------------------------------------
// Mutation lock and the write latch
// ---------------------------------------------------------------------------

// enterMutation takes the owner-wide mutation lock and returns the function that
// releases it.
//
// It also refuses the mutation when the owner has already lost its ability to
// persist, which is what turns a store failure into a latched condition instead
// of a silent divergence. Once the store has refused a write, the engine holds
// state the store has never seen - a live child whose identity, a terminal
// attempt, a deleted row - and every later mutation would write all of it at
// once. That is how a transient disk error turns into metadata nobody can
// explain, so the owner refuses further mutations with OWNER_UNAVAILABLE and
// leaves the recovery to the operator: reads keep working, because a read cannot
// make the divergence worse and an operator needs them to see what happened.
func (s *Service) enterMutation(subject string) (func(), error) {
	s.mutMu.Lock()
	if s.writeFault != nil {
		s.mutMu.Unlock()
		return nil, s.writeUnavailable(subject)
	}
	return s.mutMu.Unlock, nil
}

// writeUnavailable reports the latched write failure with the recovery the
// operator needs.
func (s *Service) writeUnavailable(subject string) error {
	return session.WrapError(session.CodeOwnerUnavailable, subject,
		"the session metadata could not be written, so this owner will not record another mutation: "+s.writeFault.Error(),
		"restart asd so the owner reloads the metadata from the store; if the write keeps failing, inspect the state file and its permissions",
		s.writeFault)
}

// latchWrite records the first store write failure. It never clears the fault:
// the engine cannot be rewound, so the owner stays write-unavailable until it is
// restarted. It is called under the mutation lock, which is the only place a
// write happens.
func (s *Service) latchWrite(err error) {
	if s.writeFault == nil {
		s.writeFault = err
	}
}

// ---------------------------------------------------------------------------
// Capability and provider resolution
// ---------------------------------------------------------------------------

// RequireProvider returns the provider for an agent id and refuses a provider
// that does not declare need.
//
// It is the one capability gate the use cases share. A provider that declares
// neither the core launch capability nor a native vendor capability reports a
// typed UNSUPPORTED error rather than a generic failure, so `asd` can say which
// operation is unavailable instead of pretending it tried.
func (s *Service) RequireProvider(agentID agent.ID, need agent.Capability) (Provider, error) {
	if err := agentID.Validate(); err != nil {
		return nil, session.WrapError(session.CodeInvalidConfiguration, string(agentID),
			"the agent id is not valid", "run a scan to see valid agent ids", err)
	}
	provider, found := s.registry.Lookup(agentID)
	if !found || provider == nil {
		return nil, session.NewError(session.CodeNotFound, string(agentID),
			"no agent definition is registered under this id",
			"run a scan to see the agents this build knows about")
	}
	if !need.Valid() {
		return nil, session.NewError(session.CodeInvalidConfiguration, string(agentID),
			"capability "+string(need)+" is not a defined operation",
			"request one of "+capabilityNames())
	}
	if err := provider.Capabilities().Supports(need); err != nil {
		return nil, session.NewError(session.CodeUnsupported, string(agentID),
			"this agent definition does not provide the "+string(need.Class())+" capability "+string(need),
			capabilityHint(need)).ForAttempt(0)
	}
	return provider, nil
}

// RequireCapability is the capability gate for an operation this owner does not
// implement itself.
//
// Every native vendor operation - listing the vendor's own sessions, reading its
// logs, resuming a conversation or killing through its API - is off until an
// adapter exists, and the typed UNSUPPORTED error is what keeps a caller honest
// about that instead of silently substituting a restart.
func (s *Service) RequireCapability(agentID agent.ID, need agent.Capability) error {
	_, err := s.RequireProvider(agentID, need)
	return err
}

func capabilityNames() string {
	names := make([]string, 0, len(agent.AllCapabilities()))
	for _, c := range agent.AllCapabilities() {
		names = append(names, c.String())
	}
	return strings.Join(names, ", ")
}

func capabilityHint(need agent.Capability) string {
	switch need {
	case agent.CapabilityLaunch:
		return "choose an agent definition that can be launched, or install the executable it resolves"
	case agent.CapabilityInteractive:
		return "this agent definition has no interactive terminal; attach to a launched session instead"
	default:
		return "no adapter provides this vendor operation in this build; restart re-runs the resolved command instead of resuming a vendor conversation"
	}
}

// ---------------------------------------------------------------------------
// Reference resolution
// ---------------------------------------------------------------------------

// resolve turns a user-supplied reference into a session id.
//
// An ambiguous prefix is refused with CONFLICT and the candidates, and an
// unknown reference with NOT_FOUND. The owner never guesses which session was
// meant.
func (s *Service) resolve(ref string) (session.ID, error) {
	if err := validateRef(ref); err != nil {
		return "", err
	}
	snap, err := s.snapshot(s.now())
	if err != nil {
		return "", err
	}
	row, err := snap.FindByPrefix(ref)
	if err != nil {
		var typed *session.Error
		if errors.As(err, &typed) {
			return "", typed
		}
		return "", session.WrapError(session.CodeOf(err), ref,
			"the session reference does not resolve", "list the sessions to see valid ids", err)
	}
	return row.ID, nil
}

// require loads the current state of one session.
func (s *Service) require(ref string) (session.Session, error) {
	id, err := s.resolve(ref)
	if err != nil {
		return session.Session{}, err
	}
	current, found := s.engine.Get(id)
	if !found {
		return session.Session{}, session.NewError(session.CodeNotFound, ref,
			"no session is registered under this id", "list the sessions to see valid ids")
	}
	return current, nil
}

// ---------------------------------------------------------------------------
// Snapshots
// ---------------------------------------------------------------------------

// snapshot projects the owner's state into the read model at its authority.
func (s *Service) snapshot(at time.Time) (session.Snapshot, error) {
	return session.NewSnapshot(s.authority, at, s.Revision(), s.engine.Sessions())
}

// row projects one session at the owner's authority.
func (s *Service) row(current session.Session, at time.Time) session.SessionSnapshot {
	return session.SessionSnapshotOf(current, s.authority, at, s.Revision())
}

// ---------------------------------------------------------------------------
// Reducer authorization
// ---------------------------------------------------------------------------

// authorize asks the pure reducer what would happen, without changing anything.
//
// Every use case calls this before it touches a port, so a refusal is reported
// with no process spawned, no signal delivered, no lease taken and no metadata
// written. The reducer is pure, so authorizing and later applying the same event
// reach the same verdict.
func authorize(prev session.Session, event session.Event) (session.Outcome, error) {
	outcome := session.Reduce(prev, event)
	if outcome.Rejected() {
		return outcome, rejectionError(outcome)
	}
	if !outcome.Applied {
		return outcome, session.NewError(session.CodeUnknown, string(prev.ID),
			"the reducer neither applied nor refused a valid observation",
			"this is a contract bug; report it with the session id")
	}
	return outcome, nil
}

// apply records one observation through the engine, which serializes this
// session's lifecycle.
func (s *Service) apply(id session.ID, event session.Event) (session.Outcome, error) {
	outcome, err := s.engine.Apply(id, event)
	if err != nil {
		return outcome, engineError(id, "record the session observation", err)
	}
	if outcome.Rejected() {
		return outcome, rejectionError(outcome)
	}
	return outcome, nil
}

// rejectionError unwraps a reducer refusal into the domain's typed error.
//
// Every use case therefore returns a *session.Error that names the subject, the
// reason and the next action, which is what the CLI contract requires of a
// user-visible failure. The contract row stays inside the reducer, where the row
// table and its tests live.
func rejectionError(outcome session.Outcome) error {
	if outcome.Rejection != nil && outcome.Rejection.Err != nil {
		return outcome.Rejection.Err
	}
	return outcome.Err()
}

// ---------------------------------------------------------------------------
// Persistence
// ---------------------------------------------------------------------------

// commit writes every session the owner holds.
//
// The expected revision is the last revision this owner observed from the store,
// refreshed under the same lock on every successful write. That gives the two
// properties the store contract needs at once: the owner's own threads serialize
// instead of fighting over the revision, and a commit that is based on metadata
// another owner has since replaced is refused as a conflict rather than silently
// overwriting it.
//
// A failure latches the owner: the engine is now ahead of the store, so no later
// mutation may write it. The caller returns the typed store error unchanged, so
// the use case still reports what actually failed.
func (s *Service) commit(ctx context.Context) (uint64, error) {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	expected := s.storeRevision
	next, err := s.store.Commit(ctx, s.engine.Sessions(), expected)
	if err != nil {
		// The latch keeps the store's own cause rather than the wrapper this
		// function returns, so the recovery message a later caller reads is not the
		// same failure described twice.
		s.latchWrite(err)
		return 0, storeError("write the session metadata", expected, err)
	}
	s.storeRevision = next
	return next, nil
}

// loadPersisted reads the store's current sessions and revision.
//
// The read takes the same lock a write holds, so a reader can never observe a
// store mid-transaction. That is the one place a read waits for a write, and it
// is why the revision bookkeeping and the revision check share a lock: a row
// stamped with a revision the store has not reached yet would be a lie.
func (s *Service) loadPersisted(ctx context.Context) ([]session.Session, uint64, error) {
	s.storeMu.RLock()
	defer s.storeMu.RUnlock()
	return s.store.Load(ctx)
}

// deletePersisted removes one session's metadata at the observed revision. A
// failure latches the owner for the same reason a failed commit does.
func (s *Service) deletePersisted(ctx context.Context, id session.ID) (uint64, error) {
	s.storeMu.Lock()
	defer s.storeMu.Unlock()
	expected := s.storeRevision
	next, err := s.store.Delete(ctx, id, expected)
	if err != nil {
		s.latchWrite(err)
		return 0, storeError("delete the session metadata", expected, err)
	}
	s.storeRevision = next
	return next, nil
}

// ---------------------------------------------------------------------------
// Metadata events
// ---------------------------------------------------------------------------

// Event types the owner publishes beyond the PRODUCT §21 set.
//
// The set in internal/events is deliberately open, so a stage adds a kind here
// rather than changing the shared package.
const (
	// TypeSessionRenamed reports a changed user-visible label.
	TypeSessionRenamed events.Type = "session.renamed"
	// TypeSessionDeleted reports removed session metadata.
	TypeSessionDeleted events.Type = "session.deleted"
	// TypeSessionStopTimeout reports a graceful stop whose window closed with the
	// child still alive. It is deliberately not TypeSessionStopped, because
	// nothing stopped.
	TypeSessionStopTimeout events.Type = "session.stop-timeout"
	// TypeSessionUnknown reports that liveness could not be established. It is
	// deliberately not TypeSessionDead: an inconclusive probe and an unverifiable
	// PTY loss both leave the session running-but-unknown, and a consumer that
	// reads dead would mark a live session dead (T15, T18).
	TypeSessionUnknown events.Type = "session.unknown"
	// TypeSessionOrphaned reports a surviving process with no interactive I/O
	// (T14). The process is alive and the session is not interactive, which is a
	// third answer, not a death.
	TypeSessionOrphaned events.Type = "session.orphaned"
	// TypeSessionStale reports that a recorded pid now carries a different
	// process, so the attempt was closed without adopting or signalling it (T17).
	TypeSessionStale events.Type = "session.stale"
)

// eventExtras carries the per-mutation metadata fields that are not part of a
// session row.
type eventExtras struct {
	// transition names the contract row the use case implemented.
	transition string
	// stopTimedOut marks the T9 outcome.
	stopTimedOut bool
}

// publish announces one committed mutation.
//
// It is called only after the store accepted the mutation, and it stamps the
// event with the revision that mutation produced, so a subscriber observes
// mutations in the order the store committed them. The metadata is bounded and
// typed: lifecycle, attachment, evidenced activity, identity numbers and the
// reason kind. It never carries terminal bytes, environment material, argv, or
// the free-form failure detail, which could hold any of those.
func (s *Service) publish(revision uint64, typ events.Type, row session.SessionSnapshot, at time.Time, extras eventExtras) (events.Event, error) {
	meta, err := eventMetadata(row, at, extras)
	if err != nil {
		return events.Event{}, s.publicationError(row, revision, err)
	}
	spec := events.Spec{
		Type:     typ,
		Session:  events.SessionID(row.ID),
		Attempt:  events.AttemptGeneration(row.Generation),
		Time:     events.NewTimestamp(at),
		Metadata: meta,
	}
	event, err := s.events.PublishRevisioned(events.Revision(revision), spec)
	if err != nil {
		return events.Event{}, s.publicationError(row, revision, err)
	}
	return event, nil
}

// eventMetadata builds the typed metadata of a session event.
func eventMetadata(row session.SessionSnapshot, at time.Time, extras eventExtras) (events.Metadata, error) {
	meta := events.Metadata{}
	var err error
	add := func(key string, value events.Value) {
		if err != nil {
			return
		}
		meta, err = meta.With(key, value)
	}
	add("name", events.StringValue(row.Name))
	add("agent", events.StringValue(string(row.AgentID)))
	add("workspace", events.StringValue(row.WorkspaceID.Path()))
	add("lifecycle", events.StringValue(row.Lifecycle.String()))
	add("attachment", events.StringValue(row.Attachment.String()))
	add("generation", events.Uint64Value(uint64(row.Generation)))
	add("attempt_count", events.Int64Value(int64(row.AttemptCount)))
	add("authority", events.StringValue(row.Authority.String()))
	add("observed_at", events.TimestampValue(events.NewTimestamp(at)))
	add("store_revision", events.Uint64Value(row.StoreRevision))
	add("orphaned", events.BoolValue(row.Orphaned()))
	add("stop_timed_out", events.BoolValue(extras.stopTimedOut))
	if extras.transition != "" {
		add("transition", events.StringValue(extras.transition))
	}
	// An activity is published only when a provider signal justifies it; silence
	// never means idle.
	if row.ActivityEvidenced {
		add("activity", events.StringValue(row.Activity.String()))
	}
	if !row.Reason.Zero() {
		add("reason", events.StringValue(row.Reason.Kind.String()))
	}
	if code, observed := row.Reason.ExitStatus(); observed {
		add("exit_code", events.Int64Value(int64(code)))
	}
	if row.HasIdentity {
		add("pid", events.Int64Value(int64(row.Identity.PID)))
		add("pgid", events.Int64Value(int64(row.Identity.PGID)))
	}
	if err != nil {
		return events.Metadata{}, err
	}
	return meta, nil
}

// publicationError reports a committed mutation whose event could not be
// published.
//
// The mutation stands, so the error says so: the session state is correct and
// the caller may render the result it already has, while the event stream is
// behind and needs a resync.
func (s *Service) publicationError(row session.SessionSnapshot, revision uint64, cause error) error {
	code := session.CodeOf(cause)
	if errors.Is(cause, events.ErrRevisionRegressed) {
		code = session.CodeConflict
	}
	if code == "" || code == session.CodeUnknown {
		code = session.CodeUnknown
	}
	typed := session.WrapError(code, string(row.ID),
		"the mutation is committed but its metadata event was not published: "+cause.Error(),
		"read the session again to confirm the state, then resynchronize the event stream", cause)
	return typed.ForAttempt(row.Generation)
}

// ---------------------------------------------------------------------------
// Error translation
// ---------------------------------------------------------------------------

// storeError turns a store failure into a typed error that keeps the subject,
// the reason and the next action.
func storeError(what string, expected uint64, err error) error {
	code := session.CodeOf(err)
	if code == "" || code == session.CodeUnknown {
		// A port that reports no code is still an operational failure, and the
		// CLI contract forbids a bare "failed" with no next action.
		code = session.CodeUnknown
	}
	hint := "retry the operation; if it keeps failing, inspect the state file"
	if code == session.CodeConflict {
		hint = "another writer advanced the metadata; read the sessions again and retry"
	}
	reason := what + " failed"
	if expected > 0 {
		reason = what + " failed at store revision " + formatUint(expected)
	}
	return session.WrapError(code, "session store", reason, hint, err)
}

// engineError turns an engine failure into a typed error.
func engineError(id session.ID, what string, err error) error {
	code := session.CodeOf(err)
	if code == "" || code == session.CodeUnknown {
		code = session.CodeCorruptState
	}
	return session.WrapError(code, string(id), what+" failed",
		"re-read the session; if the record is inconsistent, repair it by hand", err)
}

// launchError turns a failed attempt into the launch failure the CLI reports. The
// session id, the attempt generation and the reason are always present, so a
// caller can say which attempt of which session failed and what to try next.
func launchError(id session.ID, generation session.Generation, reason string) error {
	return session.NewError(session.CodeLaunchFailed, string(id), reason,
		"fix the agent's resolved command, then restart the session to try again").ForAttempt(generation)
}

// signalError turns a signal failure into a typed error.
func signalError(id session.ID, generation session.Generation, signal session.SignalKind, cause error) error {
	code := session.CodeOf(cause)
	if code == "" || code == session.CodeUnknown {
		code = session.CodePermissionDenied
	}
	return session.WrapError(code, string(id),
		"the "+string(signal)+" signal was not delivered: "+cause.Error(),
		"verify that the process is owned by this user and that its identity still matches", cause).
		ForAttempt(generation)
}

// stopTimeoutError reports the T9 outcome: the child outlived the grace window.
//
// The session is still running, so the error must never read as killed. It names
// the still-running attempt and the explicit kill that would end it.
func stopTimeoutError(row session.SessionSnapshot) error {
	return session.NewError(session.CodeConflict, string(row.ID),
		"the child did not exit inside the graceful stop window, so the session is still running",
		"wait longer and stop again, or end it explicitly with asd kill "+string(row.ID)+" --yes").
		ForAttempt(row.Generation)
}

// releaseError reports a lease or stream release failure.
func releaseError(id session.ID, generation session.Generation, what string, cause error) error {
	code := session.CodeOf(cause)
	if code == "" || code == session.CodeUnknown {
		code = session.CodeSessionIOFailed
	}
	return session.WrapError(code, string(id), what+" failed",
		"the session is unaffected; detach again once the client is gone", cause).
		ForAttempt(generation)
}

func formatUint(v uint64) string {
	return strconv.FormatUint(v, 10)
}

// ---------------------------------------------------------------------------
// Interface proofs
// ---------------------------------------------------------------------------

var _ Client = (*Service)(nil)
