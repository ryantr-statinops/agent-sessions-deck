package session

import (
	"fmt"
	"strings"
	"time"
)

// ProcessIdentity is the evidence that a live OS process is the one ASD started
// for a given Attempt.
//
// A PID alone is never proof: the OS recycles PIDs, so identity is the tuple
// PID + PGID + boot ID + process start ticks + owner instance ID
// (glossary, "ProcessIdentity"). A signal or a liveness claim is valid only when
// the observed identity equals the recorded one; otherwise the runtime fails
// closed (invariant 2).
type ProcessIdentity struct {
	// PID is the child process id.
	PID int `json:"pid"`
	// PGID is the child's process group id, the unit a stop or kill signals.
	PGID int `json:"pgid"`
	// BootID identifies the boot the process belongs to, so a recycled PID after
	// a reboot can never match a pre-reboot record.
	BootID string `json:"boot_id"`
	// StartTicks is /proc/PID/stat field 22, the process start time in clock ticks.
	StartTicks uint64 `json:"start_ticks"`
	// OwnerInstanceID is the ASD owner instance that spawned the child, so a
	// record written by a previous owner is recognized as stale on restart.
	OwnerInstanceID string `json:"owner_instance_id"`
}

// Validate reports whether the identity carries every component.
func (id ProcessIdentity) Validate() error {
	switch {
	case id.PID <= 0:
		return &InvalidIdentityError{Identity: id, Problem: "pid is not positive"}
	case id.PGID <= 0:
		return &InvalidIdentityError{Identity: id, Problem: "pgid is not positive"}
	case id.BootID == "":
		return &InvalidIdentityError{Identity: id, Problem: "boot id is empty"}
	case id.StartTicks == 0:
		return &InvalidIdentityError{Identity: id, Problem: "process start ticks are zero"}
	case id.OwnerInstanceID == "":
		return &InvalidIdentityError{Identity: id, Problem: "owner instance id is empty"}
	}
	return nil
}

// Valid reports whether the identity is usable for a liveness claim or a signal.
func (id ProcessIdentity) Valid() bool { return id.Validate() == nil }

// Equal reports whether two identities describe the same child process.
func (id ProcessIdentity) Equal(other ProcessIdentity) bool { return id == other }

// Describe renders the identity for diagnostics and logs.
func (id ProcessIdentity) Describe() string {
	return fmt.Sprintf("pid=%d pgid=%d boot=%s start=%d owner=%s",
		id.PID, id.PGID, id.BootID, id.StartTicks, id.OwnerInstanceID)
}

// String renders the identity for diagnostics.
func (id ProcessIdentity) String() string { return id.Describe() }

// InvalidIdentityError reports an incomplete or impossible identity.
// The session layer maps it to the LAUNCH_FAILED or CORRUPT_STATE code.
type InvalidIdentityError struct {
	Identity ProcessIdentity
	Problem  string
}

func (e *InvalidIdentityError) Error() string {
	return fmt.Sprintf("invalid process identity (%s): %s", e.Identity.Describe(), e.Problem)
}

// ProbeOutcome is what a runtime could establish about a recorded identity.
type ProbeOutcome string

const (
	// ProbeGone means the process entry does not exist.
	ProbeGone ProbeOutcome = "gone"
	// ProbeAlive means the process entry exists and its identity fields were read.
	ProbeAlive ProbeOutcome = "alive"
	// ProbeUnverifiable means the probe itself was inconclusive: timeout,
	// permission denied, or no proc filesystem. It is never an exit (T18).
	ProbeUnverifiable ProbeOutcome = "unverifiable"
)

// String returns the wire name of the outcome.
func (o ProbeOutcome) String() string { return string(o) }

// Valid reports whether the outcome is one of the defined ones.
func (o ProbeOutcome) Valid() bool {
	switch o {
	case ProbeGone, ProbeAlive, ProbeUnverifiable:
		return true
	default:
		return false
	}
}

// LivenessObservation is one liveness reading for a recorded identity.
type LivenessObservation struct {
	// Outcome is what the probe established.
	Outcome ProbeOutcome
	// Identity is the observed identity; it is required when Outcome is
	// ProbeAlive and ignored otherwise.
	Identity ProcessIdentity
	// Detail records why the probe was inconclusive, e.g. "permission-denied"
	// or "probe-timeout".
	Detail string
	// At is when the probe ran, UTC.
	At time.Time
}

// Validate checks that the observation is internally consistent.
func (o LivenessObservation) Validate() error {
	if !o.Outcome.Valid() {
		return fmt.Errorf("liveness observation: outcome %q is not a defined value", string(o.Outcome))
	}
	if o.At.IsZero() {
		return fmt.Errorf("liveness observation: observation timestamp is zero")
	}
	switch o.Outcome {
	case ProbeAlive:
		if err := o.Identity.Validate(); err != nil {
			return fmt.Errorf("liveness observation: alive outcome needs a complete identity: %w", err)
		}
	case ProbeUnverifiable:
		if o.Detail == "" {
			return fmt.Errorf("liveness observation: unverifiable outcome needs a detail such as probe-timeout or permission-denied")
		}
	}
	return nil
}

// Classification is the reducer's verdict on a recorded identity against one
// observation.
type Classification string

const (
	// ClassificationVerified means the live process is the recorded child.
	ClassificationVerified Classification = "verified"
	// ClassificationGone means the process no longer exists (T16).
	ClassificationGone Classification = "gone"
	// ClassificationStale means a live PID exists but the identity differs, so
	// the old observation closes and the live PID is never adopted (T17).
	ClassificationStale Classification = "stale"
	// ClassificationUnverifiable means no evidence could be gathered (T15/T18).
	ClassificationUnverifiable Classification = "unverifiable"
)

// String returns the wire name of the classification.
func (c Classification) String() string { return string(c) }

// Classify compares a recorded identity against one observation.
//
// It fails closed: an incomplete record or an unusable observation classifies as
// unverifiable rather than verified, because unverifiable never authorizes a
// signal and never fabricates an exit.
func (rec ProcessIdentity) Classify(obs LivenessObservation) Classification {
	if !rec.Valid() || obs.Validate() != nil {
		return ClassificationUnverifiable
	}
	switch obs.Outcome {
	case ProbeGone:
		return ClassificationGone
	case ProbeUnverifiable:
		return ClassificationUnverifiable
	case ProbeAlive:
		if rec.Equal(obs.Identity) {
			return ClassificationVerified
		}
		return ClassificationStale
	default:
		return ClassificationUnverifiable
	}
}

// EvidenceLine renders the observation as a reconciliation note for the
// attempt, so the original evidence survives alongside the resulting state.
func (o LivenessObservation) EvidenceLine() string {
	var b strings.Builder
	b.WriteString("liveness=")
	b.WriteString(string(o.Outcome))
	if o.Outcome == ProbeAlive {
		b.WriteString(" observed=")
		b.WriteString(o.Identity.Describe())
	}
	if o.Detail != "" {
		b.WriteString(" detail=")
		b.WriteString(o.Detail)
	}
	return b.String()
}
