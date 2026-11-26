package app

import (
	"context"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
)

type fakeJournal struct {
	appended []events.Event
}

func (f *fakeJournal) Append(ctx context.Context, ev events.Event) error {
	f.appended = append(f.appended, ev)
	return nil
}

func (f *fakeJournal) Query(ctx context.Context, filter events.JournalFilter) ([]events.Event, uint64, error) {
	var matched []events.Event
	for _, ev := range f.appended {
		if uint64(ev.Revision()) <= filter.AfterRevision {
			continue
		}
		if filter.SessionID != "" && ev.SessionID() != filter.SessionID {
			continue
		}
		if filter.Type != "" && ev.Type() != filter.Type {
			continue
		}
		matched = append(matched, ev)
		if filter.Limit > 0 && len(matched) >= filter.Limit {
			break
		}
	}
	var nextRev uint64 = filter.AfterRevision
	if len(matched) > 0 {
		nextRev = uint64(matched[len(matched)-1].Revision())
	}
	return matched, nextRev, nil
}

func (f *fakeJournal) LastRevision() uint64 {
	if len(f.appended) == 0 {
		return 0
	}
	return uint64(f.appended[len(f.appended)-1].Revision())
}

func TestServiceEventsQueryAndJournalHook(t *testing.T) {
	h := newHarness(t)
	fj := &fakeJournal{}
	h.svc.journal = fj

	// Perform a session creation to trigger publish hook
	created := h.createRunning(t)
	if len(fj.appended) == 0 {
		t.Fatalf("journal append was not triggered on session create")
	}

	// Query events via service
	res, err := h.svc.Events(context.Background(), EventsRequest{
		SessionID: string(created.Session.ID),
		Limit:     10,
	})
	if err != nil {
		t.Fatalf("Events query: %v", err)
	}
	if len(res.Events) == 0 {
		t.Fatalf("Events query returned 0 events; filter.SessionID was %q", created.Session.ID)
	}
	if res.Events[0].SessionID() != events.SessionID(created.Session.ID) {
		t.Fatalf("event session = %s, want %s", res.Events[0].SessionID(), created.Session.ID)
	}
	if res.NextRevision == 0 {
		t.Fatalf("next revision = 0")
	}
}
