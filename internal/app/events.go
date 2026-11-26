package app

import (
	"context"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
)

// Events queries persistent events from the event journal.
func (s *Service) Events(ctx context.Context, req EventsRequest) (EventsResult, error) {
	select {
	case <-ctx.Done():
		return EventsResult{}, ctx.Err()
	default:
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	} else if limit > 1000 {
		limit = 1000
	}

	if s.journal == nil {
		return EventsResult{
			Authority:    s.authority,
			ObservedAt:   s.now(),
			Events:       nil,
			NextRevision: req.AfterRevision,
		}, nil
	}

	filter := events.JournalFilter{
		SessionID:     events.SessionID(req.SessionID),
		Type:          events.Type(req.Type),
		AfterRevision: req.AfterRevision,
		Limit:         limit,
	}

	evs, nextRev, err := s.journal.Query(ctx, filter)
	if err != nil {
		return EventsResult{}, err
	}

	return EventsResult{
		Authority:    s.authority,
		ObservedAt:   s.now(),
		Events:       evs,
		NextRevision: nextRev,
	}, nil
}
