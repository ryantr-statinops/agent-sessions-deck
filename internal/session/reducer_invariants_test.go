package session

import (
	"errors"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// TestRestartKeepsSessionIDAndBumpsGeneration walks the full restart of a live
// attempt: the reducer stops the live attempt, the exit closes it, and the second
// restart request opens a new attempt. The session identity never changes and
// there is never more than one live attempt.
func TestRestartKeepsSessionIDAndBumpsGeneration(t *testing.T) {
	running := runningSession(t)
	originalID := running.ID
	first, ok := running.Current()
	if !ok {
		t.Fatalf("running fixture has no attempt")
	}

	forced := Reduce(running, Event{
		Kind:        EventRestartRequested,
		Generation:  1,
		At:          at(60),
		Forced:      true,
		Command:     launchCommand(t),
		WorkspaceID: testWorkspace,
	})
	mustApply(t, forced)
	if forced.Row != RowRestartLive {
		t.Fatalf("row = %q, want %q", forced.Row, RowRestartLive)
	}
	if forced.Session.State().Lifecycle != LifecycleStopping {
		t.Fatalf("lifecycle after forced restart = %q, want stopping", forced.Session.State().Lifecycle)
	}

	stopped := Reduce(forced.Session, Event{
		Kind:       EventChildExited,
		Generation: 1,
		At:         at(65),
		Reason:     Stopped(0),
	})
	mustApply(t, stopped)

	restarted := Reduce(stopped.Session, Event{
		Kind:        EventRestartRequested,
		Generation:  1,
		At:          at(70),
		Command:     mustCommand(t, "/usr/local/bin/opencode", "--resume-less"),
		WorkspaceID: testWorkspace,
	})
	mustApply(t, restarted)
	if restarted.Row != RowRestartTerminal {
		t.Fatalf("row = %q, want %q", restarted.Row, RowRestartTerminal)
	}
	if restarted.Session.ID != originalID {
		t.Fatalf("restart changed the session id: %q -> %q", originalID, restarted.Session.ID)
	}
	if restarted.Session.Generation != 2 {
		t.Fatalf("generation = %d, want 2", restarted.Session.Generation)
	}
	if got := restarted.Session.AttemptCount(); got != 2 {
		t.Fatalf("attempt count = %d, want 2", got)
	}
	if err := restarted.Session.Validate(); err != nil {
		t.Fatalf("session with two attempts is invalid: %v", err)
	}

	current, ok := restarted.Session.Current()
	if !ok {
		t.Fatalf("restarted session has no current attempt")
	}
	if current.Lifecycle != LifecycleStarting {
		t.Fatalf("new attempt lifecycle = %q, want starting", current.Lifecycle)
	}
	if current.Generation != 2 {
		t.Fatalf("new attempt generation = %d, want 2", current.Generation)
	}

	// The old attempt is immutable history.
	previous, ok := restarted.Session.FindAttempt(1)
	if !ok {
		t.Fatalf("attempt 1 is missing from the history")
	}
	if previous.Reason.Kind != ReasonStopped {
		t.Fatalf("previous attempt reason = %q, want stopped", previous.Reason.Kind)
	}
	if previous.Identity != first.Identity {
		t.Fatalf("previous attempt identity changed: %s -> %s", first.Identity.Describe(), previous.Identity.Describe())
	}
	if !previous.StartedAt.Equal(first.StartedAt) || !previous.RunningAt.Equal(first.RunningAt) {
		t.Fatalf("previous attempt timestamps changed: %+v -> %+v", first, previous)
	}

	// Exactly one attempt is ever live.
	live := 0
	for _, attempt := range restarted.Session.Attempts {
		if !attempt.Terminal() {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("%d attempts are live, want exactly 1", live)
	}
}

// TestRestartAfterFailedLaunchOpensTheNextGeneration proves a failed validation
// occupies generation 1 and that the retry is a restart, not a second launch.
func TestRestartAfterFailedLaunchOpensTheNextGeneration(t *testing.T) {
	failed := failedSession(t)
	if failed.Generation != 1 {
		t.Fatalf("generation after a failed launch = %d, want 1", failed.Generation)
	}
	current, _ := failed.Current()
	if current.HasCommand() {
		t.Fatalf("a failed launch must not freeze a command")
	}

	outcome := Reduce(failed, Event{
		Kind:        EventRestartRequested,
		Generation:  1,
		At:          at(120),
		Command:     launchCommand(t),
		WorkspaceID: testWorkspace,
	})
	mustApply(t, outcome)
	if outcome.Session.Generation != 2 {
		t.Fatalf("generation = %d, want 2", outcome.Session.Generation)
	}
	if outcome.Session.ID != failed.ID {
		t.Fatalf("restart changed the session id")
	}
	first, _ := outcome.Session.FindAttempt(1)
	if first.Reason.Kind != ReasonLaunchFailed || first.Reason.Detail != "agent binary not found" {
		t.Fatalf("attempt 1 reason = %q, want the original launch failure", first.Reason.String())
	}
}

// TestStaleCallbackCannotTouchTheCurrentAttempt is the T19 acceptance: a callback
// from an older generation changes nothing.
func TestStaleCallbackCannotTouchTheCurrentAttempt(t *testing.T) {
	restarted := restartToRunning(t, exitedSession(t), 2)

	stale := Reduce(restarted, Event{
		Kind:       EventChildExited,
		Generation: 1,
		At:         at(200),
		Reason:     NaturalExit(0),
	})
	if stale.Applied {
		t.Fatalf("a stale callback was applied")
	}
	if got := stale.Rejection.Code(); got != CodeStaleAttempt {
		t.Fatalf("rejection code = %s, want %s", got, CodeStaleAttempt)
	}
	if !stale.HasEffect(EffectDropStaleCallback) {
		t.Fatalf("a dropped stale callback must report the drop effect")
	}
	current, _ := stale.Session.Current()
	if current.Generation != 2 || current.Lifecycle != LifecycleRunning {
		t.Fatalf("current attempt changed: generation %d lifecycle %q", current.Generation, current.Lifecycle)
	}
	if stale.Session.Generation != restarted.Generation {
		t.Fatalf("session generation changed: %d -> %d", restarted.Generation, stale.Session.Generation)
	}

	for _, event := range []Event{
		{Kind: EventStopRequested, Generation: 1, At: at(201)},
		{Kind: EventKillRequested, Generation: 1, At: at(202)},
		{Kind: EventStopTimeout, Generation: 1, At: at(203)},
		{Kind: EventSpawned, Generation: 1, At: at(204), Identity: testIdentity},
		{Kind: EventAttached, Generation: 1, At: at(205)},
	} {
		outcome := Reduce(restarted, event)
		if outcome.Applied {
			t.Fatalf("stale %s was applied to generation %d", event.Kind, event.Generation)
		}
		if got := outcome.Rejection.Code(); got != CodeStaleAttempt {
			t.Fatalf("stale %s rejection code = %s, want %s", event.Kind, got, CodeStaleAttempt)
		}
	}
}

// TestStopTimeoutReturnsRunningAndNeverMeansKilled separates a stop timeout from
// a kill and from an exit.
func TestStopTimeoutReturnsRunningAndNeverMeansKilled(t *testing.T) {
	outcome := Reduce(stoppingSession(t), Event{
		Kind:       EventStopTimeout,
		Generation: 1,
		At:         at(40),
	})
	mustApply(t, outcome)

	current, _ := outcome.Session.Current()
	if current.Lifecycle != LifecycleRunning {
		t.Fatalf("lifecycle = %q, want running after a stop timeout", current.Lifecycle)
	}
	if !current.Reason.Zero() {
		t.Fatalf("a stop timeout must not record an exit reason, got %q", current.Reason.String())
	}
	if current.HasExitCode {
		t.Fatalf("a stop timeout must not record an exit code")
	}
	if !current.HasNote(NoteStopTimeout) || !current.HasNote(NoteKillGuidance) {
		t.Fatalf("notes = %v, want the stop timeout and kill guidance", current.Notes())
	}
	if len(outcome.Effects) != 0 {
		t.Fatalf("a stop timeout must not escalate to a signal: %v", outcome.Effects)
	}

	killed := Reduce(outcome.Session, Event{
		Kind:       EventKillRequested,
		Generation: 1,
		At:         at(50),
		Reason:     Killed("SIGKILL"),
	})
	mustApply(t, killed)
	current, _ = killed.Session.Current()
	if current.Reason.Kind != ReasonKilled {
		t.Fatalf("after an explicit kill the reason is %q, want killed", current.Reason.Kind)
	}
	if !current.HasNote(NoteStopTimeout) {
		t.Fatalf("the stop timeout note must survive as history: %v", current.Notes())
	}
}

// TestNaturalNonzeroExitDiffersFromLaunchFailure keeps the three failure shapes
// distinguishable, which is what the CLI contract requires.
func TestNaturalNonzeroExitDiffersFromLaunchFailure(t *testing.T) {
	natural := Reduce(runningSession(t), Event{
		Kind:       EventChildExited,
		Generation: 1,
		At:         at(30),
		Reason:     NaturalExit(137),
	})
	mustApply(t, natural)
	naturalAttempt, _ := natural.Session.Current()
	if naturalAttempt.Lifecycle != LifecycleExited {
		t.Fatalf("a nonzero exit must be exited, got %q", naturalAttempt.Lifecycle)
	}
	if naturalAttempt.Reason.Kind != ReasonNaturalExit || naturalAttempt.ExitCode != 137 {
		t.Fatalf("reason = %q exit code = %d, want natural-exit 137", naturalAttempt.Reason.String(), naturalAttempt.ExitCode)
	}

	spawnFailed := Reduce(startingSession(t), Event{
		Kind:       EventSpawnFailed,
		Generation: 1,
		At:         at(30),
		Failure:    "exec format error",
	})
	mustApply(t, spawnFailed)
	spawnAttempt, _ := spawnFailed.Session.Current()
	if spawnAttempt.Lifecycle != LifecycleFailed || spawnAttempt.Reason.Kind != ReasonLaunchFailed {
		t.Fatalf("spawn failure = %q/%q, want failed/launch-failed", spawnAttempt.Lifecycle, spawnAttempt.Reason.Kind)
	}
	if spawnAttempt.HasExitCode {
		t.Fatalf("a launch failure must not carry an exit code")
	}

	launchFailed := failedSession(t)
	launchAttempt, _ := launchFailed.Current()
	if launchAttempt.Lifecycle != LifecycleFailed || launchAttempt.Reason.Kind != ReasonLaunchFailed {
		t.Fatalf("launch validation failure = %q/%q, want failed/launch-failed", launchAttempt.Lifecycle, launchAttempt.Reason.Kind)
	}
	if launchAttempt.HasIdentity() {
		t.Fatalf("a launch failure must not leave a process identity behind")
	}

	for _, reason := range []Reason{naturalAttempt.Reason, spawnAttempt.Reason, launchAttempt.Reason} {
		if reason.Kind == ReasonNaturalExit && reason.Kind.Failure() {
			t.Fatalf("natural exit must not classify as a failure")
		}
	}
}

// TestUnknownIsStickyAndNeverDecaysToExited is the acceptance for invariant 3.
func TestUnknownIsStickyAndNeverDecaysToExited(t *testing.T) {
	session := unknownSession(t)
	if session.State().Lifecycle != LifecycleUnknown {
		t.Fatalf("lifecycle = %q, want unknown", session.State().Lifecycle)
	}

	for _, seconds := range []int{210, 220, 230} {
		outcome := Reduce(session, Event{
			Kind:       EventProbeInconclusive,
			Generation: 1,
			At:         at(seconds),
			Liveness: LivenessObservation{
				Outcome: ProbeUnverifiable,
				Detail:  "probe-timeout",
				At:      at(seconds),
			},
		})
		mustApply(t, outcome)
		if got := outcome.Session.State().Lifecycle; got != LifecycleUnknown {
			t.Fatalf("lifecycle after probe %d = %q, want unknown", seconds, got)
		}
		if outcome.Session.State().Lifecycle.Terminal() {
			t.Fatalf("an inconclusive probe produced a terminal lifecycle")
		}
		session = outcome.Session
	}

	// A routine graceful stop is refused while liveness is unknown, because that
	// is exactly the state in which a pid cannot be proven to be the recorded one.
	stop := Reduce(session, Event{Kind: EventStopRequested, Generation: 1, At: at(240)})
	if stop.Applied {
		t.Fatalf("stop was applied to an unknown lifecycle")
	}
	if got := stop.Rejection.Code(); got != CodeConflict {
		t.Fatalf("stop rejection code = %s, want %s", got, CodeConflict)
	}

	// An explicit kill stays available, because it is the destructive action that
	// resolves an unverifiable attempt; the runtime must re-verify the identity
	// before it signals.
	killed := Reduce(session, Event{
		Kind:       EventKillRequested,
		Generation: 1,
		At:         at(241),
		Reason:     Killed("SIGKILL"),
	})
	mustApply(t, killed)
	if !killed.HasEffect(EffectSignalOwnedGroup) {
		t.Fatalf("an explicit kill must remain available for an unverifiable attempt")
	}
	if effect := killed.Effects[0]; effect.Identity != testIdentity {
		t.Fatalf("kill effect identity = %s, want the recorded identity", effect.Identity.Describe())
	}
	if session.Orphaned() {
		t.Fatalf("an unknown attempt is not an orphan: orphaned is running plus unavailable")
	}

	// Only real evidence closes an attempt.
	gone := Reduce(session, Event{
		Kind:       EventReconciled,
		Generation: 1,
		At:         at(250),
		Liveness:   LivenessObservation{Outcome: ProbeGone, At: at(250)},
	})
	mustApply(t, gone)
	current, _ := gone.Session.Current()
	if current.Lifecycle != LifecycleExited || current.Reason.Kind != ReasonProcessGone {
		t.Fatalf("gone evidence = %q/%q, want exited/dead", current.Lifecycle, current.Reason.Kind)
	}
	if current.Reason.Detail == "" {
		t.Fatalf("reconciliation evidence must be recorded with the reason")
	}
}

// TestReconciliationClassifiesPersistedRunningExactlyOnce is invariant 4.
func TestReconciliationClassifiesPersistedRunningExactlyOnce(t *testing.T) {
	cases := []struct {
		name        string
		observation LivenessObservation
		wantRow     string
		wantState   State
		wantReason  ReasonKind
	}{
		{
			name:        "process gone closes the observation",
			observation: LivenessObservation{Outcome: ProbeGone, At: at(300)},
			wantRow:     RowProcessGone,
			wantState:   State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonProcessGone,
		},
		{
			name:        "pid present with another identity closes stale and is not adopted",
			observation: LivenessObservation{Outcome: ProbeAlive, Identity: foreignIdentity, At: at(300)},
			wantRow:     RowIdentityMismatch,
			wantState:   State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonStaleIdentity,
		},
		{
			name:        "unverifiable evidence keeps the attempt unknown",
			observation: LivenessObservation{Outcome: ProbeUnverifiable, Detail: "probe-timeout", At: at(300)},
			wantRow:     RowProbeInconclusive,
			wantState:   State{Lifecycle: LifecycleUnknown, Attachment: AttachmentDetached, Activity: ActivityUnknown},
			wantReason:  ReasonProbeTimeout,
		},
		{
			name:        "verified evidence resolves a sticky unknown",
			observation: LivenessObservation{Outcome: ProbeAlive, Identity: testIdentity, At: at(300)},
			wantRow:     "",
			wantState:   State{Lifecycle: LifecycleRunning, Attachment: AttachmentDetached, Activity: ActivityUnknown},
			wantReason:  "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			from := runningSession(t)
			if tc.wantState.Lifecycle == LifecycleUnknown {
				from = unknownSession(t)
			}
			outcome := Reduce(from, Event{
				Kind:       EventReconciled,
				Generation: 1,
				At:         at(300),
				Liveness:   tc.observation,
			})
			mustApply(t, outcome)
			if outcome.Row != tc.wantRow {
				t.Fatalf("row = %q, want %q", outcome.Row, tc.wantRow)
			}
			current, _ := outcome.Session.Current()
			if current.State() != tc.wantState {
				t.Fatalf("state = %+v, want %+v", current.State(), tc.wantState)
			}
			if current.Reason.Kind != tc.wantReason {
				t.Fatalf("reason = %q, want %q", current.Reason.Kind, tc.wantReason)
			}
			if len(outcome.Effects) != 0 {
				t.Fatalf("reconciliation must never signal or launch: %v", outcome.Effects)
			}
		})
	}
}

// TestOrphanIsAnAnnotationNotALifecycleState covers T14: running plus I/O
// unavailable keeps the process alive and untouched, and opening fails honestly.
func TestOrphanIsAnAnnotationNotALifecycleState(t *testing.T) {
	outcome := Reduce(runningSession(t), Event{
		Kind:       EventPTYLost,
		Generation: 1,
		At:         at(20),
		Liveness: LivenessObservation{
			Outcome:  ProbeAlive,
			Identity: testIdentity,
			At:       at(20),
		},
	})
	mustApply(t, outcome)

	if got := outcome.Session.State().Lifecycle; got != LifecycleRunning {
		t.Fatalf("lifecycle = %q, want running for an orphan", got)
	}
	if !outcome.Session.Orphaned() {
		t.Fatalf("running plus unavailable must be annotated orphaned")
	}
	if len(outcome.Effects) != 0 {
		t.Fatalf("a PTY loss must not signal the process: %v", outcome.Effects)
	}
	current, _ := outcome.Session.Current()
	if !current.HasIdentity() || !current.HasNote(NoteOrphaned) {
		t.Fatalf("the original observations must be preserved beside the annotation")
	}

	open := Reduce(outcome.Session, Event{Kind: EventAttached, Generation: 1, At: at(21)})
	if open.Applied {
		t.Fatalf("attaching to an orphan must fail")
	}
	if got := open.Rejection.Code(); got != CodeSessionIOFailed {
		t.Fatalf("rejection code = %s, want %s", got, CodeSessionIOFailed)
	}

	// The process is still reachable through an explicit verified action.
	killed := Reduce(outcome.Session, Event{
		Kind:       EventKillRequested,
		Generation: 1,
		At:         at(22),
		Reason:     Killed("SIGKILL"),
	})
	mustApply(t, killed)
	if !killed.HasEffect(EffectSignalOwnedGroup) {
		t.Fatalf("an explicit kill must still work on a verified orphan")
	}
	if err := outcome.Session.EnsureDeletable(); err == nil {
		t.Fatalf("an orphan must not be deletable while its process may run")
	}
	if err := killed.Session.EnsureDeletable(); err != nil {
		t.Fatalf("an explicitly killed orphan becomes deletable: %v", err)
	}
}

// TestDeleteMetadataRequiresATerminalLifecycle is the T20 acceptance.
func TestDeleteMetadataRequiresATerminalLifecycle(t *testing.T) {
	active := map[string]Session{
		"created":  freshSession(t),
		"starting": startingSession(t),
		"running":  runningSession(t),
		"stopping": stoppingSession(t),
		"unknown":  unknownSession(t),
		"orphaned": orphanSession(t),
	}
	for name, session := range active {
		err := session.EnsureDeletable()
		if err == nil {
			t.Fatalf("%s session was deletable while active", name)
		}
		if got := CodeOf(err); got != CodeConflict {
			t.Fatalf("%s session rejection code = %s, want %s", name, got, CodeConflict)
		}
		var typed *Error
		if !errors.As(err, &typed) || typed.Hint == "" {
			t.Fatalf("%s session rejection has no typed hint: %v", name, err)
		}
	}

	for name, session := range map[string]Session{
		"exited": exitedSession(t),
		"failed": failedSession(t),
	} {
		if err := session.EnsureDeletable(); err != nil {
			t.Fatalf("%s session was not deletable: %v", name, err)
		}
	}
}

// TestActivityRequiresProviderEvidence keeps silence from becoming idleness.
func TestActivityRequiresProviderEvidence(t *testing.T) {
	session := runningSession(t)
	unevidenced := session.SetActivity(ActivityIdle, Evidence{})
	if unevidenced == nil {
		t.Fatalf("silence was accepted as evidence of idleness")
	}
	if got := CodeOf(unevidenced); got != CodeUnsupported {
		t.Fatalf("unevidenced activity code = %s, want %s", got, CodeUnsupported)
	}

	evidence := Evidence{Source: "opencode-adapter", Detail: "status line reports idle", At: at(30)}
	if err := session.SetActivity(ActivityIdle, evidence); err != nil {
		t.Fatalf("evidenced activity was refused: %v", err)
	}
	current, _ := session.Current()
	if current.Activity != ActivityIdle {
		t.Fatalf("activity = %q, want idle", current.Activity)
	}

	snapshot, err := NewSnapshot(AuthorityLive, at(31), 7, []Session{session})
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	row, ok := snapshot.Find(session.ID)
	if !ok {
		t.Fatalf("snapshot is missing the session")
	}
	if !row.ActivityEvidenced || row.Activity != ActivityIdle {
		t.Fatalf("snapshot row lost the activity evidence: %+v", row)
	}
	if !row.ClaimsLiveness() {
		t.Fatalf("a live running row must claim liveness")
	}
}

// TestStoredCommandsAndNotesAreImmutableCopies proves a caller cannot mutate
// stored state through the slices it supplied or received.
func TestStoredCommandsAndNotesAreImmutableCopies(t *testing.T) {
	args := []string{"--first", "--second"}
	command, err := agent.NewCommand("/usr/local/bin/opencode", args)
	if err != nil {
		t.Fatalf("NewCommand: %v", err)
	}
	args[0] = "--mutated"
	if got := command.Args()[0]; got != "--first" {
		t.Fatalf("command args aliased the caller slice: %q", got)
	}

	session := freshSession(t)
	outcome := Reduce(session, Event{
		Kind:        EventLaunchRequested,
		At:          at(0),
		Command:     command,
		WorkspaceID: testWorkspace,
	})
	mustApply(t, outcome)

	stored := outcome.Session
	current, _ := stored.Current()
	returned := current.Command.Args()
	returned[0] = "--tampered"
	storedCurrent, _ := stored.Current()
	if got := storedCurrent.Command.Args()[0]; got != "--first" {
		t.Fatalf("stored command args were mutated through an accessor copy: %q", got)
	}

	notes := current.Notes()
	for i := range notes {
		notes[i] = "tampered"
	}
	storedCurrent, _ = stored.Current()
	if got := storedCurrent.Notes(); len(got) != 0 {
		t.Fatalf("expected no notes on a starting attempt, got %v", got)
	}

	history := stored.AttemptHistory()
	if len(history) != 1 {
		t.Fatalf("attempt history = %d, want 1", len(history))
	}
	history[0].Command = agent.Command{}
	again := stored.AttemptHistory()
	if !again[0].Command.Equal(command) {
		t.Fatalf("attempt history was mutated through a copy")
	}
}

// TestLaunchRequestValidationRefusesRelativeExecutables keeps the launch contract
// in one place.
func TestLaunchRequestValidationRefusesRelativeExecutables(t *testing.T) {
	request := LaunchRequest{
		SessionID:  testSessionID,
		Generation: 1,
		Command:    mustCommand(t, "opencode"),
		Workspace:  mustWorkspace(t, testWorkspace.Path()),
	}
	if err := request.Validate(); err == nil {
		t.Fatalf("a relative executable was accepted for launch")
	} else if got := CodeOf(err); got != CodeLaunchFailed {
		t.Fatalf("code = %s, want %s", got, CodeLaunchFailed)
	}

	request.Command = launchCommand(t)
	if err := request.Validate(); err != nil {
		t.Fatalf("an absolute executable was refused: %v", err)
	}
}

// TestReduceLeavesItsInputUntouched proves purity across a whole sequence.
func TestReduceLeavesItsInputUntouched(t *testing.T) {
	before := runningSession(t)
	original := before.Clone()

	events := []Event{
		{Kind: EventStopRequested, Generation: 1, At: at(20)},
		{Kind: EventStopTimeout, Generation: 1, At: at(40)},
		{Kind: EventStopRequested, Generation: 1, At: at(45)},
		{Kind: EventKillRequested, Generation: 1, At: at(50), Reason: Killed("SIGKILL")},
		{Kind: EventChildExited, Generation: 1, At: at(55), Reason: Stopped(0)},
	}
	for _, event := range events {
		Reduce(before, event)
	}
	if err := compareSessions(t, before, original); err != nil {
		t.Fatalf("Reduce mutated its input session: %v", err)
	}
}

// TestApplyAllStopsAtTheFirstRejection keeps a scripted sequence from applying
// events after a refusal.
func TestApplyAllStopsAtTheFirstRejection(t *testing.T) {
	// A kill closes the attempt, so the exit the single waiter reports afterwards
	// is refused instead of overwriting the recorded kill.
	killed := ApplyAll(runningSession(t),
		Event{Kind: EventStopRequested, Generation: 1, At: at(20)},
		Event{Kind: EventKillRequested, Generation: 1, At: at(25), Reason: Killed("SIGKILL")},
	)
	mustApply(t, killed)
	current, _ := killed.Session.Current()
	if current.Lifecycle != LifecycleExited || current.Reason.Kind != ReasonKilled {
		t.Fatalf("attempt = %q/%q, want exited/killed", current.Lifecycle, current.Reason.Kind)
	}

	late := ApplyAll(killed.Session,
		Event{Kind: EventChildExited, Generation: 1, At: at(30), Reason: Stopped(0)},
	)
	if late.Applied {
		t.Fatalf("an exit reported after the kill was applied")
	}
	if got := late.Rejection.Code(); got != CodeNotRunning {
		t.Fatalf("rejection code = %s, want %s", got, CodeNotRunning)
	}
	if late.Session.State().Lifecycle != LifecycleExited {
		t.Fatalf("a rejected exit changed the lifecycle to %q", late.Session.State().Lifecycle)
	}

	rejected := ApplyAll(runningSession(t),
		Event{Kind: EventChildExited, Generation: 0, At: at(30), Reason: NaturalExit(0)},
		Event{Kind: EventStopRequested, Generation: 1, At: at(31)},
	)
	if rejected.Applied {
		t.Fatalf("a sequence starting with a stale callback was applied")
	}
	if got := rejected.Rejection.Code(); got != CodeStaleAttempt {
		t.Fatalf("rejection code = %s, want %s", got, CodeStaleAttempt)
	}
	if rejected.Session.State().Lifecycle != LifecycleRunning {
		t.Fatalf("a rejected sequence changed the lifecycle to %q", rejected.Session.State().Lifecycle)
	}
}

// restartToRunning drives a terminal session to a running attempt at the given
// generation.
func restartToRunning(t *testing.T, session Session, generation Generation) Session {
	t.Helper()
	outcome := Reduce(session, Event{
		Kind:        EventRestartRequested,
		Generation:  session.Generation,
		At:          at(400),
		Command:     launchCommand(t),
		WorkspaceID: testWorkspace,
	})
	mustApply(t, outcome)
	identity := testIdentity
	identity.StartTicks += uint64(generation) * 10
	spawned := Reduce(outcome.Session, Event{
		Kind:       EventSpawned,
		Generation: generation,
		At:         at(410),
		Identity:   identity,
	})
	mustApply(t, spawned)
	if spawned.Session.Generation != generation {
		t.Fatalf("generation = %d, want %d", spawned.Session.Generation, generation)
	}
	return spawned.Session
}

func mustWorkspace(t *testing.T, path string) workspace.Workspace {
	t.Helper()
	w, err := workspace.New(path)
	if err != nil {
		t.Fatalf("workspace.New(%q): %v", path, err)
	}
	return w
}

func compareSessions(t *testing.T, got, want Session) error {
	t.Helper()
	if got.ID != want.ID || got.Generation != want.Generation || got.UpdatedAt != want.UpdatedAt {
		return errComparison("identity or timestamps changed")
	}
	if got.AttemptCount() != want.AttemptCount() {
		return errComparison("attempt count changed")
	}
	for i := range want.Attempts {
		a, b := got.Attempts[i], want.Attempts[i]
		if a.Lifecycle != b.Lifecycle || a.Attachment != b.Attachment || a.Identity != b.Identity {
			return errComparison("attempt state changed")
		}
		if !a.Reason.Equal(b.Reason) {
			return errComparison("attempt reason changed")
		}
	}
	return nil
}

type errComparison string

func (e errComparison) Error() string { return string(e) }
