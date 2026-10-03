package events

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestEventCarriesTheContractFields(t *testing.T) {
	publisher := newTestPublisher(t)
	meta, err := Metadata{}.WithString("session_name", "reviewer")
	if err != nil {
		t.Fatalf("WithString: %v", err)
	}
	meta, err = meta.WithString("lifecycle", "running")
	if err != nil {
		t.Fatalf("WithString: %v", err)
	}

	event, err := publisher.Publish(Spec{
		ID:       "evt-fixed",
		Type:     TypeSessionStarted,
		Session:  "s-42",
		Attempt:  3,
		Metadata: meta,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if event.ID() != "evt-fixed" {
		t.Fatalf("ID() = %q, want evt-fixed", event.ID())
	}
	if event.Type() != TypeSessionStarted {
		t.Fatalf("Type() = %q, want %q", event.Type(), TypeSessionStarted)
	}
	if event.SessionID() != "s-42" {
		t.Fatalf("SessionID() = %q, want s-42", event.SessionID())
	}
	attempt, ok := event.AttemptGeneration()
	if !ok || attempt != 3 {
		t.Fatalf("AttemptGeneration() = %d, %v, want 3, true", attempt, ok)
	}
	if event.Revision() != 1 {
		t.Fatalf("Revision() = %d, want 1", event.Revision())
	}
	if event.Timestamp().Time().Location() != time.UTC {
		t.Fatalf("Timestamp() location = %s, want UTC", event.Timestamp().Time().Location())
	}
	if !event.Metadata().Equal(meta) {
		t.Fatalf("Metadata() = %s, want %s", event.Metadata().Format(), meta.Format())
	}
}

func TestEventAttemptGenerationIsOptional(t *testing.T) {
	publisher := newTestPublisher(t)
	event, err := publisher.Publish(Spec{Type: TypeSessionCreated, Session: "s-42"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if attempt, ok := event.AttemptGeneration(); ok || attempt != 0 {
		t.Fatalf("AttemptGeneration() = %d, %v, want 0, false for a session-level event", attempt, ok)
	}
	if event.SessionID() != "s-42" {
		t.Fatalf("SessionID() = %q, want s-42", event.SessionID())
	}

	notSessionScoped, err := publisher.Publish(Spec{Type: TypeWorkspaceChanged})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if notSessionScoped.SessionID() != "" {
		t.Fatalf("SessionID() = %q, want empty for a non-session event", notSessionScoped.SessionID())
	}
}

func TestEventTimestampIsUTCAndDefaultsToThePublisherClock(t *testing.T) {
	publisher := newTestPublisher(t)
	stamped, err := publisher.Publish(Spec{
		Type: TypeSessionCreated,
		Time: NewTimestamp(time.Date(2026, time.March, 4, 7, 6, 7, 0, time.FixedZone("plus2", 2*60*60))),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	want := time.Date(2026, time.March, 4, 5, 6, 7, 0, time.UTC)
	if !stamped.Timestamp().Time().Equal(want) {
		t.Fatalf("Timestamp() = %s, want %s", stamped.Timestamp(), want)
	}
	if stamped.Timestamp().String() != want.Format(time.RFC3339Nano) {
		t.Fatalf("Timestamp().String() = %s, want RFC 3339 UTC", stamped.Timestamp())
	}

	clocked, err := publisher.Publish(Spec{Type: TypeSessionCreated})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if clocked.Timestamp().Time().IsZero() {
		t.Fatal("Publish did not stamp a timestamp from the publisher clock")
	}
	if clocked.Timestamp().Time().Location() != time.UTC {
		t.Fatalf("clocked Timestamp() location = %s, want UTC", clocked.Timestamp().Time().Location())
	}
}

func TestPublishMintsStableIDsByDefault(t *testing.T) {
	publisher := NewPublisher(WithClock((&fixedClock{base: time.Unix(0, 0).UTC()}).now))
	first, err := publisher.Publish(Spec{Type: TypeAgentDetected})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	second, err := publisher.Publish(Spec{Type: TypeAgentDetected})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if first.ID() != "evt-000001" || second.ID() != "evt-000002" {
		t.Fatalf("minted IDs = %q, %q, want evt-000001, evt-000002", first.ID(), second.ID())
	}
	if strings.TrimSpace(first.String()) == "" {
		t.Fatal("Event.String() is empty")
	}
}

func TestPublishRejectsInvalidSpec(t *testing.T) {
	tests := []struct {
		name    string
		spec    Spec
		wantErr error
	}{
		{name: "empty type", spec: Spec{Session: "s-42"}, wantErr: ErrInvalidType},
		{name: "session id with a control byte", spec: Spec{Type: TypeSessionCreated, Session: "s\x1b[42m"}, wantErr: ErrInvalidID},
		{name: "session id with invalid utf8", spec: Spec{Type: TypeSessionCreated, Session: "s\xff"}, wantErr: ErrInvalidID},
		{name: "session id with padding", spec: Spec{Type: TypeSessionCreated, Session: " s-42 "}, wantErr: ErrInvalidID},
		{name: "event id with a control byte", spec: Spec{ID: "evt\x00", Type: TypeSessionCreated}, wantErr: ErrInvalidID},
	}

	publisher := newTestPublisher(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := publisher.Publish(tt.spec); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Publish(%+v) error = %v, want %v", tt.spec, err, tt.wantErr)
			}
			if got := publisher.Revision(); got != 0 {
				t.Fatalf("Revision() = %d after a rejected spec, want 0", got)
			}
		})
	}
}

func TestEventRenderingOmitsMetadataPayloads(t *testing.T) {
	publisher := newTestPublisher(t)
	meta, err := Metadata{}.WithString("session_name", "super-secret-reviewer")
	if err != nil {
		t.Fatalf("WithString: %v", err)
	}
	event, err := publisher.Publish(Spec{Type: TypeSessionStarted, Session: "s-42", Attempt: 2, Metadata: meta})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	rendered := event.String()
	if strings.Contains(rendered, "super-secret-reviewer") {
		t.Fatalf("Event.String() = %q, want no metadata payload", rendered)
	}
	for _, want := range []string{"session.started", "s-42", "attempt=2", "revision=1"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("Event.String() = %q, want it to mention %q", rendered, want)
		}
	}
	notification := Notification{event: event}
	if strings.Contains(notification.String(), "super-secret-reviewer") {
		t.Fatalf("Notification.String() = %q, want no metadata payload", notification.String())
	}
}
