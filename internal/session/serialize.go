package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// The JSON below is the durable form of session metadata: ADR 0003's "one
// transaction updates logical Session + Attempt", expressed as an explicit wire
// struct per record rather than as whatever the struct tags happen to produce.
//
// It exists because the domain has private state. The attempt's diagnostic notes
// and the resolved command's argv are the two fields a stored record must never
// lose - the notes carry T9's kill guidance and the orphan annotation across an
// owner restart, and the frozen argv is the only record of what was launched - and
// both are invisible to the default encoder. So each record declares its own wire
// form, and decoding is strict and validating: an unknown field is a schema drift
// to refuse rather than a field to drop, and a record that fails Validate never
// becomes a session.

// errTrailingJSON refuses a document with more than one value in it, so a
// truncated or concatenated store file cannot be read as a complete one.
var errTrailingJSON = errors.New("trailing data after the stored record")

// attemptJSON is the durable wire form of one attempt.
type attemptJSON struct {
	Generation       Generation      `json:"generation"`
	Command          agent.Command   `json:"command"`
	WorkspaceID      workspace.ID    `json:"workspace_id"`
	OwnerInstanceID  string          `json:"owner_instance_id"`
	StartedAt        time.Time       `json:"started_at"`
	RunningAt        time.Time       `json:"running_at"`
	EndedAt          time.Time       `json:"ended_at"`
	Lifecycle        Lifecycle       `json:"lifecycle"`
	Attachment       Attachment      `json:"attachment"`
	Activity         Activity        `json:"activity"`
	ActivityEvidence Evidence        `json:"activity_evidence"`
	Reason           Reason          `json:"reason"`
	Identity         ProcessIdentity `json:"identity"`
	ExitCode         int             `json:"exit_code"`
	HasExitCode      bool            `json:"has_exit_code"`
	Notes            []string        `json:"notes"`
}

// attemptWire projects an attempt onto its durable wire form.
func attemptWire(a Attempt) attemptJSON {
	notes := a.notes
	if notes == nil {
		notes = []string{}
	}
	return attemptJSON{
		Generation:       a.Generation,
		Command:          a.Command,
		WorkspaceID:      a.WorkspaceID,
		OwnerInstanceID:  a.OwnerInstanceID,
		StartedAt:        a.StartedAt,
		RunningAt:        a.RunningAt,
		EndedAt:          a.EndedAt,
		Lifecycle:        a.Lifecycle,
		Attachment:       a.Attachment,
		Activity:         a.Activity,
		ActivityEvidence: a.ActivityEvidence,
		Reason:           a.Reason,
		Identity:         a.Identity,
		ExitCode:         a.ExitCode,
		HasExitCode:      a.HasExitCode,
		Notes:            notes,
	}
}

// attemptFromWire rebuilds an attempt from its durable wire form. It is the only
// way an attempt is deserialized, so the validating UnmarshalJSON below cannot be
// bypassed.
func attemptFromWire(wire attemptJSON) (Attempt, error) {
	restored := Attempt{
		Generation:       wire.Generation,
		Command:          wire.Command,
		WorkspaceID:      wire.WorkspaceID,
		OwnerInstanceID:  wire.OwnerInstanceID,
		StartedAt:        wire.StartedAt,
		RunningAt:        wire.RunningAt,
		EndedAt:          wire.EndedAt,
		Lifecycle:        wire.Lifecycle,
		Attachment:       wire.Attachment,
		Activity:         wire.Activity,
		ActivityEvidence: wire.ActivityEvidence,
		Reason:           wire.Reason,
		Identity:         wire.Identity,
		ExitCode:         wire.ExitCode,
		HasExitCode:      wire.HasExitCode,
	}
	// setNote keeps the notes unique and in order, exactly as the reducer does.
	for _, note := range wire.Notes {
		restored.setNote(note)
	}
	if err := restored.Validate(); err != nil {
		return Attempt{}, err
	}
	return restored, nil
}

// MarshalJSON renders the attempt, including its notes, as durable JSON.
func (a Attempt) MarshalJSON() ([]byte, error) {
	return json.Marshal(attemptWire(a))
}

// UnmarshalJSON restores an attempt and validates it. A record that lost a field
// or carries a state the contract forbids is refused here, not adopted.
func (a *Attempt) UnmarshalJSON(data []byte) error {
	var wire attemptJSON
	if err := decodeStrictJSON(data, &wire); err != nil {
		return err
	}
	restored, err := attemptFromWire(wire)
	if err != nil {
		return err
	}
	*a = restored
	return nil
}

// sessionJSON is the durable wire form of one logical session.
type sessionJSON struct {
	ID          ID            `json:"id"`
	Name        string        `json:"name"`
	AgentID     agent.ID      `json:"agent_id"`
	WorkspaceID workspace.ID  `json:"workspace_id"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	LastSeenAt  time.Time     `json:"last_seen_at"`
	Generation  Generation    `json:"generation"`
	Attempts    []attemptJSON `json:"attempts"`
}

// sessionFromWire rebuilds a session from its durable wire form, validating every
// attempt and then the session as a whole.
func sessionFromWire(wire sessionJSON) (Session, error) {
	restored := Session{
		ID:          wire.ID,
		Name:        wire.Name,
		AgentID:     wire.AgentID,
		WorkspaceID: wire.WorkspaceID,
		CreatedAt:   wire.CreatedAt,
		UpdatedAt:   wire.UpdatedAt,
		LastSeenAt:  wire.LastSeenAt,
		Generation:  wire.Generation,
	}
	for _, encoded := range wire.Attempts {
		attempt, err := attemptFromWire(encoded)
		if err != nil {
			return Session{}, err
		}
		restored.Attempts = append(restored.Attempts, attempt)
	}
	if err := restored.Validate(); err != nil {
		return Session{}, err
	}
	return restored, nil
}

// MarshalJSON renders the session and its whole attempt history as durable JSON.
func (s Session) MarshalJSON() ([]byte, error) {
	attempts := make([]attemptJSON, 0, len(s.Attempts))
	for _, attempt := range s.Attempts {
		attempts = append(attempts, attemptWire(attempt))
	}
	return json.Marshal(sessionJSON{
		ID:          s.ID,
		Name:        s.Name,
		AgentID:     s.AgentID,
		WorkspaceID: s.WorkspaceID,
		CreatedAt:   s.CreatedAt,
		UpdatedAt:   s.UpdatedAt,
		LastSeenAt:  s.LastSeenAt,
		Generation:  s.Generation,
		Attempts:    attempts,
	})
}

// UnmarshalJSON restores a session and validates it, attempt by attempt.
func (s *Session) UnmarshalJSON(data []byte) error {
	var wire sessionJSON
	if err := decodeStrictJSON(data, &wire); err != nil {
		return err
	}
	restored, err := sessionFromWire(wire)
	if err != nil {
		return err
	}
	*s = restored
	return nil
}

// DecodeSession reads one session from durable JSON and validates it. A failure is
// reported as CORRUPT_STATE, because a caller reading a store cannot fix the record
// by retrying and must not adopt it half-read.
func DecodeSession(r io.Reader) (Session, error) {
	var restored Session
	if err := decodeStrictJSONFrom(r, &restored); err != nil {
		return Session{}, WrapError(CodeCorruptState, "session",
			"the stored session record could not be read", "repair or remove the record by hand", err)
	}
	return restored, nil
}

// DecodeSessions reads every session of one store revision.
func DecodeSessions(r io.Reader) ([]Session, error) {
	var stored []Session
	if err := decodeStrictJSONFrom(r, &stored); err != nil {
		return nil, WrapError(CodeCorruptState, "sessions",
			"the stored session records could not be read", "repair or remove the records by hand", err)
	}
	return stored, nil
}

// EncodeSessions renders every session of one store revision as one JSON array.
func EncodeSessions(w io.Writer, sessions []Session) error {
	encoded := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		encoded = append(encoded, session)
	}
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(encoded); err != nil {
		return WrapError(CodeCorruptState, "sessions",
			"the session records could not be written", "report this as a contract bug", err)
	}
	return nil
}

// decodeStrictJSON decodes one JSON value, refusing unknown fields and trailing
// data so a schema drift can never be read back as a valid record.
func decodeStrictJSON(data []byte, target any) error {
	return decodeStrictJSONFrom(bytes.NewReader(data), target)
}

func decodeStrictJSONFrom(r io.Reader, target any) error {
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.More() {
		return errTrailingJSON
	}
	return nil
}
