package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestCreateResolvesLaunchesAndCommits(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)

	if created.Session.ID != "s-000001" {
		t.Fatalf("session id = %q, want the minted s-000001", created.Session.ID)
	}
	if created.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("lifecycle = %s, want running", created.Session.Lifecycle)
	}
	if created.Session.Attachment != session.AttachmentDetached {
		t.Fatalf("attachment = %s, want detached", created.Session.Attachment)
	}
	if created.Session.Generation != 1 {
		t.Fatalf("generation = %d, want 1", created.Session.Generation)
	}
	if !created.Session.HasIdentity {
		t.Fatal("the running session recorded no process identity")
	}
	if created.Revision != 8 {
		t.Fatalf("revision = %d, want the store's post-commit revision 8", created.Revision)
	}
	if h.store.count() != 1 {
		t.Fatalf("stored sessions = %d, want 1", h.store.count())
	}
	stored, found := h.store.find(created.Session.ID)
	if !found {
		t.Fatal("the created session was not persisted")
	}
	attempt, hasAttempt := stored.Current()
	if !hasAttempt || attempt.Lifecycle != session.LifecycleRunning {
		t.Fatalf("persisted attempt = %+v, want running", attempt)
	}

	captured := h.capture.all()
	if len(captured) != 1 {
		t.Fatalf("published events = %v, want exactly one", h.capture.types())
	}
	if captured[0].spec.Type != events.TypeSessionStarted {
		t.Fatalf("event type = %s, want %s", captured[0].spec.Type, events.TypeSessionStarted)
	}
	if captured[0].spec.Session != events.SessionID(created.Session.ID) {
		t.Fatalf("event session = %q, want %q", captured[0].spec.Session, created.Session.ID)
	}
	if captured[0].spec.Attempt != events.AttemptGeneration(1) {
		t.Fatalf("event attempt = %d, want 1", captured[0].spec.Attempt)
	}
	if captured[0].revision != events.Revision(created.Revision) {
		t.Fatalf("event revision = %d, want the committed revision %d", captured[0].revision, created.Revision)
	}
}

func TestCreatePreservesLiteralArgv(t *testing.T) {
	h := newHarness(t)
	extra := []string{
		"--prompt", "fix the flaky test with a space",
		"--glob", "*.go",
		"--quote", `"already quoted"`,
		"--empty-looking", "-",
	}
	created := h.createRunning(t, extra...)

	want := append([]string{launchBin, "--acp"}, extra...)
	if got := created.Session.Command.Argv(); !slices.Equal(got, want) {
		t.Fatalf("frozen argv = %q, want %q", got, want)
	}
	if got := h.provider.lastExtra; !slices.Equal(got, extra) {
		t.Fatalf("provider saw extra args %q, want %q", got, extra)
	}
	if h.provider.lastWorksp != testWorkspace {
		t.Fatalf("provider workspace = %q, want %q", h.provider.lastWorksp, testWorkspace)
	}
	// The launch request handed to the runtime must carry the same literal argv,
	// never a re-split or expanded form.
	launch, ok := h.runtime.lastLaunch()
	if !ok {
		t.Fatal("the runtime was never asked to launch")
	}
	if got := launch.Command.Argv(); !slices.Equal(got, want) {
		t.Fatalf("launch argv = %q, want %q", got, want)
	}
	if launch.Workspace.Path() != testWorkspace {
		t.Fatalf("launch workspace = %q, want %q", launch.Workspace.Path(), testWorkspace)
	}
	if launch.Generation != 1 {
		t.Fatalf("launch generation = %d, want 1", launch.Generation)
	}
}

func TestCreateRefusesUnknownAgent(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "not-registered",
		WorkspacePath: testWorkspace,
	})
	typed := requireCode(t, err, session.CodeNotFound)
	if typed.Subject != "not-registered" {
		t.Fatalf("subject = %q, want the agent id", typed.Subject)
	}
	if typed.Hint == "" {
		t.Fatal("the refusal has no hint")
	}
	if h.runtime.launchCount() != 0 {
		t.Fatal("a refused launch spawned something")
	}
	if h.store.count() != 0 {
		t.Fatal("a refused launch wrote metadata")
	}
	if len(h.capture.all()) != 0 {
		t.Fatal("a refused launch published an event")
	}
}

func TestCreateRefusesProviderWithoutLaunchCapability(t *testing.T) {
	h := newHarness(t)
	h.provider.caps = agent.Capabilities(0)
	_, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: testWorkspace,
	})
	typed := requireCode(t, err, session.CodeUnsupported)
	if typed.Subject != "opencode" {
		t.Fatalf("subject = %q, want the agent id", typed.Subject)
	}
	if typed.Reason == "" || typed.Hint == "" {
		t.Fatalf("unsupported error must carry a reason and a hint: %v", typed)
	}
	if h.runtime.launchCount() != 0 {
		t.Fatal("a provider without the launch capability still spawned something")
	}
}

func TestCreateRefusesUnresolvableWorkspace(t *testing.T) {
	h := newHarness(t)
	h.resolver.failures[secondWork] = &workspace.InvalidPathError{Path: secondWork, Problem: "no such directory"}
	_, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: secondWork,
	})
	requireCode(t, err, session.CodeInvalidConfiguration)
	if h.provider.resolves != 0 {
		t.Fatal("the provider was asked to resolve a command for an unusable workspace")
	}
	if h.runtime.launchCount() != 0 {
		t.Fatal("a refused workspace spawned something")
	}
	if h.store.count() != 0 {
		t.Fatal("a refused workspace wrote metadata")
	}
}

func TestCreateRecordsTheFailedAttemptWhenTheSpawnFails(t *testing.T) {
	h := newHarness(t)
	h.runtime.launchErr = errors.New("fork/exec: no such file or directory")
	_, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: testWorkspace,
	})
	typed := requireCode(t, err, session.CodeLaunchFailed)
	if typed.Attempt != 1 {
		t.Fatalf("error attempt = %d, want 1", typed.Attempt)
	}

	stored, found := h.store.find("s-000001")
	if !found {
		t.Fatal("a failed launch must still persist its attempt")
	}
	attempt, hasAttempt := stored.Current()
	if !hasAttempt || attempt.Lifecycle != session.LifecycleFailed {
		t.Fatalf("persisted attempt = %+v, want failed", attempt)
	}
	if attempt.Reason.Kind != session.ReasonLaunchFailed {
		t.Fatalf("recorded reason = %s, want %s", attempt.Reason.Kind, session.ReasonLaunchFailed)
	}
	if types := h.capture.types(); len(types) != 1 || types[0] != string(TypeSessionLaunchFailed) {
		t.Fatalf("published events = %v, want one %s", types, TypeSessionLaunchFailed)
	}
	if len(h.runtime.signalKinds()) != 0 {
		t.Fatal("a spawn that produced no identity must never signal anything")
	}
}

func TestCreateRefusesAnUnverifiableSpawnIdentity(t *testing.T) {
	h := newHarness(t)
	// A PID with no boot id or owner instance is not proof of ownership, so a
	// runtime that returns one has not proven it spawned anything.
	h.runtime.identity = session.ProcessIdentity{PID: 999}
	_, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: testWorkspace,
	})
	requireCode(t, err, session.CodeLaunchFailed)
	if signals := h.runtime.signalKinds(); len(signals) != 0 {
		t.Fatalf("an unverifiable identity was signalled: %v", signals)
	}
	stored, found := h.store.find("s-000001")
	if !found {
		t.Fatal("the failed attempt must be persisted")
	}
	attempt, _ := stored.Current()
	if attempt.HasIdentity() {
		t.Fatal("an unverifiable identity was recorded")
	}
	if attempt.Lifecycle != session.LifecycleFailed {
		t.Fatalf("lifecycle = %s, want failed", attempt.Lifecycle)
	}
}

func TestCreateCleansUpTheChildOnlyThroughItsVerifiedIdentity(t *testing.T) {
	h := newHarness(t)
	h.store.failNextCommit(errors.New("disk full"))

	_, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: testWorkspace,
	})
	typed := requireCode(t, err, session.CodeUnknown)
	if !contains(typed.Reason, "reaped") {
		t.Fatalf("reason = %q, must say the confirmed cleanup reaped the child", typed.Reason)
	}
	kills := h.runtime.forcedKills()
	if len(kills) != 1 || kills[0] != testIdentity(4242) {
		t.Fatalf("force kills = %v, want one SIGKILL of the verified identity", kills)
	}
	if types := h.capture.types(); len(types) != 0 {
		t.Fatalf("published events = %v, want none because nothing was committed", types)
	}
	if h.svc.Revision() != 7 {
		t.Fatalf("revision = %d, want the unchanged 7", h.svc.Revision())
	}
	if h.store.count() != 0 {
		t.Fatal("a failed commit must not leave a partial write")
	}
	// The in-memory record stays visible so the operator can see what happened,
	// and the store says plainly that it holds nothing for this session.
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: "s-000001"})
	if err != nil {
		t.Fatalf("Get after the cleanup: %v", err)
	}
	if live.Session.Lifecycle != session.LifecycleFailed {
		t.Fatalf("lifecycle = %s, want failed", live.Session.Lifecycle)
	}
	if !slices.Contains(live.Session.Notes, session.NoteCleanupRequired) {
		t.Fatalf("notes = %v, want the cleanup-required note", live.Session.Notes)
	}
	if live.Persisted {
		t.Fatal("the stored reading claims metadata that was never written")
	}
	if live.Stored.ID != "" {
		t.Fatalf("stored reading = %+v, want no stored record", live.Stored)
	}
}

func TestCreateReportsAnUnconfirmedCleanupHonestly(t *testing.T) {
	h := newHarness(t)
	h.store.failNextCommit(errors.New("disk full"))
	// The forced kill reached the verified group, but the reap was never confirmed,
	// so the child may still be running and the message has to say so.
	h.runtime.kill = killDeliveredOnly()

	_, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: testWorkspace,
	})
	typed := requireCode(t, err, session.CodeUnknown)
	if !contains(typed.Reason, "may still be running") {
		t.Fatalf("reason = %q, must say the child may still be running", typed.Reason)
	}
	if !contains(typed.Hint, "by hand") {
		t.Fatalf("hint = %q, must tell the operator to end the child by hand", typed.Hint)
	}
	if len(h.runtime.forcedKills()) != 1 {
		t.Fatal("the unpersisted child was never force killed")
	}
}

func TestCreateSurfacesAStoreFailureWhileRecordingAFailedLaunch(t *testing.T) {
	h := newHarness(t)
	h.runtime.launchErr = errors.New("fork/exec: no such file or directory")
	h.store.failNextCommit(errors.New("read-only file system"))
	_, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: testWorkspace,
	})
	typed := requireCode(t, err, session.CodeUnknown)
	if typed.Subject != "session store" {
		t.Fatalf("subject = %q, want the store", typed.Subject)
	}
	if signals := h.runtime.signalKinds(); len(signals) != 0 {
		t.Fatalf("signals = %v, want none", signals)
	}
	if types := h.capture.types(); len(types) != 0 {
		t.Fatalf("published events = %v, want none", types)
	}
}

func TestCreateReportsAStoreConflictAsTypedConflict(t *testing.T) {
	h := newHarness(t)
	h.store.failNextCommit(session.NewError(session.CodeConflict, "store",
		"the expected revision is stale", "retry"))
	_, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: testWorkspace,
	})
	requireCode(t, err, session.CodeConflict)
}

func TestCreateDerivesANameWhenNoneWasGiven(t *testing.T) {
	h := newHarness(t)
	created, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: testWorkspace,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Session.Name != "opencode-kestrel" {
		t.Fatalf("derived name = %q, want opencode-kestrel", created.Session.Name)
	}
}

func TestCreateRejectsAnInvalidRequestBeforeTouchingAPort(t *testing.T) {
	h := newHarness(t)
	tests := []struct {
		name string
		req  CreateRequest
		want session.Code
	}{
		{
			name: "invalid agent id",
			req:  CreateRequest{Agent: "OpenCode", WorkspacePath: testWorkspace},
			want: session.CodeInvalidConfiguration,
		},
		{
			name: "blank workspace",
			req:  CreateRequest{Agent: "opencode", WorkspacePath: "  "},
			want: session.CodeInvalidConfiguration,
		},
		{
			name: "unknown workspace source",
			req:  CreateRequest{Agent: "opencode", WorkspacePath: testWorkspace, WorkspaceSource: "telepathy"},
			want: session.CodeInvalidConfiguration,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.svc.Create(context.Background(), tt.req)
			requireCode(t, err, tt.want)
			if len(h.resolver.resolved()) != 0 {
				t.Fatal("an invalid request reached the workspace resolver")
			}
		})
	}
}

func TestCreateUsesTheConfiguredStopGrace(t *testing.T) {
	h := newHarness(t, withGrace(time.Second))
	h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(0)}
	h.createRunning(t)
	if _, err := h.svc.Stop(context.Background(), StopRequest{Ref: "s-000001"}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if len(h.runtime.stops) != 1 {
		t.Fatalf("stop calls = %d, want 1", len(h.runtime.stops))
	}
}

func intPtr(v int) *int { return &v }
