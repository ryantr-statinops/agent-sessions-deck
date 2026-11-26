package app

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// History queries stored session history with filtering and pagination.
func (s *Service) History(ctx context.Context, req HistoryRequest) (HistoryResult, error) {
	select {
	case <-ctx.Done():
		return HistoryResult{}, ctx.Err()
	default:
	}

	if err := req.validate(); err != nil {
		return HistoryResult{}, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 20
	} else if limit > 1000 {
		limit = 1000
	}

	offset := req.Offset
	if offset < 0 {
		offset = 0
	}

	var sinceTime, untilTime time.Time
	if req.Since != "" {
		t, err := parseTimestamp(req.Since)
		if err != nil {
			return HistoryResult{}, fmt.Errorf("invalid since timestamp %q: %w", req.Since, err)
		}
		sinceTime = t
	}
	if req.Until != "" {
		t, err := parseTimestamp(req.Until)
		if err != nil {
			return HistoryResult{}, fmt.Errorf("invalid until timestamp %q: %w", req.Until, err)
		}
		untilTime = t
	}

	sessions, _, err := s.store.Load(ctx)
	if err != nil {
		return HistoryResult{}, fmt.Errorf("load sessions from store: %w", err)
	}

	var filtered []session.Session
	for _, sess := range sessions {
		if req.AgentID != "" && string(sess.AgentID) != req.AgentID {
			continue
		}
		if req.WorkspaceID != "" && string(sess.WorkspaceID) != req.WorkspaceID && !strings.Contains(string(sess.WorkspaceID), req.WorkspaceID) {
			continue
		}
		if req.Status == "active" && !sess.Lifecycle().Active() {
			continue
		}
		if req.Status == "finished" && !sess.Lifecycle().Terminal() {
			continue
		}
		if !sinceTime.IsZero() && sess.CreatedAt.Before(sinceTime) {
			continue
		}
		if !untilTime.IsZero() && sess.CreatedAt.After(untilTime) {
			continue
		}
		filtered = append(filtered, sess)
	}

	// Sort newest first by CreatedAt
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].CreatedAt.After(filtered[j].CreatedAt)
	})

	totalCount := len(filtered)
	start := offset
	if start > totalCount {
		start = totalCount
	}
	end := start + limit
	if end > totalCount {
		end = totalCount
	}

	paged := filtered[start:end]
	outSessions := make([]SessionHistory, len(paged))

	for i, sess := range paged {
		var attempts []AttemptHistory
		if req.IncludeAttempts {
			attempts = make([]AttemptHistory, len(sess.Attempts))
			for j, att := range sess.Attempts {
				var durStr string
				if !att.EndedAt.IsZero() && !att.StartedAt.IsZero() {
					durStr = att.EndedAt.Sub(att.StartedAt).Round(time.Millisecond).String()
				} else if !att.StartedAt.IsZero() {
					durStr = s.now().Sub(att.StartedAt).Round(time.Millisecond).String()
				}
				attempts[j] = AttemptHistory{
					Generation:  uint64(att.Generation),
					StartedAt:   att.StartedAt,
					RunningAt:   att.RunningAt,
					EndedAt:     att.EndedAt,
					Duration:    durStr,
					Lifecycle:   string(att.Lifecycle),
					Reason:      att.Reason.String(),
					ExitCode:    att.ExitCode,
					HasExitCode: att.HasExitCode,
					Command:     att.Command.String(),
				}
			}
		}

		outSessions[i] = SessionHistory{
			ID:           string(sess.ID),
			Name:         sess.Name,
			AgentID:      string(sess.AgentID),
			WorkspaceID:  string(sess.WorkspaceID),
			CreatedAt:    sess.CreatedAt,
			UpdatedAt:    sess.UpdatedAt,
			Lifecycle:    string(sess.Lifecycle()),
			Generation:   uint64(sess.Generation),
			AttemptCount: len(sess.Attempts),
			Attempts:     attempts,
		}
	}

	return HistoryResult{
		Authority:  s.authority,
		ObservedAt: s.now(),
		Sessions:   outSessions,
		TotalCount: totalCount,
	}, nil
}

func (req HistoryRequest) validate() error {
	switch req.Status {
	case "", "all", "active", "finished":
	default:
		return fmt.Errorf("invalid status filter %q: must be active, finished, or all", req.Status)
	}
	return nil
}

func parseTimestamp(val string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, val)
	if err == nil {
		return t.UTC(), nil
	}
	t, err = time.Parse(time.RFC3339, val)
	if err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, err
}
