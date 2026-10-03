package session

import (
	"fmt"
	"strconv"
	"strings"
)

// ReasonKind classifies why an Attempt ended, or why its liveness is unknown.
//
// The distinction is normative: a natural nonzero exit is an exit with a code
// (T6), never a launch failure (T1/T4/T5), and a stop that timed out is not a
// kill (T9).
type ReasonKind string

const (
	// ReasonNaturalExit is a child that exited on its own, any code included.
	ReasonNaturalExit ReasonKind = "natural-exit"
	// ReasonStopped is a child that exited inside the graceful stop window (T8).
	ReasonStopped ReasonKind = "stopped"
	// ReasonKilled is a child terminated by an explicit kill action (T10).
	ReasonKilled ReasonKind = "killed"
	// ReasonLaunchFailed covers T1 (validation), T4 (spawn) and T5 (persist after
	// spawn). No child survived.
	ReasonLaunchFailed ReasonKind = "launch-failed"
	// ReasonRestartFailed is a restart whose precondition failed (T13); the prior
	// Attempt record is untouched.
	ReasonRestartFailed ReasonKind = "restart-failed"
	// ReasonProcessGone is a persisted running Attempt whose process no longer
	// exists after an owner restart (T16).
	ReasonProcessGone ReasonKind = "dead"
	// ReasonStaleIdentity is a persisted running Attempt whose live PID carries a
	// different identity (T17); the live PID is never adopted or signalled.
	ReasonStaleIdentity ReasonKind = "stale-identity"
	// ReasonProbeTimeout is an inconclusive liveness probe (T18).
	ReasonProbeTimeout ReasonKind = "probe-timeout"
	// ReasonPermissionDenied is an inconclusive liveness probe (T18).
	ReasonPermissionDenied ReasonKind = "permission-denied"
)

// AllReasonKinds returns every reason kind in contract order.
func AllReasonKinds() []ReasonKind {
	return []ReasonKind{
		ReasonNaturalExit,
		ReasonStopped,
		ReasonKilled,
		ReasonLaunchFailed,
		ReasonRestartFailed,
		ReasonProcessGone,
		ReasonStaleIdentity,
		ReasonProbeTimeout,
		ReasonPermissionDenied,
	}
}

// String returns the wire name of the reason kind.
func (k ReasonKind) String() string { return string(k) }

// Valid reports whether the reason kind is one of the defined ones.
func (k ReasonKind) Valid() bool {
	switch k {
	case ReasonNaturalExit, ReasonStopped, ReasonKilled, ReasonLaunchFailed,
		ReasonRestartFailed, ReasonProcessGone, ReasonStaleIdentity,
		ReasonProbeTimeout, ReasonPermissionDenied:
		return true
	default:
		return false
	}
}

// Terminal reports whether the kind closes an attempt in lifecycle exited.
func (k ReasonKind) Terminal() bool {
	switch k {
	case ReasonNaturalExit, ReasonStopped, ReasonKilled, ReasonProcessGone, ReasonStaleIdentity:
		return true
	default:
		return false
	}
}

// Failure reports whether the kind closes an attempt in lifecycle failed.
func (k ReasonKind) Failure() bool {
	return k == ReasonLaunchFailed || k == ReasonRestartFailed
}

// Diagnostic reports whether the kind explains an unknown lifecycle rather than
// an outcome. Unknown is sticky until real evidence arrives.
func (k ReasonKind) Diagnostic() bool {
	return k == ReasonProbeTimeout || k == ReasonPermissionDenied
}

// Kills reports whether the kind means the process was force-terminated.
// A stop timeout is deliberately absent: it returns the session to running.
func (k ReasonKind) Kills() bool { return k == ReasonKilled }

// Reason is the recorded cause of an attempt's terminal observation or of its
// unknown lifecycle.
//
// ExitCode is a pointer because "exited with code 0" and "no exit code observed"
// must never be confused. These JSON names are part of the durable record format.
type Reason struct {
	// Kind is the contract reason.
	Kind ReasonKind `json:"kind"`
	// ExitCode is the observed exit status, when the attempt produced one.
	ExitCode *int `json:"exit_code"`
	// Signal is the observed or sent signal name, e.g. "SIGKILL".
	Signal string `json:"signal"`
	// Detail is a typed error code or evidence text; it never replaces Kind.
	Detail string `json:"detail"`
}

// NaturalExit records a child that exited on its own with the given code.
func NaturalExit(code int) Reason {
	return Reason{Kind: ReasonNaturalExit, ExitCode: &code}
}

// Stopped records a child that exited inside the graceful stop window.
func Stopped(code int) Reason {
	return Reason{Kind: ReasonStopped, ExitCode: &code}
}

// StoppedBySignal records a child the graceful stop terminated with a signal. A
// SIGTERM-killed child is reaped with WIFSIGNALED and no exit code at all, so
// the signal is the evidence rather than a missing status.
func StoppedBySignal(signal string) Reason {
	return Reason{Kind: ReasonStopped, Signal: signal}
}

// Killed records an explicit forced termination.
func Killed(signal string) Reason {
	return Reason{Kind: ReasonKilled, Signal: signal}
}

// LaunchFailed records a launch that produced no surviving child.
func LaunchFailed(detail string) Reason {
	return Reason{Kind: ReasonLaunchFailed, Detail: detail}
}

// RestartFailed records a restart whose precondition failed.
func RestartFailed(detail string) Reason {
	return Reason{Kind: ReasonRestartFailed, Detail: detail}
}

// ProcessGone records a persisted running Attempt whose process vanished.
func ProcessGone(detail string) Reason {
	return Reason{Kind: ReasonProcessGone, Detail: detail}
}

// StaleIdentity records a persisted Attempt whose live PID is a different
// process.
func StaleIdentity(detail string) Reason {
	return Reason{Kind: ReasonStaleIdentity, Detail: detail}
}

// ProbeInconclusive records why liveness could not be established.
func ProbeInconclusive(kind ReasonKind, detail string) Reason {
	if !kind.Diagnostic() {
		kind = ReasonProbeTimeout
	}
	return Reason{Kind: kind, Detail: detail}
}

// Zero reports the unset reason.
func (r Reason) Zero() bool {
	return r.Kind == "" && r.ExitCode == nil && r.Signal == "" && r.Detail == ""
}

// ExitStatus returns the observed exit code, if one was recorded.
func (r Reason) ExitStatus() (int, bool) {
	if r.ExitCode == nil {
		return 0, false
	}
	return *r.ExitCode, true
}

// HasEvidence reports whether the reason names how the attempt ended: an exit
// code, a terminating signal or the runtime's own reap evidence.
//
// It is the check behind two rules. A terminal exit may never be recorded from
// nothing, because a reaped child always yields at least one of the three; and a
// record that needs one must not be refused for the other two, because the
// ordinary POSIX case - a child killed by a signal - has no exit code at all.
func (r Reason) HasEvidence() bool {
	return r.ExitCode != nil || r.Signal != "" || r.Detail != ""
}

// Validate checks that the reason is internally consistent.
func (r Reason) Validate() error {
	if r.Zero() {
		return nil
	}
	if !r.Kind.Valid() {
		return fmt.Errorf("reason %q is not a defined kind", string(r.Kind))
	}
	switch r.Kind {
	case ReasonNaturalExit, ReasonStopped:
		if !r.HasEvidence() {
			return fmt.Errorf("reason %q requires terminal evidence: an exit code, a terminating signal or the reap evidence", string(r.Kind))
		}
		if r.ExitCode != nil && r.Signal != "" {
			return fmt.Errorf("reason %q carries both an exit code and a signal", string(r.Kind))
		}
	case ReasonKilled:
		if r.ExitCode != nil {
			return fmt.Errorf("reason %q must not carry an exit code", string(r.Kind))
		}
		if r.Signal == "" && r.Detail == "" {
			return fmt.Errorf("reason %q requires the delivered signal or the reap evidence", string(r.Kind))
		}
	}
	return nil
}

// Equal reports whether two reasons record the same cause.
func (r Reason) Equal(other Reason) bool {
	if r.Kind != other.Kind || r.Signal != other.Signal || r.Detail != other.Detail {
		return false
	}
	if (r.ExitCode == nil) != (other.ExitCode == nil) {
		return false
	}
	if r.ExitCode != nil && *r.ExitCode != *other.ExitCode {
		return false
	}
	return true
}

// String renders the reason for diagnostics, e.g. "natural-exit exit-code=1".
func (r Reason) String() string {
	if r.Zero() {
		return ""
	}
	parts := []string{string(r.Kind)}
	if r.ExitCode != nil {
		parts = append(parts, "exit-code="+strconv.Itoa(*r.ExitCode))
	}
	if r.Signal != "" {
		parts = append(parts, "signal="+r.Signal)
	}
	if r.Detail != "" {
		parts = append(parts, "detail="+r.Detail)
	}
	return strings.Join(parts, " ")
}

// Notes attached to an attempt by the reducer. They are diagnostics: a stop
// timeout is a note on a still-running attempt, never an exit reason.
const (
	// NoteStopTimeout marks T9: the graceful stop did not finish in time and the
	// attempt went back to running.
	NoteStopTimeout = "stop-timeout"
	// NoteKillGuidance marks the guidance that follows a stop timeout.
	NoteKillGuidance = "kill-required"
	// NoteRestartPending marks T11: a stop was started to make room for a new
	// Attempt and the restart completes when the exit is observed.
	NoteRestartPending = "restart-pending"
	// NoteCleanupRequired marks T5: a child exists and must be cleaned up
	// through verified identity because persisting the running state failed.
	NoteCleanupRequired = "cleanup-required"
	// NoteCleanupUnverified marks T5 without a captured identity, where ASD
	// cannot signal anything and must report instead.
	NoteCleanupUnverified = "cleanup-unverified"
	// NoteReconciled marks T16/T17 evidence recorded from a persisted state.
	NoteReconciled = "reconciled"
	// NoteOrphaned marks T14: running with I/O unavailable.
	NoteOrphaned = "orphaned"
)
