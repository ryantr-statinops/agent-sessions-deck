package session

import (
	"fmt"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// EventKind names one observation the reducer can receive. Every kind maps to a
// row of docs/architecture/session-state-machine.md.
type EventKind string

const (
	// EventLaunchRequested opens the first attempt. A validation failure yields
	// T1, a resolved command yields T2.
	EventLaunchRequested EventKind = "launch-requested"
	// EventSpawned reports a child with a captured identity (T3).
	EventSpawned EventKind = "spawned"
	// EventSpawnFailed reports that no child exists (T4).
	EventSpawnFailed EventKind = "spawn-failed"
	// EventPersistFailed reports that persisting the running state failed, so a
	// child exists and must be cleaned up through verified identity (T5).
	EventPersistFailed EventKind = "persist-failed"
	// EventChildExited reports a reaped child (T6 natural, T8 after stop).
	EventChildExited EventKind = "child-exited"
	// EventStopRequested asks for a graceful stop (T7).
	EventStopRequested EventKind = "stop-requested"
	// EventStopTimeout reports that the stop window elapsed (T9).
	EventStopTimeout EventKind = "stop-timeout"
	// EventKillRequested is an explicit forced termination (T10).
	EventKillRequested EventKind = "kill-requested"
	// EventRestartRequested replaces the current attempt (T11, T12, T13).
	EventRestartRequested EventKind = "restart-requested"
	// EventPTYLost reports that the PTY handle is gone while a process may live
	// (T14 orphan, T15 unverifiable, T16 gone, T17 stale).
	EventPTYLost EventKind = "pty-lost"
	// EventProbeInconclusive reports a probe that could not conclude (T18).
	EventProbeInconclusive EventKind = "probe-inconclusive"
	// EventReconciled reports a reconciliation reading for a persisted state
	// (T16, T17, and the resolution of a sticky unknown).
	EventReconciled EventKind = "reconciled"
	// EventAttached moves only the attachment axis.
	EventAttached EventKind = "attached"
	// EventDetached moves only the attachment axis.
	EventDetached EventKind = "detached"
)

// AllEventKinds returns every event kind in contract order.
func AllEventKinds() []EventKind {
	return []EventKind{
		EventLaunchRequested,
		EventSpawned,
		EventSpawnFailed,
		EventPersistFailed,
		EventChildExited,
		EventStopRequested,
		EventStopTimeout,
		EventKillRequested,
		EventRestartRequested,
		EventPTYLost,
		EventProbeInconclusive,
		EventReconciled,
		EventAttached,
		EventDetached,
	}
}

// String returns the wire name of the event kind.
func (k EventKind) String() string { return string(k) }

// Valid reports whether the event kind is one of the defined ones.
func (k EventKind) Valid() bool {
	switch k {
	case EventLaunchRequested, EventSpawned, EventSpawnFailed, EventPersistFailed,
		EventChildExited, EventStopRequested, EventStopTimeout, EventKillRequested,
		EventRestartRequested, EventPTYLost, EventProbeInconclusive, EventReconciled,
		EventAttached, EventDetached:
		return true
	default:
		return false
	}
}

// LifecycleAffecting reports whether the kind can change the lifecycle axis.
// Attach and detach must not touch lifecycle: a client leaving never terminates
// the agent.
func (k EventKind) LifecycleAffecting() bool { return k != EventAttached && k != EventDetached }

// Event is one observation handed to the reducer.
//
// The fields are the union of every event's payload; each kind documents which
// ones it requires, and Validate enforces the requirement so a malformed event
// is rejected instead of half-applied.
type Event struct {
	// Kind selects the transition.
	Kind EventKind
	// Generation is the attempt the observation belongs to. It fences the
	// callback: an observation carrying another generation is dropped (T19).
	// A session without an attempt uses generation 0.
	Generation Generation
	// At is the observation timestamp, UTC.
	At time.Time

	// Command is the resolved command frozen into a new attempt (T2, T12).
	Command agent.Command
	// WorkspaceID is the workspace for a new attempt (T2, T12).
	WorkspaceID workspace.ID
	// Identity is the captured child identity (T3).
	Identity ProcessIdentity
	// Reason is the recorded cause (T6, T8, T10, T13, T18).
	Reason Reason
	// Liveness is the reconciliation or probe reading (T14-T18).
	Liveness LivenessObservation
	// Failure is a typed failure detail for the kinds that fail without a
	// structured reason (T1 validation, T4 spawn, T5 store, T13 precondition).
	Failure string
	// Forced marks an explicit force confirmation, required to restart a live
	// attempt (T11).
	Forced bool
}

// Validate checks the event payload for its kind.
func (e Event) Validate() error {
	if !e.Kind.Valid() {
		return fmt.Errorf("event kind %q is not a defined value", string(e.Kind))
	}
	if e.At.IsZero() {
		return fmt.Errorf("event %q has a zero timestamp", string(e.Kind))
	}
	switch e.Kind {
	case EventLaunchRequested:
		if e.Failure == "" {
			if err := e.Command.ValidateForLaunch(); err != nil {
				return fmt.Errorf("event %q needs a launch-valid resolved command: %w", string(e.Kind), err)
			}
			if err := e.WorkspaceID.Validate(); err != nil {
				return fmt.Errorf("event %q needs a workspace id: %w", string(e.Kind), err)
			}
		}
	case EventSpawned:
		if err := e.Identity.Validate(); err != nil {
			return fmt.Errorf("event %q needs a captured process identity: %w", string(e.Kind), err)
		}
	case EventChildExited:
		if err := e.Reason.Validate(); err != nil {
			return fmt.Errorf("event %q: %w", string(e.Kind), err)
		}
		if !e.Reason.Kind.Terminal() {
			return fmt.Errorf("event %q needs a terminal reason kind, got %q", string(e.Kind), string(e.Reason.Kind))
		}
	case EventKillRequested:
		if err := e.Reason.Validate(); err != nil {
			return fmt.Errorf("event %q: %w", string(e.Kind), err)
		}
	case EventSpawnFailed, EventPersistFailed:
		if e.Failure == "" {
			return fmt.Errorf("event %q needs a failure detail", string(e.Kind))
		}
	case EventRestartRequested:
		if e.Failure == "" {
			if err := e.Command.ValidateForLaunch(); err != nil {
				return fmt.Errorf("event %q needs a launch-valid resolved command: %w", string(e.Kind), err)
			}
			if err := e.WorkspaceID.Validate(); err != nil {
				return fmt.Errorf("event %q needs a workspace id: %w", string(e.Kind), err)
			}
		}
	case EventPTYLost, EventReconciled, EventProbeInconclusive:
		if err := e.Liveness.Validate(); err != nil {
			return fmt.Errorf("event %q: %w", string(e.Kind), err)
		}
	case EventStopRequested, EventStopTimeout, EventAttached, EventDetached:
		// No payload beyond the timestamp is required.
	}
	return nil
}
