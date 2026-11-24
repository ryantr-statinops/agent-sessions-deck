package workspace

import (
	"errors"
	"time"
)

// WorkspaceSummary aggregates metadata and session counts for a single workspace.
type WorkspaceSummary struct {
	Workspace      Workspace `json:"workspace"`
	Git            *Git      `json:"git,omitempty"`
	ActiveCount    int       `json:"active_count"`
	TotalCount     int       `json:"total_count"`
	SessionIDs     []string  `json:"session_ids,omitempty"`
	LastObservedAt time.Time `json:"last_observed_at,omitempty"`
}

// Validate ensures the summary maintains internal consistency invariants.
func (s WorkspaceSummary) Validate() error {
	if s.Workspace.ID() == "" {
		return errors.New("workspace summary requires a non-empty workspace identity")
	}
	if s.ActiveCount < 0 {
		return errors.New("active count cannot be negative")
	}
	if s.TotalCount < 0 {
		return errors.New("total count cannot be negative")
	}
	if s.ActiveCount > s.TotalCount {
		return errors.New("active count cannot exceed total count")
	}
	if len(s.SessionIDs) != s.TotalCount {
		return errors.New("session IDs count does not match total count")
	}
	if s.Git != nil {
		if err := s.Git.Validate(); err != nil {
			return err
		}
	}
	return nil
}
