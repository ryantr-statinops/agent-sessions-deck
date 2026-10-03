package session

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

// rowCase is one contract row of docs/architecture/session-state-machine.md,
// including the rejections the stage acceptance requires.
type rowCase struct {
	name         string
	row          string
	from         func(t *testing.T) Session
	event        func(t *testing.T) Event
	wantApply    bool
	wantRow      string
	wantCode     Code
	wantState    State
	wantReason   ReasonKind
	wantOrphan   bool
	wantNotes    []string
	wantEffects  []EffectKind
	wantExitCode *int
	wantErr      error
}

// TestReduceTransitionTable walks every row T1-T20 plus the rejections, and
// asserts the outcome, the recorded reason and the effects handed to the caller.
func TestReduceTransitionTable(t *testing.T) {
	probeAlive := func(identity ProcessIdentity, seconds int) LivenessObservation {
		return LivenessObservation{Outcome: ProbeAlive, Identity: identity, At: at(seconds)}
	}
	probeGone := func(seconds int) LivenessObservation {
		return LivenessObservation{Outcome: ProbeGone, At: at(seconds)}
	}
	probeUnverifiable := func(detail string, seconds int) LivenessObservation {
		return LivenessObservation{Outcome: ProbeUnverifiable, Detail: detail, At: at(seconds)}
	}

	cases := []rowCase{
		{
			name: "T1 launch validation fails with no child",
			row:  RowLaunchValidationFailed,
			from: freshSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventLaunchRequested, At: at(0), Failure: "agent binary not found"}
			},
			wantApply:   true,
			wantRow:     RowLaunchValidationFailed,
			wantState:   State{Lifecycle: LifecycleFailed, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonLaunchFailed,
			wantEffects: nil,
		},
		{
			name: "T2 launch accepted freezes argv at generation 1",
			row:  RowLaunchAccepted,
			from: freshSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventLaunchRequested, At: at(0), Command: launchCommand(t), WorkspaceID: testWorkspace}
			},
			wantApply:   true,
			wantRow:     RowLaunchAccepted,
			wantState:   State{Lifecycle: LifecycleStarting, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantEffects: []EffectKind{EffectLaunchChild},
		},
		{
			name: "T3 spawn captured identity becomes running and detached",
			row:  RowSpawned,
			from: startingSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventSpawned, Generation: 1, At: at(10), Identity: testIdentity}
			},
			wantApply:   true,
			wantRow:     RowSpawned,
			wantState:   State{Lifecycle: LifecycleRunning, Attachment: AttachmentDetached, Activity: ActivityUnknown},
			wantEffects: nil,
		},
		{
			name: "T4 spawn failure leaves no child and fails the attempt",
			row:  RowSpawnFailed,
			from: startingSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventSpawnFailed, Generation: 1, At: at(10), Failure: "pty setup failed"}
			},
			wantApply:   true,
			wantRow:     RowSpawnFailed,
			wantState:   State{Lifecycle: LifecycleFailed, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonLaunchFailed,
			wantEffects: nil,
		},
		{
			name: "T5 persist failure after spawn cleans up by verified identity",
			row:  RowPersistAfterSpawn,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventPersistFailed, Generation: 1, At: at(15), Failure: "short write"}
			},
			wantApply:   true,
			wantRow:     RowPersistAfterSpawn,
			wantState:   State{Lifecycle: LifecycleFailed, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonLaunchFailed,
			wantNotes:   []string{NoteCleanupRequired},
			wantEffects: []EffectKind{EffectCleanupOwnedChild},
		},
		{
			name: "T5 without a captured identity refuses to clean up",
			row:  RowPersistAfterSpawn,
			from: startingSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventPersistFailed, Generation: 1, At: at(15), Failure: "short write"}
			},
			wantApply:   true,
			wantRow:     RowPersistAfterSpawn,
			wantState:   State{Lifecycle: LifecycleFailed, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonLaunchFailed,
			wantNotes:   []string{NoteCleanupRequired, NoteCleanupUnverified},
			wantEffects: nil,
		},
		{
			name: "T6 natural nonzero exit is exited with a code",
			row:  RowNaturalExit,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventChildExited, Generation: 1, At: at(30), Reason: NaturalExit(3)}
			},
			wantApply:    true,
			wantRow:      RowNaturalExit,
			wantState:    State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:   ReasonNaturalExit,
			wantExitCode: intPtr(3),
			wantEffects:  nil,
		},
		{
			name:        "T7 stop request signals the verified owned group",
			row:         RowStopRequested,
			from:        runningSession,
			event:       func(t *testing.T) Event { return Event{Kind: EventStopRequested, Generation: 1, At: at(20)} },
			wantApply:   true,
			wantRow:     RowStopRequested,
			wantState:   State{Lifecycle: LifecycleStopping, Attachment: AttachmentDetached, Activity: ActivityUnknown},
			wantEffects: []EffectKind{EffectSignalOwnedGroup},
		},
		{
			name: "T8 exit inside the stop window records stopped",
			row:  RowStopped,
			from: stoppingSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventChildExited, Generation: 1, At: at(25), Reason: NaturalExit(0)}
			},
			wantApply:    true,
			wantRow:      RowStopped,
			wantState:    State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:   ReasonStopped,
			wantExitCode: intPtr(0),
			wantEffects:  nil,
		},
		{
			name: "T8 a signal-killed child has no exit code and is still recorded",
			row:  RowStopped,
			from: stoppingSession,
			event: func(t *testing.T) Event {
				// The ordinary POSIX case: WIFSIGNALED, no exit status. Refusing it
				// would leave the session stopping forever.
				return Event{Kind: EventChildExited, Generation: 1, At: at(25), Reason: StoppedBySignal("SIGTERM")}
			},
			wantApply:   true,
			wantRow:     RowStopped,
			wantState:   State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonStopped,
			wantEffects: nil,
		},
		{
			name: "T8 a reap with no usable status is recorded with its evidence",
			row:  RowStopped,
			from: stoppingSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventChildExited, Generation: 1, At: at(25),
					Reason: Reason{Kind: ReasonNaturalExit, Detail: "reaped pid=4242 status=0x008b"}}
			},
			wantApply:   true,
			wantRow:     RowStopped,
			wantState:   State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonStopped,
			wantEffects: nil,
		},
		{
			name: "T8 never presents a stop as a natural exit",
			row:  RowStopped,
			from: stoppingSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventChildExited, Generation: 1, At: at(25), Reason: Stopped(0)}
			},
			wantApply:    true,
			wantRow:      RowStopped,
			wantState:    State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:   ReasonStopped,
			wantExitCode: intPtr(0),
			wantEffects:  nil,
		},
		{
			name:        "T9 stop timeout returns the attempt to running and never kills",
			row:         RowStopTimeout,
			from:        stoppingSession,
			event:       func(t *testing.T) Event { return Event{Kind: EventStopTimeout, Generation: 1, At: at(40)} },
			wantApply:   true,
			wantRow:     RowStopTimeout,
			wantState:   State{Lifecycle: LifecycleRunning, Attachment: AttachmentDetached, Activity: ActivityUnknown},
			wantNotes:   []string{NoteStopTimeout, NoteKillGuidance},
			wantEffects: nil,
		},
		{
			name: "T10 explicit kill terminates the verified owned group",
			row:  RowKilled,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventKillRequested, Generation: 1, At: at(50), Reason: Killed("SIGKILL")}
			},
			wantApply:   true,
			wantRow:     RowKilled,
			wantState:   State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonKilled,
			wantEffects: []EffectKind{EffectSignalOwnedGroup},
		},
		{
			name: "T10 kill of an orphan needs no second force: kill is already explicit",
			row:  RowKilled,
			from: orphanSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventKillRequested, Generation: 1, At: at(50), Reason: Killed("SIGKILL")}
			},
			wantApply:   true,
			wantRow:     RowKilled,
			wantState:   State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonKilled,
			wantNotes:   []string{NoteOrphaned},
			wantEffects: []EffectKind{EffectSignalOwnedGroup},
		},
		{
			name: "T10 records the signal that was actually delivered",
			row:  RowKilled,
			from: stoppingSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventKillRequested, Generation: 1, At: at(50), Reason: Killed("SIGKILL")}
			},
			wantApply:   true,
			wantRow:     RowKilled,
			wantState:   State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonKilled,
			wantEffects: []EffectKind{EffectSignalOwnedGroup},
		},
		{
			name: "T10 kill of a running attempt records the reported exit status",
			row:  RowKilled,
			from: runningSession,
			event: func(t *testing.T) Event {
				// A kill that landed just as the child exited on its own records the
				// exit it observed; the reducer never overwrites it with SIGKILL.
				return Event{Kind: EventKillRequested, Generation: 1, At: at(50), Reason: NaturalExit(0)}
			},
			wantApply:    true,
			wantRow:      RowKilled,
			wantState:    State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:   ReasonNaturalExit,
			wantExitCode: intPtr(0),
			wantEffects:  []EffectKind{EffectSignalOwnedGroup},
		},
		{
			name: "rejection: a kill with no observed evidence records no exit",
			row:  RowKilled,
			from: runningSession,
			event: func(t *testing.T) Event {
				// Delivering a signal proves nothing about the child, so a kill with
				// no reason and no reap evidence may not close the attempt.
				return Event{Kind: EventKillRequested, Generation: 1, At: at(50)}
			},
			wantApply: false,
			wantRow:   RowKilled,
			wantCode:  CodeConflict,
		},
		{
			name: "T11 forced restart of a live attempt stops it first",
			row:  RowRestartLive,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventRestartRequested, Generation: 1, At: at(60), Forced: true, Command: launchCommand(t), WorkspaceID: testWorkspace}
			},
			wantApply:   true,
			wantRow:     RowRestartLive,
			wantState:   State{Lifecycle: LifecycleStopping, Attachment: AttachmentDetached, Activity: ActivityUnknown},
			wantNotes:   []string{NoteRestartPending},
			wantEffects: []EffectKind{EffectSignalOwnedGroup},
		},
		{
			name: "T12 restart of a finished attempt bumps the generation",
			row:  RowRestartTerminal,
			from: exitedSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventRestartRequested, Generation: 1, At: at(60), Command: launchCommand(t), WorkspaceID: testWorkspace}
			},
			wantApply:   true,
			wantRow:     RowRestartTerminal,
			wantState:   State{Lifecycle: LifecycleStarting, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantEffects: []EffectKind{EffectLaunchChild},
		},
		{
			name: "T13 restart precondition failure opens a failed attempt",
			row:  RowRestartPrecondition,
			from: exitedSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventRestartRequested, Generation: 1, At: at(60), Failure: "agent binary vanished"}
			},
			wantApply:   true,
			wantRow:     RowRestartPrecondition,
			wantState:   State{Lifecycle: LifecycleFailed, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonRestartFailed,
			wantEffects: nil,
		},
		{
			name: "T14 PTY lost with a verified live process annotates orphaned",
			row:  RowPTYLostAlive,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventPTYLost, Generation: 1, At: at(20), Liveness: probeAlive(testIdentity, 20)}
			},
			wantApply:   true,
			wantRow:     RowPTYLostAlive,
			wantState:   State{Lifecycle: LifecycleRunning, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantOrphan:  true,
			wantNotes:   []string{NoteOrphaned},
			wantEffects: nil,
		},
		{
			name: "T15 PTY lost with unverifiable liveness keeps unknown",
			row:  RowPTYLostUnverifiable,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventPTYLost, Generation: 1, At: at(20), Liveness: probeUnverifiable("permission-denied", 20)}
			},
			wantApply:   true,
			wantRow:     RowPTYLostUnverifiable,
			wantState:   State{Lifecycle: LifecycleUnknown, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonPermissionDenied,
			wantEffects: nil,
		},
		{
			name: "T16 stored running with the process gone becomes exited",
			row:  RowProcessGone,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventReconciled, Generation: 1, At: at(90), Liveness: probeGone(90)}
			},
			wantApply:   true,
			wantRow:     RowProcessGone,
			wantState:   State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonProcessGone,
			wantNotes:   []string{NoteReconciled},
			wantEffects: nil,
		},
		{
			name: "T16 PTY loss plus a gone process closes the observation",
			row:  RowProcessGone,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventPTYLost, Generation: 1, At: at(90), Liveness: probeGone(90)}
			},
			wantApply:   true,
			wantRow:     RowProcessGone,
			wantState:   State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonProcessGone,
			wantEffects: nil,
		},
		{
			name: "T17 stored running whose pid is now another process closes stale",
			row:  RowIdentityMismatch,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventReconciled, Generation: 1, At: at(90), Liveness: probeAlive(foreignIdentity, 90)}
			},
			wantApply:   true,
			wantRow:     RowIdentityMismatch,
			wantState:   State{Lifecycle: LifecycleExited, Attachment: AttachmentUnavailable, Activity: ActivityUnknown},
			wantReason:  ReasonStaleIdentity,
			wantNotes:   []string{NoteReconciled},
			wantEffects: nil,
		},
		{
			name: "T18 inconclusive probe from running becomes unknown",
			row:  RowProbeInconclusive,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventProbeInconclusive, Generation: 1, At: at(70), Liveness: probeUnverifiable("probe-timeout", 70)}
			},
			wantApply:   true,
			wantRow:     RowProbeInconclusive,
			wantState:   State{Lifecycle: LifecycleUnknown, Attachment: AttachmentDetached, Activity: ActivityUnknown},
			wantReason:  ReasonProbeTimeout,
			wantEffects: nil,
		},
		{
			name: "T18 inconclusive probe from stopping becomes unknown",
			row:  RowProbeInconclusive,
			from: stoppingSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventProbeInconclusive, Generation: 1, At: at(70), Liveness: probeUnverifiable("probe-timeout", 70)}
			},
			wantApply:   true,
			wantRow:     RowProbeInconclusive,
			wantState:   State{Lifecycle: LifecycleUnknown, Attachment: AttachmentDetached, Activity: ActivityUnknown},
			wantReason:  ReasonProbeTimeout,
			wantEffects: nil,
		},
		{
			name: "T18 permission denied probe records the diagnostic reason",
			row:  RowProbeInconclusive,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventProbeInconclusive, Generation: 1, At: at(70), Liveness: probeUnverifiable("permission-denied on /proc", 70)}
			},
			wantApply:   true,
			wantRow:     RowProbeInconclusive,
			wantState:   State{Lifecycle: LifecycleUnknown, Attachment: AttachmentDetached, Activity: ActivityUnknown},
			wantReason:  ReasonPermissionDenied,
			wantEffects: nil,
		},
		{
			name: "T19 stale callback from an older generation is dropped",
			row:  RowStaleCallback,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventChildExited, Generation: 0, At: at(30), Reason: NaturalExit(0)}
			},
			wantApply:   false,
			wantRow:     RowStaleCallback,
			wantCode:    CodeStaleAttempt,
			wantEffects: []EffectKind{EffectDropStaleCallback},
		},
		{
			name: "rejection: launch twice would create concurrent attempts",
			row:  RowRestartTerminal,
			from: startingSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventLaunchRequested, Generation: 1, At: at(5), Command: launchCommand(t), WorkspaceID: testWorkspace}
			},
			wantApply: false,
			wantRow:   RowRestartTerminal,
			wantCode:  CodeConflict,
		},
		{
			name: "rejection: launch request without a usable command",
			row:  "",
			from: freshSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventLaunchRequested, At: at(0), Command: mustCommand(t, "opencode"), WorkspaceID: testWorkspace}
			},
			wantApply: false,
			wantCode:  CodeUnknown,
		},
		{
			name:      "rejection: spawn cannot be confirmed without an identity",
			row:       "",
			from:      startingSession,
			event:     func(t *testing.T) Event { return Event{Kind: EventSpawned, Generation: 1, At: at(10)} },
			wantApply: false,
			wantCode:  CodeUnknown,
		},
		{
			name: "rejection: spawn failure after running is already recorded",
			row:  "",
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventSpawnFailed, Generation: 1, At: at(11), Failure: "late error"}
			},
			wantApply: false,
			wantCode:  CodeConflict,
		},
		{
			name:      "rejection: stop on a finished attempt",
			row:       "",
			from:      exitedSession,
			event:     func(t *testing.T) Event { return Event{Kind: EventStopRequested, Generation: 1, At: at(31)} },
			wantApply: false,
			wantCode:  CodeNotRunning,
		},
		{
			name:      "rejection: stop while liveness is unknown refuses to signal",
			row:       RowStopRequested,
			from:      unknownSession,
			event:     func(t *testing.T) Event { return Event{Kind: EventStopRequested, Generation: 1, At: at(21)} },
			wantApply: false,
			wantRow:   RowStopRequested,
			wantCode:  CodeConflict,
		},
		{
			name:      "rejection: a second stop is not a new transition",
			row:       RowStopRequested,
			from:      stoppingSession,
			event:     func(t *testing.T) Event { return Event{Kind: EventStopRequested, Generation: 1, At: at(21)} },
			wantApply: false,
			wantRow:   RowStopRequested,
			wantCode:  CodeConflict,
		},
		{
			name:      "rejection: stop timeout with no stop in progress",
			row:       RowStopTimeout,
			from:      runningSession,
			event:     func(t *testing.T) Event { return Event{Kind: EventStopTimeout, Generation: 1, At: at(40)} },
			wantApply: false,
			wantRow:   RowStopTimeout,
			wantCode:  CodeConflict,
		},
		{
			name:      "rejection: kill before the identity is captured",
			row:       RowKilled,
			from:      startingSession,
			event:     func(t *testing.T) Event { return Event{Kind: EventKillRequested, Generation: 1, At: at(11)} },
			wantApply: false,
			wantRow:   RowKilled,
			wantCode:  CodeConflict,
		},
		{
			name: "rejection: kill carrying a recycled pid is refused",
			row:  RowKilled,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventKillRequested, Generation: 1, At: at(51), Identity: foreignIdentity}
			},
			wantApply: false,
			wantRow:   RowKilled,
			wantCode:  CodeStaleAttempt,
		},
		{
			name: "rejection: a forced restart of a starting attempt has nothing to signal",
			row:  RowRestartLive,
			from: startingSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventRestartRequested, Generation: 1, At: at(60), Forced: true, Command: launchCommand(t), WorkspaceID: testWorkspace}
			},
			wantApply: false,
			wantRow:   RowRestartLive,
			wantCode:  CodeConflict,
		},
		{
			name: "rejection: restart of a live attempt needs explicit force",
			row:  RowRestartLive,
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventRestartRequested, Generation: 1, At: at(60), Command: launchCommand(t), WorkspaceID: testWorkspace}
			},
			wantApply: false,
			wantRow:   RowRestartLive,
			wantCode:  CodeConflict,
		},
		{
			name: "rejection: an orphaned attempt blocks restart",
			row:  RowRestartLive,
			from: orphanSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventRestartRequested, Generation: 1, At: at(60), Forced: true, Command: launchCommand(t), WorkspaceID: testWorkspace}
			},
			wantApply: false,
			wantRow:   RowRestartLive,
			wantCode:  CodeConflict,
		},
		{
			name: "rejection: restart must stay in the session workspace",
			row:  RowRestartTerminal,
			from: exitedSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventRestartRequested, Generation: 1, At: at(60), Command: launchCommand(t), WorkspaceID: "/home/tester/other"}
			},
			wantApply: false,
			wantRow:   RowRestartTerminal,
			wantCode:  CodeConflict,
		},
		{
			name: "rejection: restart of a stopped attempt waits for the exit",
			row:  RowRestartLive,
			from: stoppingSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventRestartRequested, Generation: 1, At: at(60), Forced: true, Command: launchCommand(t), WorkspaceID: testWorkspace}
			},
			wantApply: false,
			wantRow:   RowRestartLive,
			wantCode:  CodeConflict,
		},
		{
			name: "rejection: restart cannot replace an unverified attempt",
			row:  RowRestartLive,
			from: unknownSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventRestartRequested, Generation: 1, At: at(60), Forced: true, Command: launchCommand(t), WorkspaceID: testWorkspace}
			},
			wantApply: false,
			wantRow:   RowRestartLive,
			wantCode:  CodeConflict,
		},
		{
			name: "rejection: exit reported twice for one attempt",
			row:  "",
			from: exitedSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventChildExited, Generation: 1, At: at(35), Reason: NaturalExit(0)}
			},
			wantApply: false,
			wantCode:  CodeNotRunning,
		},
		{
			name: "rejection: exit cannot un-observe a terminal attempt",
			row:  RowProbeInconclusive,
			from: exitedSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventProbeInconclusive, Generation: 1, At: at(70), Liveness: probeUnverifiable("probe-timeout", 70)}
			},
			wantApply: false,
			wantRow:   RowProbeInconclusive,
			wantCode:  CodeNotRunning,
		},
		{
			name:      "rejection: attach needs a PTY handle, so orphaned fails",
			row:       "",
			from:      orphanSession,
			event:     func(t *testing.T) Event { return Event{Kind: EventAttached, Generation: 1, At: at(21)} },
			wantApply: false,
			wantCode:  CodeSessionIOFailed,
		},
		{
			name:      "rejection: attach after exit fails with SESSION_IO_FAILED",
			row:       "",
			from:      exitedSession,
			event:     func(t *testing.T) Event { return Event{Kind: EventAttached, Generation: 1, At: at(31)} },
			wantApply: false,
			wantCode:  CodeSessionIOFailed,
		},
		{
			name:        "attach moves only the attachment axis",
			row:         "",
			from:        runningSession,
			event:       func(t *testing.T) Event { return Event{Kind: EventAttached, Generation: 1, At: at(21)} },
			wantApply:   true,
			wantState:   State{Lifecycle: LifecycleRunning, Attachment: AttachmentAttached, Activity: ActivityUnknown},
			wantEffects: nil,
		},
		{
			name: "rejection: a second interactive lease is refused",
			row:  "",
			from: func(t *testing.T) Session {
				outcome := Reduce(runningSession(t), Event{Kind: EventAttached, Generation: 1, At: at(21)})
				mustApply(t, outcome)
				return outcome.Session
			},
			event:     func(t *testing.T) Event { return Event{Kind: EventAttached, Generation: 1, At: at(22)} },
			wantApply: false,
			wantCode:  CodeConflict,
		},
		{
			name: "detach leaves the lifecycle untouched",
			row:  "",
			from: func(t *testing.T) Session {
				outcome := Reduce(runningSession(t), Event{Kind: EventAttached, Generation: 1, At: at(21)})
				mustApply(t, outcome)
				return outcome.Session
			},
			event:       func(t *testing.T) Event { return Event{Kind: EventDetached, Generation: 1, At: at(22)} },
			wantApply:   true,
			wantState:   State{Lifecycle: LifecycleRunning, Attachment: AttachmentDetached, Activity: ActivityUnknown},
			wantEffects: nil,
		},
		{
			name:      "rejection: detaching a detached session",
			row:       "",
			from:      runningSession,
			event:     func(t *testing.T) Event { return Event{Kind: EventDetached, Generation: 1, At: at(22)} },
			wantApply: false,
			wantCode:  CodeConflict,
		},
		{
			name: "rejection: a generation the session has not reached is a defect, not a stale callback",
			row:  "",
			from: runningSession,
			event: func(t *testing.T) Event {
				return Event{Kind: EventChildExited, Generation: 7, At: at(30), Reason: NaturalExit(0)}
			},
			wantApply: false,
			wantRow:   "",
			wantCode:  CodeConflict,
			// A future-generation observation is never the benign T19 drop, so it must
			// not report the drop effect and must be distinguishable by errors.Is.
			wantErr: ErrFutureGeneration,
		},
	}

	covered := make(map[string]bool, len(cases))
	for _, tc := range cases {
		covered[tc.row] = true
		t.Run(tc.name, func(t *testing.T) {
			from := tc.from(t)
			before := from.Clone()
			outcome := Reduce(from, tc.event(t))

			if outcome.Applied != tc.wantApply {
				t.Fatalf("applied = %v, want %v (rejection: %v)", outcome.Applied, tc.wantApply, outcome.Err())
			}
			if outcome.Row != tc.wantRow {
				t.Fatalf("row = %q, want %q", outcome.Row, tc.wantRow)
			}
			if tc.row != "" && outcome.Row != tc.row {
				t.Fatalf("row = %q, want contract row %q", outcome.Row, tc.row)
			}
			if outcome.Session.ID != before.ID {
				t.Fatalf("session id changed: %q -> %q", before.ID, outcome.Session.ID)
			}
			if err := outcome.Validate(); err != nil {
				t.Fatalf("resulting session is invalid: %v", err)
			}

			if !tc.wantApply {
				if outcome.Rejection == nil {
					t.Fatalf("rejected outcome carries no typed rejection")
				}
				if got := outcome.Rejection.Code(); got != tc.wantCode {
					t.Fatalf("rejection code = %s, want %s (%v)", got, tc.wantCode, outcome.Rejection)
				}
				if outcome.Rejection.Err.Hint == "" {
					t.Fatalf("rejection %q has no hint; the CLI contract requires a next action", outcome.Rejection)
				}
				if outcome.Rejection.Err.Reason == "" {
					t.Fatalf("rejection %q has no reason", outcome.Rejection)
				}
				if tc.wantErr != nil && !errors.Is(outcome.Err(), tc.wantErr) {
					t.Fatalf("rejection %v is not %v", outcome.Err(), tc.wantErr)
				}
				if tc.wantErr == nil && errors.Is(outcome.Err(), ErrFutureGeneration) {
					t.Fatalf("only a future-generation refusal may be %v", ErrFutureGeneration)
				}
				if !reflect.DeepEqual(outcome.Session, before) {
					t.Fatalf("rejected event changed the session:\n got %+v\nwant %+v", outcome.Session, before)
				}
				assertEffects(t, outcome, tc.wantEffects)
				return
			}

			current, ok := outcome.Session.Current()
			if !ok {
				t.Fatalf("applied event left the session without an attempt")
			}
			if current.State() != tc.wantState {
				t.Fatalf("state = %+v, want %+v", current.State(), tc.wantState)
			}
			if current.Reason.Kind != tc.wantReason {
				t.Fatalf("reason = %q, want %q", current.Reason.Kind, tc.wantReason)
			}
			if current.Orphaned() != tc.wantOrphan {
				t.Fatalf("orphaned = %v, want %v", current.Orphaned(), tc.wantOrphan)
			}
			for _, note := range tc.wantNotes {
				if !current.HasNote(note) {
					t.Fatalf("notes %v are missing %q", current.Notes(), note)
				}
			}
			if tc.wantExitCode != nil {
				if !current.HasExitCode || current.ExitCode != *tc.wantExitCode {
					t.Fatalf("exit code = %d (present %v), want %d", current.ExitCode, current.HasExitCode, *tc.wantExitCode)
				}
			}
			assertEffects(t, outcome, tc.wantEffects)
		})
	}

	for _, row := range AllTransitionRows() {
		if row == RowDeleteWhileActive {
			// T20 is a store operation rather than a lifecycle event; it is covered
			// by TestEnsureDeletableRefusesActiveSessions.
			continue
		}
		if !covered[row] {
			t.Errorf("contract row %s has no table case", row)
		}
	}
}

func assertEffects(t *testing.T, outcome Outcome, want []EffectKind) {
	t.Helper()
	got := make([]EffectKind, 0, len(outcome.Effects))
	for _, effect := range outcome.Effects {
		got = append(got, effect.Kind)
		if effect.Kind == EffectSignalOwnedGroup || effect.Kind == EffectCleanupOwnedChild {
			if !effect.Identity.Valid() {
				t.Errorf("effect %s carries an incomplete identity: %s", effect.Kind, effect.Identity.Describe())
			}
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("effects = %v, want %v", got, want)
	}
}

func intPtr(v int) *int { return &v }
