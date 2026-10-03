package session

import (
	"slices"
	"strings"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// Authority marks whether a snapshot was taken by the owner with live access or
// read back from persisted metadata.
//
// The distinction is normative: an offline reader must never present a stored
// `running` row as a live process (CLI contract, ADR 0003), so every
// snapshot and every session row carries its authority.
type Authority string

const (
	// AuthorityLive is an observation from the live owner.
	AuthorityLive Authority = "live"
	// AuthorityStored is metadata read back from the store, with an
	// observed-at timestamp and no liveness claim.
	AuthorityStored Authority = "stored"
)

// String returns the wire name of the authority.
func (a Authority) String() string { return string(a) }

// Valid reports whether the authority is one of the defined ones.
func (a Authority) Valid() bool { return a == AuthorityLive || a == AuthorityStored }

// SessionSnapshot is the read model of one session.
//
// It carries the identity, the observable triple, the current attempt's evidence
// and the authority of the observation, and nothing else: no raw terminal bytes
// and no environment secrets (ADR 0003).
type SessionSnapshot struct {
	// ID is the stable session identity.
	ID ID
	// Name is the user-visible label.
	Name string
	// AgentID is the agent definition.
	AgentID agent.ID
	// WorkspaceID is the canonical workspace identity.
	WorkspaceID workspace.ID
	// Authority is the authority of this row's observations.
	Authority Authority
	// ObservedAt is when these observations were taken, UTC.
	ObservedAt time.Time
	// StoreRevision is the store revision the snapshot was read at.
	StoreRevision uint64
	// Lifecycle, Attachment and Activity are the observable triple.
	Lifecycle  Lifecycle
	Attachment Attachment
	Activity   Activity
	// Generation is the attempt generation the row describes, 0 when the session
	// has no attempt yet.
	Generation Generation
	// AttemptCount is the number of attempts recorded for the session.
	AttemptCount int
	// Command is the immutable resolved command of the current attempt, zero when
	// the attempt has none.
	Command agent.Command
	// Identity is the current attempt's captured process identity.
	Identity ProcessIdentity
	// HasIdentity reports whether a verified identity was captured.
	HasIdentity bool
	// Reason is the current attempt's recorded cause.
	Reason Reason
	// ActivityEvidenced reports whether a provider signal justifies a non-unknown
	// activity. An unevidenced activity is always unknown, so a display must not
	// infer idleness from silence.
	ActivityEvidenced bool
	// Notes is a copy of the current attempt's diagnostic notes.
	Notes []string
	// LastSeenAt is when ASD last held positive liveness evidence.
	LastSeenAt time.Time
}

// Clone returns a deep copy.
func (s SessionSnapshot) Clone() SessionSnapshot {
	clone := s
	clone.Notes = slices.Clone(s.Notes)
	return clone
}

// Orphaned reports the orphan annotation on this row.
func (s SessionSnapshot) Orphaned() bool { return s.State().Orphaned() }

// State returns the observable triple of the row.
func (s SessionSnapshot) State() State {
	return State{Lifecycle: s.Lifecycle, Attachment: s.Attachment, Activity: s.Activity}
}

// ClaimsLiveness reports whether this row is a live claim that the process is
// running now. A stored row never claims liveness, however it was recorded.
func (s SessionSnapshot) ClaimsLiveness() bool {
	return s.Authority == AuthorityLive && s.Lifecycle == LifecycleRunning
}

// Deletable reports whether metadata deletion is allowed (transition T20). It is
// the read-model projection of Session.EnsureDeletable, for a client that holds
// only a snapshot and never the session itself; the enforcement stays with the
// session, which re-checks under the per-session lifecycle lock.
func (s SessionSnapshot) Deletable() bool { return s.Lifecycle.Terminal() }

// SessionSnapshotOf projects a session into the read model at one authority.
func SessionSnapshotOf(s Session, authority Authority, observedAt time.Time, revision uint64) SessionSnapshot {
	current, hasCurrent := s.Current()
	row := SessionSnapshot{
		ID:                s.ID,
		Name:              s.Name,
		AgentID:           s.AgentID,
		WorkspaceID:       s.WorkspaceID,
		Authority:         authority,
		ObservedAt:        observedAt,
		StoreRevision:     revision,
		Generation:        s.Generation,
		AttemptCount:      s.AttemptCount(),
		ActivityEvidenced: false,
		LastSeenAt:        s.LastSeenAt,
	}
	state := s.State()
	row.Lifecycle = state.Lifecycle
	row.Attachment = state.Attachment
	row.Activity = state.Activity
	if hasCurrent {
		row.Command = current.Command
		row.Identity = current.Identity
		row.HasIdentity = current.HasIdentity()
		row.Reason = current.Reason
		row.Notes = current.Notes()
		row.ActivityEvidenced = current.Activity != ActivityUnknown && current.ActivityEvidence.Valid()
	}
	if row.Activity != ActivityUnknown && !row.ActivityEvidenced {
		// Belt and braces: an unevidenced activity is never published.
		row.Activity = ActivityUnknown
	}
	return row
}

// Snapshot is the read model of a set of sessions at one revision.
type Snapshot struct {
	// Authority is the authority of every row in the snapshot.
	Authority Authority
	// ObservedAt is when the snapshot was taken, UTC.
	ObservedAt time.Time
	// Revision is the store revision the snapshot reflects.
	Revision uint64
	// Sessions is a copy of the session rows, ordered by session id.
	Sessions []SessionSnapshot
}

// NewSnapshot projects sessions into an immutable snapshot, ordered by id so two
// reads of the same state compare equal.
func NewSnapshot(authority Authority, observedAt time.Time, revision uint64, sessions []Session) (Snapshot, error) {
	if !authority.Valid() {
		return Snapshot{}, NewError(CodeInvalidConfiguration, "snapshot",
			"authority "+string(authority)+" is not live or stored", "use live or stored")
	}
	if observedAt.IsZero() {
		return Snapshot{}, NewError(CodeInvalidConfiguration, "snapshot",
			"observed-at timestamp is zero", "stamp the snapshot with the observation time")
	}
	rows := make([]SessionSnapshot, 0, len(sessions))
	for _, s := range sessions {
		if err := s.Validate(); err != nil {
			return Snapshot{}, WrapError(CodeCorruptState, string(s.ID), "session failed validation", "repair the stored record by hand", err)
		}
		rows = append(rows, SessionSnapshotOf(s, authority, observedAt, revision))
	}
	slices.SortFunc(rows, func(a, b SessionSnapshot) int {
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return Snapshot{Authority: authority, ObservedAt: observedAt, Revision: revision, Sessions: rows}, nil
}

// Find returns the row for a session id.
func (s Snapshot) Find(id ID) (SessionSnapshot, bool) {
	for _, row := range s.Sessions {
		if row.ID == id {
			return row.Clone(), true
		}
	}
	return SessionSnapshot{}, false
}

// Filter returns copies of the rows matching the predicate, in id order.
func (s Snapshot) Filter(keep func(SessionSnapshot) bool) []SessionSnapshot {
	var out []SessionSnapshot
	for _, row := range s.Sessions {
		if keep(row) {
			out = append(out, row.Clone())
		}
	}
	return out
}

// FindByPrefix resolves a session id prefix.
//
// An ambiguous prefix is refused with CONFLICT and the candidates, and the CLI
// never guesses which session was meant.
func (s Snapshot) FindByPrefix(prefix string) (SessionSnapshot, error) {
	var matches []SessionSnapshot
	for _, row := range s.Sessions {
		if strings.HasPrefix(string(row.ID), prefix) {
			matches = append(matches, row.Clone())
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return SessionSnapshot{}, NewError(CodeNotFound, prefix, "no session id matches this prefix", "list the sessions to see valid ids")
	default:
		ids := make([]string, 0, len(matches))
		for _, row := range matches {
			ids = append(ids, string(row.ID))
		}
		return SessionSnapshot{}, NewError(CodeConflict, prefix,
			"session id prefix is ambiguous: "+strings.Join(ids, ", "), "use a longer, unambiguous id prefix")
	}
}

// AsStored returns the same rows downgraded to stored authority.
//
// The observations and their timestamp are preserved, so a reader can still see
// what was last known; only the claim changes, and ClaimsLiveness becomes false.
func (s Snapshot) AsStored() Snapshot {
	downgraded := Snapshot{
		Authority:  AuthorityStored,
		ObservedAt: s.ObservedAt,
		Revision:   s.Revision,
		Sessions:   make([]SessionSnapshot, 0, len(s.Sessions)),
	}
	for _, row := range s.Sessions {
		row.Authority = AuthorityStored
		downgraded.Sessions = append(downgraded.Sessions, row)
	}
	return downgraded
}

// Validate checks the snapshot's internal consistency.
func (s Snapshot) Validate() error {
	if !s.Authority.Valid() {
		return NewError(CodeInvalidConfiguration, "snapshot",
			"authority "+string(s.Authority)+" is not live or stored", "")
	}
	if s.ObservedAt.IsZero() {
		return NewError(CodeInvalidConfiguration, "snapshot", "observed-at timestamp is zero", "")
	}
	for _, row := range s.Sessions {
		if err := row.State().Validate(); err != nil {
			return NewError(CodeCorruptState, string(row.ID), err.Error(), "repair the stored record by hand")
		}
		if row.Authority != s.Authority {
			return NewError(CodeCorruptState, string(row.ID),
				"row authority "+string(row.Authority)+" disagrees with snapshot authority "+string(s.Authority), "")
		}
	}
	return nil
}
