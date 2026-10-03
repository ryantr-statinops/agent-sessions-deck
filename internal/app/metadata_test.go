package app

import (
	"context"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestScanReportsCoreAndNativeCapabilitiesWithoutResolvingAnything(t *testing.T) {
	h := newHarness(t)
	h.registry.providers = append(h.registry.providers,
		&fakeProvider{
			id:      "codex",
			caps:    agent.MustCapabilitiesOf(agent.CapabilityLaunch, agent.CapabilitySessionList, agent.CapabilitySessionLogs),
			command: mustCommand(t, "/usr/local/bin/codex"),
		},
	)

	result, err := h.svc.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Agents) != 2 {
		t.Fatalf("scanned agents = %d, want 2", len(result.Agents))
	}
	if result.Agents[0].ID != "codex" || result.Agents[1].ID != "opencode" {
		t.Fatalf("scan order = %s, %s, want codex then opencode", result.Agents[0].ID, result.Agents[1].ID)
	}
	codex := result.Agents[0]
	if !codex.Launchable || codex.Interactive {
		t.Fatalf("codex capabilities = %+v, want launchable and not interactive", codex)
	}
	if len(codex.Native) != 2 {
		t.Fatalf("codex native capabilities = %v, want the vendor list and logs", codex.Native)
	}
	if h.provider.resolves != 0 {
		t.Fatal("a scan resolved a command")
	}
	if result.Authority != session.AuthorityLive || result.Revision != h.svc.Revision() {
		t.Fatalf("scan authority/revision = %s/%d", result.Authority, result.Revision)
	}
	if result.ObservedAt.Before(baseTime) {
		t.Fatalf("scan observed at %s, want the injected clock", result.ObservedAt)
	}
}

func TestScanCanBeRestrictedToNamedAgents(t *testing.T) {
	h := newHarness(t)
	h.registry.providers = append(h.registry.providers, newFakeProvider(t, "codex", launchBin))

	result, err := h.svc.Scan(context.Background(), ScanRequest{Agents: []agent.ID{"codex"}})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(result.Agents) != 1 || result.Agents[0].ID != "codex" {
		t.Fatalf("scan = %+v, want only codex", result.Agents)
	}
	if _, err := h.svc.Scan(context.Background(), ScanRequest{Agents: []agent.ID{"Codex"}}); err == nil {
		t.Fatal("an invalid agent id must be refused")
	}
}

func TestListPreservesAuthorityRevisionAndObservationTime(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)

	live, err := h.svc.List(context.Background(), ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if live.Snapshot.Authority != session.AuthorityLive {
		t.Fatalf("authority = %s, want live for an owner with the runtime", live.Snapshot.Authority)
	}
	if live.Snapshot.Revision != created.Revision {
		t.Fatalf("revision = %d, want the committed %d", live.Snapshot.Revision, created.Revision)
	}
	if live.Snapshot.ObservedAt.IsZero() {
		t.Fatal("the snapshot carries no observation instant")
	}
	row := live.Snapshot.Sessions[0]
	if row.StoreRevision != created.Revision || !row.ClaimsLiveness() {
		t.Fatalf("live row = %+v, want a revision-stamped liveness claim", row)
	}

	// The same reading downgraded keeps the observations but drops the claim.
	stored := live.Snapshot.AsStored()
	storedRow := stored.Sessions[0]
	if stored.Authority != session.AuthorityStored || storedRow.ClaimsLiveness() {
		t.Fatalf("stored row = %+v, want no liveness claim", storedRow)
	}
	if storedRow.Lifecycle != session.LifecycleRunning || storedRow.ObservedAt != row.ObservedAt {
		t.Fatalf("the downgrade lost the observations: %+v", storedRow)
	}
}

func TestStoredAuthorityOwnerNeverClaimsLiveness(t *testing.T) {
	h := newHarness(t, withAuthority(session.AuthorityStored))
	h.createRunning(t)

	result, err := h.svc.List(context.Background(), ListRequest{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if result.Snapshot.Authority != session.AuthorityStored {
		t.Fatalf("authority = %s, want stored", result.Snapshot.Authority)
	}
	if result.Snapshot.Sessions[0].ClaimsLiveness() {
		t.Fatal("a stored reader claimed liveness")
	}
	if err := result.Snapshot.Validate(); err != nil {
		t.Fatalf("the snapshot is not internally consistent: %v", err)
	}
}

func TestListFiltersCombineWithoutInferringState(t *testing.T) {
	h := newHarness(t)
	h.createRunning(t)
	if _, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: secondWork,
		Name:          "observability",
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	h.runtime.identity = testIdentity(5150)
	if _, err := h.svc.Kill(context.Background(), KillRequest{Ref: "s-000002"}); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	tests := []struct {
		name string
		req  ListRequest
		want []session.ID
	}{
		{name: "no filter", req: ListRequest{}, want: []session.ID{"s-000001", "s-000002"}},
		{name: "lifecycle", req: ListRequest{Lifecycles: []session.Lifecycle{session.LifecycleRunning}}, want: []session.ID{"s-000001"}},
		{name: "agent", req: ListRequest{AgentID: "opencode"}, want: []session.ID{"s-000001", "s-000002"}},
		{name: "workspace", req: ListRequest{Workspace: workspace.ID(secondWork)}, want: []session.ID{"s-000002"}},
		{name: "combined", req: ListRequest{
			AgentID:    "opencode",
			Workspace:  workspace.ID(testWorkspace),
			Lifecycles: []session.Lifecycle{session.LifecycleRunning},
		}, want: []session.ID{"s-000001"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := h.svc.List(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			got := make([]session.ID, 0, len(result.Snapshot.Sessions))
			for _, row := range result.Snapshot.Sessions {
				got = append(got, row.ID)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("rows = %v, want %v", got, tt.want)
			}
			for i, id := range tt.want {
				if got[i] != id {
					t.Fatalf("rows = %v, want %v", got, tt.want)
				}
			}
		})
	}

	empty, err := h.svc.List(context.Background(), ListRequest{Lifecycles: []session.Lifecycle{session.LifecycleFailed}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if empty.Snapshot.Sessions == nil || len(empty.Snapshot.Sessions) != 0 {
		t.Fatalf("an empty list = %v, want an empty non-nil list", empty.Snapshot.Sessions)
	}
}

func TestListRejectsUnknownFilterValues(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.List(context.Background(), ListRequest{Lifecycles: []session.Lifecycle{"idle"}})
	typed := requireCode(t, err, session.CodeInvalidConfiguration)
	if !contains(typed.Hint, "running") {
		t.Fatalf("hint = %q, must name the defined lifecycle values", typed.Hint)
	}
}

func TestGetReturnsBothTheLiveAndTheStoredReading(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)

	result, err := h.svc.Get(context.Background(), GetRequest{Ref: "s-0000"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if result.Session.Authority != session.AuthorityLive || !result.Session.ClaimsLiveness() {
		t.Fatalf("live reading = %+v, want a live claim", result.Session)
	}
	if result.Stored.Authority != session.AuthorityStored || result.Stored.ClaimsLiveness() {
		t.Fatalf("stored reading = %+v, want no liveness claim", result.Stored)
	}
	if result.Session.ID != created.Session.ID || result.Revision != created.Revision {
		t.Fatalf("Get = %+v at revision %d", result.Session, result.Revision)
	}
	if result.ObservedAt.IsZero() {
		t.Fatal("Get carries no observation instant")
	}
}

func TestGetRefusesAnUnknownReference(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.Get(context.Background(), GetRequest{Ref: "s-999999"})
	typed := requireCode(t, err, session.CodeNotFound)
	if typed.Hint == "" {
		t.Fatal("the refusal carries no hint")
	}
	_, err = h.svc.Get(context.Background(), GetRequest{})
	requireCode(t, err, session.CodeInvalidConfiguration)
}

func TestRenameKeepsIdentityAndAttemptHistory(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	before, found := h.store.find(created.Session.ID)
	if !found {
		t.Fatal("the created session was not persisted")
	}

	renamed, err := h.svc.Rename(context.Background(), RenameRequest{
		Ref:  string(created.Session.ID),
		Name: "kestrel-review",
	})
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if renamed.Session.ID != created.Session.ID {
		t.Fatalf("rename changed the id: %q -> %q", created.Session.ID, renamed.Session.ID)
	}
	if renamed.Session.Generation != created.Session.Generation {
		t.Fatalf("rename changed the generation: %d -> %d", created.Session.Generation, renamed.Session.Generation)
	}
	if renamed.Session.Name != "kestrel-review" {
		t.Fatalf("name = %q, want kestrel-review", renamed.Session.Name)
	}
	if renamed.Session.Lifecycle != session.LifecycleRunning || renamed.Session.Attachment != created.Session.Attachment {
		t.Fatalf("rename changed the observable triple: %+v", renamed.Session)
	}
	after, _ := h.store.find(created.Session.ID)
	if after.AttemptCount() != before.AttemptCount() {
		t.Fatalf("rename changed the attempt count: %d -> %d", before.AttemptCount(), after.AttemptCount())
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated-at did not move: %s -> %s", before.UpdatedAt, after.UpdatedAt)
	}
	if types := h.capture.types(); types[len(types)-1] != string(TypeSessionRenamed) {
		t.Fatalf("published events = %v, want the rename last", types)
	}
}

func TestRenameRefusesAnUnusableNameAndLeavesTheRecord(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	tests := []struct {
		name string
		want string
	}{
		{name: " padded", want: " kestrel "},
		{name: " control character", want: "kestrel\x07"},
		{name: " too long", want: longName(200)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.svc.Rename(context.Background(), RenameRequest{
				Ref:  string(created.Session.ID),
				Name: tt.want,
			})
			requireCode(t, err, session.CodeInvalidConfiguration)
		})
	}
	live, err := h.svc.Get(context.Background(), GetRequest{Ref: string(created.Session.ID)})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if live.Session.Name != "kestrel" {
		t.Fatalf("name = %q, want the untouched kestrel", live.Session.Name)
	}
}

func longName(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'x'
	}
	return string(out)
}

func TestNativeCapabilitiesReportTypedUnsupported(t *testing.T) {
	h := newHarness(t)
	native := []agent.Capability{
		agent.CapabilitySessionList,
		agent.CapabilitySessionLogs,
		agent.CapabilityResume,
		agent.CapabilityKill,
	}
	for _, capability := range native {
		t.Run(capability.String(), func(t *testing.T) {
			err := h.svc.RequireCapability("opencode", capability)
			typed := requireCode(t, err, session.CodeUnsupported)
			if typed.Subject != "opencode" {
				t.Fatalf("subject = %q, want the agent id", typed.Subject)
			}
			if !contains(typed.Reason, string(capability)) {
				t.Fatalf("reason = %q, must name the operation", typed.Reason)
			}
			if !contains(typed.Reason, "native") {
				t.Fatalf("reason = %q, must say it is a native vendor capability", typed.Reason)
			}
			if typed.Hint == "" {
				t.Fatal("the refusal carries no hint")
			}
		})
	}
}

func TestRequireCapabilityAcceptsADeclaredCoreCapability(t *testing.T) {
	h := newHarness(t)
	if err := h.svc.RequireCapability("opencode", agent.CapabilityLaunch); err != nil {
		t.Fatalf("a declared core capability must be accepted: %v", err)
	}
	if err := h.svc.RequireCapability("unknown-agent", agent.CapabilityLaunch); err == nil {
		t.Fatal("an unknown agent must be refused")
	} else {
		requireCode(t, err, session.CodeNotFound)
	}
}

func TestServiceRefusesAnIncompletePortSet(t *testing.T) {
	h := newHarness(t)
	deps := Deps{
		Registry:   h.registry,
		Workspaces: h.resolver,
		Store:      h.store,
		Runtime:    h.runtime,
		Leases:     h.broker,
		Terminals:  h.terminal,
		Events:     h.capture,
		Clock:      h.clock,
	}
	_, err := NewService(context.Background(), deps)
	typed := requireCode(t, err, session.CodeInvalidConfiguration)
	if !contains(typed.Reason, "id minter") {
		t.Fatalf("reason = %q, must name the missing port", typed.Reason)
	}
}

func TestServiceRefusesAnInvalidConfiguration(t *testing.T) {
	h := newHarness(t)
	deps := Deps{
		Registry:   h.registry,
		Workspaces: h.resolver,
		Store:      h.store,
		Runtime:    h.runtime,
		Leases:     h.broker,
		Terminals:  h.terminal,
		Events:     h.capture,
		Clock:      h.clock,
		IDs:        newSeqIDs(),
	}
	tests := []struct {
		name string
		opts []Option
	}{
		{name: "bad authority", opts: []Option{WithAuthority("guesswork")}},
		{name: "bad stop window", opts: []Option{WithStopGrace(0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewService(context.Background(), deps, tt.opts...)
			requireCode(t, err, session.CodeInvalidConfiguration)
		})
	}
}

func TestServiceRefusesToStartOverCorruptStoredState(t *testing.T) {
	h := newHarness(t)
	// A stored record that no longer validates blocks the owner rather than being
	// silently repaired.
	broken := session.Session{ID: "s-broken", Name: "broken", AgentID: "opencode", WorkspaceID: workspace.ID(testWorkspace)}
	broken.Attempts = []session.Attempt{session.NewAttempt(1, baseTime)}
	broken.Generation = 1
	h.store.sessions = []session.Session{broken}

	deps := Deps{
		Registry:   h.registry,
		Workspaces: h.resolver,
		Store:      h.store,
		Runtime:    h.runtime,
		Leases:     h.broker,
		Terminals:  h.terminal,
		Events:     h.capture,
		Clock:      h.clock,
		IDs:        newSeqIDs(),
	}
	_, err := NewService(context.Background(), deps)
	typed := requireCode(t, err, session.CodeCorruptState)
	if !contains(typed.Hint, "repair") {
		t.Fatalf("hint = %q, must tell the operator to repair the record", typed.Hint)
	}
}

func TestScanAndListNeedNoClockOfTheirOwn(t *testing.T) {
	h := newHarness(t)
	// Both are pure reads: no commit, no event, no revision change.
	before := h.svc.Revision()
	if _, err := h.svc.Scan(context.Background(), ScanRequest{}); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if _, err := h.svc.List(context.Background(), ListRequest{}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if h.svc.Revision() != before {
		t.Fatalf("revision moved from %d to %d on a read", before, h.svc.Revision())
	}
	if types := h.capture.types(); len(types) != 0 {
		t.Fatalf("reads published events: %v", types)
	}
}

func TestStopTimeoutCarriesTheConfiguredWindow(t *testing.T) {
	h := newHarness(t, withGrace(2*time.Second))
	h.createRunning(t)
	if _, err := h.svc.Stop(context.Background(), StopRequest{Ref: "s-000001"}); err == nil {
		t.Fatal("the scripted runtime never exits, so the stop must time out")
	}
}

func TestAgentSummarySplitsCoreFromNativeCapabilities(t *testing.T) {
	h := newHarness(t)
	h.provider.caps = agent.MustCapabilitiesOf(
		agent.CapabilityLaunch, agent.CapabilityInteractive,
		agent.CapabilitySessionLogs, agent.CapabilityResume,
	)
	result, err := h.svc.Scan(context.Background(), ScanRequest{})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	summary := result.Agents[0]
	if got := summary.Core(); len(got) != 2 {
		t.Fatalf("core capabilities = %v, want launch and interactive", got)
	}
	if got := summary.Native; len(got) != 2 {
		t.Fatalf("native capabilities = %v, want logs and resume", got)
	}
	if !summary.Launchable || !summary.Interactive {
		t.Fatalf("summary = %+v, want both core flags set", summary)
	}
}

func TestServiceReportsItsOwnAuthority(t *testing.T) {
	live := newHarness(t)
	if live.svc.Authority() != session.AuthorityLive {
		t.Fatalf("authority = %s, want live by default", live.svc.Authority())
	}
	stored := newHarness(t, withAuthority(session.AuthorityStored))
	if stored.svc.Authority() != session.AuthorityStored {
		t.Fatalf("authority = %s, want stored", stored.svc.Authority())
	}
}

func TestRequireProviderRefusesAnUndefinedCapability(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.RequireProvider("opencode", agent.Capability("telemetry"))
	typed := requireCode(t, err, session.CodeInvalidConfiguration)
	if !contains(typed.Hint, "launch") {
		t.Fatalf("hint = %q, must name the defined capabilities", typed.Hint)
	}
	if _, err := h.svc.RequireProvider("OpenCode", agent.CapabilityLaunch); err == nil {
		t.Fatal("an invalid agent id must be refused")
	}
}

func TestDepsValidateNamesEveryMissingPort(t *testing.T) {
	err := Deps{}.validate()
	typed := requireCode(t, err, session.CodeInvalidConfiguration)
	for _, want := range []string{
		"provider registry", "workspace resolver", "session store",
		"process runtime", "lease broker", "terminal source",
		"event publisher", "clock", "id minter",
	} {
		if !contains(typed.Reason, want) {
			t.Fatalf("reason = %q, must name the missing %q port", typed.Reason, want)
		}
	}
}
