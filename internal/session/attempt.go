package session

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// maxNameLen bounds a session name.
const maxNameLen = 128

// ID is the stable identity of a logical Session.
//
// The ID survives restarts: a restart creates a new Attempt with a higher
// generation and never mints a new Session ID (transition T12). It is an opaque
// string so a future change of format does not renumber stored sessions.
type ID string

// String returns the session id.
func (id ID) String() string { return string(id) }

// Valid reports whether the id is a non-empty printable token.
func (id ID) Valid() bool {
	if id == "" {
		return false
	}
	return !strings.ContainsFunc(string(id), func(r rune) bool {
		return r <= ' ' || r == 0x7f
	})
}

// Generation is the monotonically increasing Attempt generation, scoped to one
// Session. It fences callbacks: an exit or timeout that belongs to an older
// generation is dropped instead of overwriting the current Attempt (T19).
type Generation uint64

// String renders the generation.
func (g Generation) String() string { return fmt.Sprintf("%d", uint64(g)) }

// Attempt is one execution of a Session's resolved command in its workspace.
//
// An Attempt is the unit of lifecycle: created state, exit reason and process
// identity all belong to it, and the slice fields are copied on the way in and
// out so a stored attempt cannot be mutated by a caller holding the slice it
// supplied.
type Attempt struct {
	// Generation is the attempt number within the owning Session, starting at 1.
	Generation Generation
	// Command is the immutable resolved command. It is zero until resolution
	// succeeded (T1).
	Command agent.Command
	// WorkspaceID is the workspace the command runs in.
	WorkspaceID workspace.ID
	// OwnerInstanceID is the owner that captured this attempt's child identity.
	OwnerInstanceID string
	// StartedAt is when the attempt was opened by a launch or restart request.
	StartedAt time.Time
	// RunningAt is when a child identity was captured and persisted (T3).
	RunningAt time.Time
	// EndedAt is when a terminal observation was recorded.
	EndedAt time.Time
	// Lifecycle, Attachment and Activity are the observable triple.
	Lifecycle  Lifecycle
	Attachment Attachment
	Activity   Activity
	// ActivityEvidence justifies a non-unknown Activity.
	ActivityEvidence Evidence
	// Reason is the recorded cause of a terminal or unknown observation.
	Reason Reason
	// Identity is the observed ProcessIdentity, invalid until captured (T3).
	Identity ProcessIdentity
	// ExitCode mirrors Reason.ExitCode for structured readers.
	ExitCode int
	// HasExitCode distinguishes exit code 0 from no observed code.
	HasExitCode bool

	notes []string
}

// NewAttempt opens an attempt in lifecycle created.
func NewAttempt(generation Generation, at time.Time) Attempt {
	return Attempt{
		Generation: generation,
		StartedAt:  at,
		Lifecycle:  LifecycleCreated,
		Attachment: AttachmentUnavailable,
		Activity:   ActivityUnknown,
	}
}

// State returns the observable triple of the attempt.
func (a Attempt) State() State {
	return State{Lifecycle: a.Lifecycle, Attachment: a.Attachment, Activity: a.Activity}
}

// HasIdentity reports whether a verified child identity was captured.
func (a Attempt) HasIdentity() bool { return a.Identity.Valid() }

// HasCommand reports whether a resolved command was frozen into the attempt.
func (a Attempt) HasCommand() bool { return !a.Command.IsZero() }

// Terminal reports whether the attempt has a recorded terminal observation.
func (a Attempt) Terminal() bool { return a.Lifecycle.Terminal() }

// signalAuthorizable reports whether a signal may be sent to this attempt's
// child. Both halves are required and neither is sufficient alone: a lifecycle
// that may still own a process, and a verified ProcessIdentity proving the live
// process is the one ASD started. A pid without the rest of the identity is
// refused, because the OS recycles pids (invariant 2).
func (a Attempt) signalAuthorizable() bool {
	return a.Lifecycle.ProcessMayExist() && a.HasIdentity()
}

// Orphaned reports the orphan annotation: lifecycle running with I/O
// unavailable.
func (a Attempt) Orphaned() bool { return a.State().Orphaned() }

// Notes returns a copy of the attempt's diagnostic notes.
func (a Attempt) Notes() []string { return slices.Clone(a.notes) }

// HasNote reports whether the attempt carries a note.
func (a Attempt) HasNote(note string) bool { return slices.Contains(a.notes, note) }

// Clone returns a deep copy.
func (a Attempt) Clone() Attempt {
	clone := a
	clone.notes = slices.Clone(a.notes)
	return clone
}

// setNote appends a note once, keeping notes stable across re-application.
func (a *Attempt) setNote(note string) {
	if !slices.Contains(a.notes, note) {
		a.notes = append(a.notes, note)
	}
}

// Validate checks the attempt's internal consistency.
func (a Attempt) Validate() error {
	if a.Generation == 0 {
		return fmt.Errorf("attempt generation must be positive")
	}
	if a.StartedAt.IsZero() {
		return fmt.Errorf("attempt %d has no start timestamp", a.Generation)
	}
	if err := a.State().Validate(); err != nil {
		return fmt.Errorf("attempt %d: %w", a.Generation, err)
	}
	if err := a.Reason.Validate(); err != nil {
		return fmt.Errorf("attempt %d: %w", a.Generation, err)
	}
	if a.Activity != ActivityUnknown && !a.ActivityEvidence.Valid() {
		return fmt.Errorf("attempt %d: activity %q has no provider evidence", a.Generation, string(a.Activity))
	}
	if a.Lifecycle == LifecycleRunning && a.Attachment == AttachmentAttached && !a.HasIdentity() {
		return fmt.Errorf("attempt %d: attached and running without a captured identity", a.Generation)
	}
	// running/stopping and unknown attempts all refer to a captured child.
	// Unknown is liveness uncertainty for a known identity, never a missing one.
	switch a.Lifecycle {
	case LifecycleRunning, LifecycleStopping, LifecycleUnknown:
		if !a.HasIdentity() {
			return fmt.Errorf("attempt %d: lifecycle %q without a captured process identity",
				a.Generation, string(a.Lifecycle))
		}
	}
	if a.Lifecycle.Terminal() && a.EndedAt.IsZero() {
		return fmt.Errorf("attempt %d: terminal lifecycle %q without an end timestamp", a.Generation, string(a.Lifecycle))
	}
	// The recorded reason must match the lifecycle: an exit carries a terminal
	// reason, a failure carries a failure reason, and an unknown carries only a
	// diagnostic reason.
	switch a.Lifecycle {
	case LifecycleExited:
		if !a.Reason.Kind.Terminal() {
			return fmt.Errorf("attempt %d: exited lifecycle needs a terminal reason, got %q", a.Generation, string(a.Reason.Kind))
		}
	case LifecycleFailed:
		if !a.Reason.Kind.Failure() {
			return fmt.Errorf("attempt %d: failed lifecycle needs a failure reason, got %q", a.Generation, string(a.Reason.Kind))
		}
	case LifecycleUnknown:
		if !a.Reason.Kind.Diagnostic() {
			return fmt.Errorf("attempt %d: unknown lifecycle needs a diagnostic reason, got %q", a.Generation, string(a.Reason.Kind))
		}
	}
	if a.HasExitCode {
		code, ok := a.Reason.ExitStatus()
		if !ok || code != a.ExitCode {
			return fmt.Errorf("attempt %d: exit code %d disagrees with reason %q", a.Generation, a.ExitCode, string(a.Reason.Kind))
		}
	}
	if a.HasIdentity() && a.OwnerInstanceID != "" && a.Identity.OwnerInstanceID != a.OwnerInstanceID {
		return fmt.Errorf("attempt %d: identity owner %q disagrees with attempt owner %q",
			a.Generation, a.Identity.OwnerInstanceID, a.OwnerInstanceID)
	}
	return nil
}

// Validate checks an activity against provider evidence. Silence is not
// evidence, so anything other than unknown needs a documented provider signal.
func ValidateActivity(activity Activity, evidence Evidence) error {
	if !activity.Valid() {
		return fmt.Errorf("activity %q is not a defined value", string(activity))
	}
	if activity.Evidenced() && !evidence.Valid() {
		return fmt.Errorf("activity %q requires provider evidence", string(activity))
	}
	return nil
}

// Session is the logical unit of work: one agent, one workspace, one stable
// identity across restarts (glossary, "Logical Session").
type Session struct {
	// ID is stable across restarts.
	ID ID
	// Name is the user-visible label.
	Name string
	// AgentID is the agent definition this session launches.
	AgentID agent.ID
	// WorkspaceID is the canonical workspace identity.
	WorkspaceID workspace.ID
	// CreatedAt is when the record was created, UTC.
	CreatedAt time.Time
	// UpdatedAt is when the last applied transition landed, UTC.
	UpdatedAt time.Time
	// LastSeenAt is when ASD last held positive liveness evidence, UTC.
	LastSeenAt time.Time
	// Generation is the current attempt generation; 0 means no attempt yet.
	Generation Generation
	// Attempts is the immutable attempt history, oldest first.
	Attempts []Attempt
}

// New builds an empty session record in lifecycle created.
func New(id ID, name string, agentID agent.ID, workspaceID workspace.ID, at time.Time) (Session, error) {
	s := Session{
		ID:          id,
		Name:        name,
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		CreatedAt:   at,
		UpdatedAt:   at,
		Generation:  0,
	}
	if err := s.Validate(); err != nil {
		return Session{}, err
	}
	return s, nil
}

// Current returns the current attempt and whether one exists.
func (s Session) Current() (Attempt, bool) {
	if s.Generation == 0 || len(s.Attempts) == 0 {
		return Attempt{}, false
	}
	for i := len(s.Attempts) - 1; i >= 0; i-- {
		if s.Attempts[i].Generation == s.Generation {
			return s.Attempts[i], true
		}
	}
	return Attempt{}, false
}

// AttemptCount returns the number of attempts.
func (s Session) AttemptCount() int { return len(s.Attempts) }

// AttemptHistory returns a deep copy of the attempt history, oldest first.
func (s Session) AttemptHistory() []Attempt {
	history := make([]Attempt, len(s.Attempts))
	for i, a := range s.Attempts {
		history[i] = a.Clone()
	}
	return history
}

// FindAttempt returns the attempt with the given generation.
func (s Session) FindAttempt(generation Generation) (Attempt, bool) {
	for _, a := range s.Attempts {
		if a.Generation == generation {
			return a.Clone(), true
		}
	}
	return Attempt{}, false
}

// State returns the observable triple of the current attempt, or the initial
// created/unavailable/unknown triple when no attempt exists yet.
func (s Session) State() State {
	if current, ok := s.Current(); ok {
		return current.State()
	}
	return initialState()
}

// Lifecycle returns the current lifecycle axis.
func (s Session) Lifecycle() Lifecycle { return s.State().Lifecycle }

// Orphaned reports the orphan annotation on the current attempt.
func (s Session) Orphaned() bool { return s.State().Orphaned() }

// Clone returns a deep copy, including every attempt and its notes.
func (s Session) Clone() Session {
	clone := s
	clone.Attempts = make([]Attempt, len(s.Attempts))
	for i, a := range s.Attempts {
		clone.Attempts[i] = a.Clone()
	}
	return clone
}

// Validate checks the session's identity and attempt history.
func (s Session) Validate() error {
	if !s.ID.Valid() {
		return fmt.Errorf("session id %q is not a valid id", string(s.ID))
	}
	if err := validateName(s.Name); err != nil {
		return err
	}
	if err := s.AgentID.Validate(); err != nil {
		return err
	}
	if err := s.WorkspaceID.Validate(); err != nil {
		return err
	}
	if s.CreatedAt.IsZero() {
		return fmt.Errorf("session %q has no creation timestamp", string(s.ID))
	}
	for i, a := range s.Attempts {
		if a.Generation != Generation(i+1) {
			return fmt.Errorf("session %q: attempt history is not contiguous: index %d holds generation %d",
				string(s.ID), i, a.Generation)
		}
		if err := a.Validate(); err != nil {
			return fmt.Errorf("session %q: %w", string(s.ID), err)
		}
	}
	if s.Generation != Generation(len(s.Attempts)) {
		return fmt.Errorf("session %q: current generation %d does not match %d recorded attempts",
			string(s.ID), s.Generation, len(s.Attempts))
	}
	if s.Generation > 0 {
		current, ok := s.Current()
		if !ok {
			return fmt.Errorf("session %q: generation %d has no attempt record", string(s.ID), s.Generation)
		}
		if !current.Terminal() {
			// Concurrency rule: never two concurrent attempts of one session.
			if i := len(s.Attempts) - 2; i >= 0 && !s.Attempts[i].Terminal() {
				return fmt.Errorf("session %q: attempt %d and %d are both non-terminal",
					string(s.ID), s.Attempts[i].Generation, current.Generation)
			}
		}
	}
	return nil
}

// EnsureDeletable refuses metadata deletion while the session still owns
// something (transition T20): delete requires lifecycle exited or failed.
func (s Session) EnsureDeletable() error {
	state := s.State()
	if !state.Lifecycle.Active() {
		return nil
	}
	return NewError(CodeConflict, string(s.ID),
		fmt.Sprintf("lifecycle %q is active; metadata can only be deleted after exited or failed", string(state.Lifecycle)),
		"stop or kill the session first, then delete its metadata").ForAttempt(s.Generation)
}

// SetActivity records a provider-evidenced activity, refusing to record working
// or idle without evidence.
func (s *Session) SetActivity(activity Activity, evidence Evidence) error {
	if _, ok := s.Current(); !ok {
		return NewError(CodeNotRunning, string(s.ID), "session has no attempt to annotate", "")
	}
	if err := ValidateActivity(activity, evidence); err != nil {
		return NewError(CodeUnsupported, string(s.ID), err.Error(),
			"only a documented provider signal may set working or idle")
	}
	current, _ := s.Current()
	current.Activity = activity
	if activity == ActivityUnknown {
		current.ActivityEvidence = Evidence{}
	} else {
		current.ActivityEvidence = evidence
	}
	s.replaceCurrent(current)
	return nil
}

// appendAttempt adds a new attempt to the history. Generations are contiguous, so
// appending is the only way an attempt is created.
func (s *Session) appendAttempt(attempt Attempt) {
	s.Attempts = append(s.Attempts, attempt.Clone())
}

// replaceCurrent swaps the current attempt for an updated copy.
func (s *Session) replaceCurrent(updated Attempt) {
	for i := len(s.Attempts) - 1; i >= 0; i-- {
		if s.Attempts[i].Generation == updated.Generation {
			s.Attempts[i] = updated.Clone()
			return
		}
	}
}

// validateName checks a session name.
func validateName(name string) error {
	if name == "" {
		return fmt.Errorf("session name is empty")
	}
	if len(name) > maxNameLen {
		return fmt.Errorf("session name is longer than %d bytes", maxNameLen)
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("session name %q has leading or trailing whitespace", name)
	}
	if strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return fmt.Errorf("session name %q contains control characters", name)
	}
	return nil
}
