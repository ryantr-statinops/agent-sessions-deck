// Package session holds the logical Session, its execution Attempt, the
// observable state triple, and the pure lifecycle reducer that decides which
// observations are allowed to change a stored state.
//
// The normative contract is docs/architecture/session-state-machine.md; every
// reducer case in this package names the transition row it implements. Nothing
// here spawns a process, opens a PTY, writes a store or speaks a transport: the
// reducer is pure so Stages 03, 04, 05 and 09 can test and reuse the same
// decisions.
package session

import (
	"fmt"
	"time"
)

// Lifecycle is the process-truth axis: what the OS child is doing.
type Lifecycle string

const (
	// LifecycleCreated means the record exists and nothing was spawned.
	LifecycleCreated Lifecycle = "created"
	// LifecycleStarting means a launch was requested and no child identity is
	// captured yet.
	LifecycleStarting Lifecycle = "starting"
	// LifecycleRunning means a child identity is captured with no terminal
	// observation.
	LifecycleRunning Lifecycle = "running"
	// LifecycleStopping means a graceful stop was requested and the child is not
	// reaped yet.
	LifecycleStopping Lifecycle = "stopping"
	// LifecycleExited means the child was reaped and an exit reason was recorded.
	LifecycleExited Lifecycle = "exited"
	// LifecycleFailed means launch, restart precondition or runtime fault failed;
	// it always carries a reason.
	LifecycleFailed Lifecycle = "failed"
	// LifecycleUnknown means liveness could not be established. It is sticky:
	// a failed probe never becomes an exit (invariant 3).
	LifecycleUnknown Lifecycle = "unknown"
)

// AllLifecycles returns every lifecycle value in contract order.
func AllLifecycles() []Lifecycle {
	return []Lifecycle{
		LifecycleCreated,
		LifecycleStarting,
		LifecycleRunning,
		LifecycleStopping,
		LifecycleExited,
		LifecycleFailed,
		LifecycleUnknown,
	}
}

// String returns the wire name of the lifecycle.
func (l Lifecycle) String() string { return string(l) }

// Valid reports whether the lifecycle is one of the defined ones.
func (l Lifecycle) Valid() bool {
	switch l {
	case LifecycleCreated, LifecycleStarting, LifecycleRunning, LifecycleStopping, LifecycleExited, LifecycleFailed, LifecycleUnknown:
		return true
	default:
		return false
	}
}

// Terminal reports whether the attempt has a recorded terminal observation and
// can no longer change without a new Attempt.
func (l Lifecycle) Terminal() bool {
	return l == LifecycleExited || l == LifecycleFailed
}

// Active reports whether the session still owns something: metadata delete is
// refused while active (transition T20).
func (l Lifecycle) Active() bool { return !l.Terminal() }

// ProcessMayExist reports whether a child could still exist in this lifecycle.
// Signal authorization additionally requires a verified ProcessIdentity.
func (l Lifecycle) ProcessMayExist() bool {
	switch l {
	case LifecycleStarting, LifecycleRunning, LifecycleStopping, LifecycleUnknown:
		return true
	default:
		return false
	}
}

// ParseLifecycle converts a wire name into a Lifecycle.
func ParseLifecycle(value string) (Lifecycle, error) {
	lifecycle := Lifecycle(value)
	if !lifecycle.Valid() {
		return "", fmt.Errorf("unknown lifecycle %q", value)
	}
	return lifecycle, nil
}

// Attachment is the I/O-truth axis: whether ASD can drive the session now.
type Attachment string

const (
	// AttachmentAttached means a client holds the interactive lease.
	AttachmentAttached Attachment = "attached"
	// AttachmentDetached means no client is attached but the PTY is healthy.
	AttachmentDetached Attachment = "detached"
	// AttachmentUnavailable means no PTY handle exists for the current Attempt:
	// never started, already exited, or PTY lost.
	AttachmentUnavailable Attachment = "unavailable"
)

// AllAttachments returns every attachment value in contract order.
func AllAttachments() []Attachment {
	return []Attachment{AttachmentAttached, AttachmentDetached, AttachmentUnavailable}
}

// String returns the wire name of the attachment.
func (a Attachment) String() string { return string(a) }

// Valid reports whether the attachment is one of the defined ones.
func (a Attachment) Valid() bool {
	switch a {
	case AttachmentAttached, AttachmentDetached, AttachmentUnavailable:
		return true
	default:
		return false
	}
}

// Available reports whether a PTY handle exists for the current Attempt.
func (a Attachment) Available() bool { return a == AttachmentAttached || a == AttachmentDetached }

// Activity is the interaction-hint axis. It is set only from documented provider
// evidence; silence never means idle (plan README decision 5).
type Activity string

const (
	ActivityUnknown Activity = "unknown"
	ActivityWorking Activity = "working"
	ActivityIdle    Activity = "idle"
)

// AllActivities returns every activity value in contract order.
func AllActivities() []Activity {
	return []Activity{ActivityUnknown, ActivityWorking, ActivityIdle}
}

// String returns the wire name of the activity.
func (a Activity) String() string { return string(a) }

// Valid reports whether the activity is one of the defined ones.
func (a Activity) Valid() bool {
	switch a {
	case ActivityUnknown, ActivityWorking, ActivityIdle:
		return true
	default:
		return false
	}
}

// Evidenced reports whether the value is a provider-evidenced hint rather than
// the unknown default.
func (a Activity) Evidenced() bool { return a == ActivityWorking || a == ActivityIdle }

// Evidence is the documented provider signal that justifies an Activity other
// than unknown.
type Evidence struct {
	// Source is the provider or signal that reported the activity.
	Source string `json:"source"`
	// Detail is the provider-specific observation.
	Detail string `json:"detail"`
	// At is when the evidence was observed, UTC.
	At time.Time `json:"at"`
}

// Valid reports whether the evidence may justify a non-unknown activity.
func (e Evidence) Valid() bool { return e.Source != "" && !e.At.IsZero() }

// State is the observable triple. No single field is the state of a session.
type State struct {
	Lifecycle  Lifecycle
	Attachment Attachment
	Activity   Activity
}

// initialState is the state of a session record that has no Attempt yet.
func initialState() State {
	return State{
		Lifecycle:  LifecycleCreated,
		Attachment: AttachmentUnavailable,
		Activity:   ActivityUnknown,
	}
}

// Validate checks that every axis holds a defined value.
func (s State) Validate() error {
	if !s.Lifecycle.Valid() {
		return fmt.Errorf("state: lifecycle %q is not a defined value", string(s.Lifecycle))
	}
	if !s.Attachment.Valid() {
		return fmt.Errorf("state: attachment %q is not a defined value", string(s.Attachment))
	}
	if !s.Activity.Valid() {
		return fmt.Errorf("state: activity %q is not a defined value", string(s.Activity))
	}
	return nil
}

// Orphaned reports the orphan annotation: lifecycle running with I/O
// unavailable. Orphaned is not a lifecycle state, and the observations that
// produced it are preserved (glossary, "Orphaned").
func (s State) Orphaned() bool {
	return s.Lifecycle == LifecycleRunning && s.Attachment == AttachmentUnavailable
}
