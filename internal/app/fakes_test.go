package app

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// Every fake below is deterministic: a fixed clock, a fixed id sequence and a
// scripted runtime. That is what lets these tests prove the use cases need no
// process, no filesystem, no PTY and no transport.

// ---------------------------------------------------------------------------
// Clock and identity
// ---------------------------------------------------------------------------

// baseTime is the fixed instant every test observes.
var baseTime = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// stepClock hands out baseTime plus one second per reading, so successive
// instants inside one use case are strictly ordered and reproducible.
type stepClock struct {
	mu sync.Mutex
	n  int
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return baseTime.Add(time.Duration(c.n) * time.Second)
}

// fixedClock always reports the same instant.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// seqIDs mints predictable session identities.
type seqIDs struct {
	mu    sync.Mutex
	names []string
	next  int
}

func newSeqIDs(names ...string) *seqIDs { return &seqIDs{names: names} }

func (g *seqIDs) MintID(context.Context) (session.ID, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.next < len(g.names) {
		name := g.names[g.next]
		g.next++
		return session.ID(name), nil
	}
	g.next++
	return session.ID(fmt.Sprintf("s-%06d", g.next)), nil
}

var _ IDMinter = (*seqIDs)(nil)

// ---------------------------------------------------------------------------
// Agent registry
// ---------------------------------------------------------------------------

// fakeProvider resolves one fixed command and remembers the request it saw, so a
// test can prove the literal argv reached the resolver untouched.
type fakeProvider struct {
	id         agent.ID
	caps       agent.Capabilities
	command    agent.Command
	err        error
	resolves   int
	lastExtra  []string
	lastWorksp string
	lastName   string
}

func newFakeProvider(t *testing.T, id agent.ID, executable string, extra ...string) *fakeProvider {
	t.Helper()
	caps, err := agent.CapabilitiesOf(agent.CapabilityLaunch, agent.CapabilityInteractive)
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	command, err := agent.NewCommand(executable, extra)
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	return &fakeProvider{id: id, caps: caps, command: command}
}

func (p *fakeProvider) ID() agent.ID                     { return p.id }
func (p *fakeProvider) Capabilities() agent.Capabilities { return p.caps }
func (p *fakeProvider) Resolve(_ context.Context, req agent.ResolveRequest) (agent.Command, error) {
	p.resolves++
	p.lastExtra = req.ExtraArgs()
	p.lastWorksp = req.WorkspaceDir()
	p.lastName = req.Name()
	if p.err != nil {
		return agent.Command{}, p.err
	}
	// A real provider appends the literal `-- <argv...>` tail to the command it
	// resolves, so the frozen command carries every argument verbatim.
	args := append(p.command.Args(), req.ExtraArgs()...)
	return agent.NewCommand(p.command.Executable(), args)
}

var _ Provider = (*fakeProvider)(nil)

// fakeRegistry is a deterministic provider index.
type fakeRegistry struct {
	providers []Provider
	err       error
}

func (r *fakeRegistry) Lookup(id agent.ID) (Provider, bool) {
	for _, p := range r.providers {
		if p.ID() == id {
			return p, true
		}
	}
	return nil, false
}

func (r *fakeRegistry) IDs() []agent.ID {
	out := make([]agent.ID, 0, len(r.providers))
	for _, p := range r.providers {
		out = append(out, p.ID())
	}
	return out
}

var _ ProviderRegistry = (*fakeRegistry)(nil)

// ---------------------------------------------------------------------------
// Workspace resolver
// ---------------------------------------------------------------------------

// fakeResolver answers from a table so a test decides exactly which directory a
// candidate resolves to, and which candidate is refused.
type fakeResolver struct {
	mu       sync.Mutex
	known    map[string]string
	failures map[string]error
	calls    []string
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{known: map[string]string{}, failures: map[string]error{}}
}

func (r *fakeResolver) Resolve(_ context.Context, candidate workspace.ResolveRequest) (workspace.Workspace, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, candidate.Path())
	if err, failed := r.failures[candidate.Path()]; failed {
		return workspace.Workspace{}, err
	}
	dir, known := r.known[candidate.Path()]
	if !known {
		return workspace.Workspace{}, fmt.Errorf("no workspace is configured for %q", candidate.Path())
	}
	return workspace.New(dir)
}

func (r *fakeResolver) resolved() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

var _ WorkspaceResolver = (*fakeResolver)(nil)

// ---------------------------------------------------------------------------
// Session store
// ---------------------------------------------------------------------------

// fakeStore is an in-memory single-writer store. It enforces the optimistic
// expected revision exactly like a real implementation must, so the use cases
// are tested against the contract rather than against a permissive double.
type fakeStore struct {
	mu       sync.Mutex
	sessions []session.Session
	revision uint64
	commits  int
	deletes  int
	// commitErr and deleteErr force the next write to fail.
	commitErr error
	deleteErr error
}

func newFakeStore(sessions ...session.Session) *fakeStore {
	return &fakeStore{sessions: sessions, revision: 7}
}

func (s *fakeStore) Load(context.Context) ([]session.Session, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]session.Session, 0, len(s.sessions))
	for _, stored := range s.sessions {
		out = append(out, stored.Clone())
	}
	return out, s.revision, nil
}

func (s *fakeStore) Commit(_ context.Context, sessions []session.Session, expected uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commitErr != nil {
		err := s.commitErr
		s.commitErr = nil
		return s.revision, err
	}
	if expected != s.revision {
		return s.revision, session.NewError(session.CodeConflict, "store",
			"the expected revision "+formatUint(expected)+" is not the current revision "+formatUint(s.revision),
			"re-read the metadata and retry")
	}
	next := make([]session.Session, 0, len(sessions))
	for _, stored := range sessions {
		next = append(next, stored.Clone())
	}
	s.sessions = next
	s.revision++
	s.commits++
	return s.revision, nil
}

func (s *fakeStore) Delete(_ context.Context, id session.ID, expected uint64) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteErr != nil {
		err := s.deleteErr
		s.deleteErr = nil
		return s.revision, err
	}
	if expected != s.revision {
		return s.revision, session.NewError(session.CodeConflict, "store",
			"the expected revision does not match", "re-read the metadata and retry")
	}
	kept := make([]session.Session, 0, len(s.sessions))
	for _, stored := range s.sessions {
		if stored.ID != id {
			kept = append(kept, stored)
		}
	}
	s.sessions = kept
	s.revision++
	s.deletes++
	return s.revision, nil
}

func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *fakeStore) find(id session.ID) (session.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, stored := range s.sessions {
		if stored.ID == id {
			return stored.Clone(), true
		}
	}
	return session.Session{}, false
}

func (s *fakeStore) failNextCommit(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commitErr = err
}

var _ SessionStore = (*fakeStore)(nil)

// ---------------------------------------------------------------------------
// Process runtime
// ---------------------------------------------------------------------------

// scriptedRuntime is the confirmed process port: it answers with a fixed
// identity, a scripted stop outcome and a scripted force kill, and records every
// signal it was asked to deliver.
//
// The default kill is a confirmed one, because that is the only shape a kill may
// record: a delivered signal with no reap would leave the session running, which
// each test that needs it asks for explicitly.
type scriptedRuntime struct {
	mu         sync.Mutex
	identity   session.ProcessIdentity
	launches   []session.LaunchRequest
	launched   bool
	launchErr  error
	stops      []session.ProcessIdentity
	stop       session.StopOutcome
	stopErr    error
	stopHook   func()
	kills      []session.ProcessIdentity
	kill       session.KillOutcome
	killErr    error
	signals    []session.SignalKind
	signalErr  error
	waits      []session.ExitWait
	waitStatus session.ExitStatus
	waitErr    error
}

func newScriptedRuntime(identity session.ProcessIdentity) *scriptedRuntime {
	return &scriptedRuntime{
		identity: identity,
		kill: session.KillOutcome{
			Delivered: true,
			Signal:    "SIGKILL",
			Terminated: session.ExitStatus{
				Signal: "SIGKILL",
				At:     baseTime,
			},
		},
		waitStatus: session.ExitStatus{Signal: "SIGKILL", At: baseTime},
	}
}

func (r *scriptedRuntime) Launch(_ context.Context, req session.LaunchRequest) (session.ProcessIdentity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.launches = append(r.launches, req)
	if r.launchErr != nil {
		return session.ProcessIdentity{}, r.launchErr
	}
	r.launched = true
	return r.identity, nil
}

func (r *scriptedRuntime) Observe(context.Context, session.ProcessIdentity) (session.LivenessObservation, error) {
	return session.LivenessObservation{}, fmt.Errorf("the scripted runtime does not probe")
}

// Stop runs the scripted hook first, so a test can model the owner's single
// waiter landing its report inside the grace window, then answers with the
// scripted outcome.
func (r *scriptedRuntime) Stop(_ context.Context, recorded session.ProcessIdentity, _ time.Duration) (session.StopOutcome, error) {
	r.mu.Lock()
	hook := r.stopHook
	r.stops = append(r.stops, recorded)
	stop, stopErr := r.stop, r.stopErr
	r.mu.Unlock()
	if hook != nil {
		hook()
	}
	if stopErr != nil {
		return session.StopOutcome{}, stopErr
	}
	return stop, nil
}

func (r *scriptedRuntime) Signal(_ context.Context, _ session.ProcessIdentity, signal session.SignalKind) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.signals = append(r.signals, signal)
	return r.signalErr
}

func (r *scriptedRuntime) ForceKill(_ context.Context, recorded session.ProcessIdentity, _ time.Duration) (session.KillOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kills = append(r.kills, recorded)
	if r.killErr != nil {
		return session.KillOutcome{}, r.killErr
	}
	return r.kill, nil
}

func (r *scriptedRuntime) WaitExit(_ context.Context, wait session.ExitWait) (session.ExitStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.waits = append(r.waits, wait)
	if r.waitErr != nil {
		return session.ExitStatus{}, r.waitErr
	}
	return r.waitStatus, nil
}

func (r *scriptedRuntime) launchCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.launches)
}

func (r *scriptedRuntime) lastLaunch() (session.LaunchRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.launches) == 0 {
		return session.LaunchRequest{}, false
	}
	return r.launches[len(r.launches)-1], true
}

func (r *scriptedRuntime) signalKinds() []session.SignalKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]session.SignalKind(nil), r.signals...)
}

// forcedKills returns the identities the owner force-killed.
func (r *scriptedRuntime) forcedKills() []session.ProcessIdentity {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]session.ProcessIdentity(nil), r.kills...)
}

// reapConfirmed answers a force kill that reaped the child with an exit status.
func reapConfirmed(code int) session.KillOutcome {
	return session.KillOutcome{
		Delivered:  true,
		Signal:     "SIGKILL",
		Terminated: session.ExitStatus{ExitCode: &code, At: baseTime},
	}
}

// killDeliveredOnly answers a force kill whose signal reached the verified group
// but whose reap was never confirmed.
func killDeliveredOnly() session.KillOutcome {
	return session.KillOutcome{Delivered: true, Signal: "SIGKILL", Timeout: true}
}

var _ ChildRuntime = (*scriptedRuntime)(nil)

// ---------------------------------------------------------------------------
// Lease broker
// ---------------------------------------------------------------------------

// fakeBroker grants at most one lease per session attempt, like the V1 contract.
//
// The claim is keyed by attempt as well as by session and is validated against
// the attempt it names, which is what the contract requires: a lease is bound to
// its generation, so a stale claim can neither block the next generation of the
// same session nor be released as if it were the current one.
type fakeBroker struct {
	mu         sync.Mutex
	held       map[brokerKey]session.InteractiveLease
	granted    int
	released   int
	acquireErr error
	releaseErr error
}

// brokerKey is the (session, attempt) pair a lease is bound to.
type brokerKey struct {
	session    session.ID
	generation session.Generation
}

func newFakeBroker() *fakeBroker {
	return &fakeBroker{held: map[brokerKey]session.InteractiveLease{}}
}

func (b *fakeBroker) Acquire(_ context.Context, req session.Lease) (session.InteractiveLease, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.acquireErr != nil {
		return session.InteractiveLease{}, b.acquireErr
	}
	for key, lease := range b.held {
		if key.session == req.SessionID && key.generation >= req.Generation {
			return session.InteractiveLease{}, session.NewError(session.CodeConflict, string(req.SessionID),
				"the interactive lease of attempt "+key.generation.String()+" is held by "+lease.Holder,
				"wait for that client to detach").ForAttempt(key.generation)
		}
	}
	key := brokerKey{session: req.SessionID, generation: req.Generation}
	lease := session.InteractiveLease{
		SessionID:  req.SessionID,
		Generation: req.Generation,
		Holder:     req.Holder,
		AcquiredAt: req.At,
	}
	b.held[key] = lease
	b.granted++
	return lease, nil
}

func (b *fakeBroker) Release(_ context.Context, lease session.InteractiveLease) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := brokerKey{session: lease.SessionID, generation: lease.Generation}
	if _, taken := b.held[key]; taken {
		delete(b.held, key)
	}
	b.released++
	return b.releaseErr
}

// holder returns the current claim of a session, newest attempt first.
func (b *fakeBroker) holder(id session.ID) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var newest session.InteractiveLease
	found := false
	for key, lease := range b.held {
		if key.session != id {
			continue
		}
		if !found || key.generation > newest.Generation {
			newest, found = lease, true
		}
	}
	return newest.Holder, found
}

// holderOf returns the claim bound to one attempt of a session.
func (b *fakeBroker) holderOf(id session.ID, generation session.Generation) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	lease, ok := b.held[brokerKey{session: id, generation: generation}]
	return lease.Holder, ok
}

// claims counts the live claims the broker holds.
func (b *fakeBroker) claims() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.held)
}

var _ LeaseBroker = (*fakeBroker)(nil)

// ---------------------------------------------------------------------------
// Terminal source
// ---------------------------------------------------------------------------

// fakeTerminal is a stream claim that records closes instead of moving bytes.
type fakeTerminal struct {
	mu           sync.Mutex
	sessionID    session.ID
	generation   session.Generation
	holder       string
	closed       int
	resizes      [][2]int
	subscribeErr error
	closeErr     error
}

func (t *fakeTerminal) SessionID() session.ID          { return t.sessionID }
func (t *fakeTerminal) Generation() session.Generation { return t.generation }
func (t *fakeTerminal) Holder() string                 { return t.holder }
func (t *fakeTerminal) Read([]byte) (int, error)       { return 0, io.EOF }
func (t *fakeTerminal) Write(p []byte) (int, error)    { return len(p), nil }
func (t *fakeTerminal) Resize(w, h int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resizes = append(t.resizes, [2]int{w, h})
	return nil
}
func (t *fakeTerminal) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed++
	return t.closeErr
}

func (t *fakeTerminal) closeCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

var _ TerminalSubscription = (*fakeTerminal)(nil)

type fakeTerminalSource struct {
	mu           sync.Mutex
	granted      []*fakeTerminal
	subscribeErr error
}

func (s *fakeTerminalSource) Subscribe(_ context.Context, lease session.InteractiveLease) (TerminalSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subscribeErr != nil {
		return nil, s.subscribeErr
	}
	terminal := &fakeTerminal{
		sessionID:  lease.SessionID,
		generation: lease.Generation,
		holder:     lease.Holder,
	}
	s.granted = append(s.granted, terminal)
	return terminal, nil
}

func (s *fakeTerminalSource) last() (*fakeTerminal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.granted) == 0 {
		return nil, false
	}
	return s.granted[len(s.granted)-1], true
}

var _ TerminalSource = (*fakeTerminalSource)(nil)

// ---------------------------------------------------------------------------
// Event capture
// ---------------------------------------------------------------------------

// capturingPublisher records the specs it was handed instead of fanning them out.
// It validates the spec the same way the real publisher does, so a use case
// cannot smuggle an invalid event past the contract.
type capturingPublisher struct {
	mu     sync.Mutex
	specs  []capturedEvent
	failAt int
	err    error
}

type capturedEvent struct {
	revision events.Revision
	spec     events.Spec
}

func newCapturingPublisher() *capturingPublisher { return &capturingPublisher{} }

func (p *capturingPublisher) PublishRevisioned(revision events.Revision, spec events.Spec) (events.Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return events.Event{}, p.err
	}
	if spec.Type == "" {
		return events.Event{}, events.ErrInvalidType
	}
	p.specs = append(p.specs, capturedEvent{revision: revision, spec: spec})
	return events.Event{}, nil
}

func (p *capturingPublisher) all() []capturedEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]capturedEvent(nil), p.specs...)
}

func (p *capturingPublisher) types() []string {
	out := make([]string, 0)
	for _, captured := range p.all() {
		out = append(out, string(captured.spec.Type))
	}
	return out
}

var _ MetadataPublisher = (*capturingPublisher)(nil)

// realPublisher wires the actual events package so the metadata policy, the
// revision ordering and the fan-out are exercised for real.
func realPublisher() *events.Publisher {
	return events.NewPublisher(
		events.WithClock(func() time.Time { return baseTime }),
		events.WithIDMint(func(rev events.Revision) events.ID {
			return events.ID(fmt.Sprintf("evt-%03d", rev))
		}),
	)
}

// ---------------------------------------------------------------------------
// Fixtures
// ---------------------------------------------------------------------------

const (
	testWorkspace = "/home/tester/kestrel"
	secondWork    = "/home/tester/observability"
	launchBin     = "/usr/local/bin/opencode"
)

func testIdentity(pid int) session.ProcessIdentity {
	return session.ProcessIdentity{
		PID:             pid,
		PGID:            pid,
		BootID:          "boot-1",
		StartTicks:      4242,
		OwnerInstanceID: "owner-1",
	}
}

func mustCommand(t *testing.T, executable string, args ...string) agent.Command {
	t.Helper()
	command, err := agent.NewCommand(executable, args)
	if err != nil {
		t.Fatalf("build command: %v", err)
	}
	return command
}

// harness bundles the service with every fake the tests reach into.
type harness struct {
	svc      *Service
	provider *fakeProvider
	registry *fakeRegistry
	resolver *fakeResolver
	store    *fakeStore
	runtime  *scriptedRuntime
	broker   *fakeBroker
	terminal *fakeTerminalSource
	capture  *capturingPublisher
	clock    *stepClock
}

type harnessOption func(*harnessConfig)

type harnessConfig struct {
	authority session.Authority
	grace     time.Duration
	seed      []session.Session
}

func withAuthority(a session.Authority) harnessOption {
	return func(c *harnessConfig) { c.authority = a }
}

func withGrace(d time.Duration) harnessOption {
	return func(c *harnessConfig) { c.grace = d }
}

func withSeed(sessions ...session.Session) harnessOption {
	return func(c *harnessConfig) { c.seed = sessions }
}

// newHarness builds a service over deterministic fakes.
func newHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()
	cfg := harnessConfig{authority: session.AuthorityLive}
	for _, opt := range opts {
		opt(&cfg)
	}
	provider := newFakeProvider(t, "opencode", launchBin, "--acp")
	registry := &fakeRegistry{providers: []Provider{provider}}
	resolver := newFakeResolver()
	resolver.known[testWorkspace] = testWorkspace
	resolver.known[secondWork] = secondWork
	store := newFakeStore(cfg.seed...)
	runtime := newScriptedRuntime(testIdentity(4242))
	broker := newFakeBroker()
	terminal := &fakeTerminalSource{}
	capture := newCapturingPublisher()
	clock := &stepClock{}

	options := []Option{WithAuthority(cfg.authority)}
	if cfg.grace > 0 {
		options = append(options, WithStopGrace(cfg.grace))
	}
	svc, err := NewService(context.Background(), Deps{
		Registry:   registry,
		Workspaces: resolver,
		Store:      store,
		Runtime:    runtime,
		Leases:     broker,
		Terminals:  terminal,
		Events:     capture,
		Clock:      clock,
		IDs:        newSeqIDs(),
	}, options...)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return &harness{
		svc: svc, provider: provider, registry: registry, resolver: resolver,
		store: store, runtime: runtime, broker: broker, terminal: terminal,
		capture: capture, clock: clock,
	}
}

// createRunning is the common fixture: a launched, attached-free running session.
func (h *harness) createRunning(t *testing.T, extraArgs ...string) CreateResult {
	t.Helper()
	result, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: testWorkspace,
		Name:          "kestrel",
		ExtraArgs:     extraArgs,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return result
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

func requireCode(t *testing.T, err error, want session.Code) *session.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", want)
	}
	typed, ok := err.(*session.Error)
	if !ok {
		t.Fatalf("error %v (%T) is not a typed *session.Error", err, err)
	}
	if typed.Code != want {
		t.Fatalf("error code = %s, want %s (%v)", typed.Code, want, err)
	}
	if typed.Reason == "" {
		t.Fatalf("error %v has no reason", typed)
	}
	return typed
}

// contains reports whether a typed error's text names an expectation, so the
// tests assert the user-visible message and not only the code.
func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
