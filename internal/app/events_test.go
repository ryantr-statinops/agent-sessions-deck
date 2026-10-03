package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

func TestEventsFollowCommitOrderAndCarrySessionAttemptRevisionAndTime(t *testing.T) {
	h := newHarness(t)
	created := h.createRunning(t)
	if _, err := h.svc.Open(context.Background(), OpenRequest{Ref: "s-000001", Holder: "cli-1"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := h.svc.Rename(context.Background(), RenameRequest{Ref: "s-000001", Name: "kestrel-2"}); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(0)}
	if _, err := h.svc.Stop(context.Background(), StopRequest{Ref: "s-000001"}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if _, err := h.svc.Delete(context.Background(), DeleteRequest{Ref: "s-000001"}); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	wantTypes := []string{
		string(events.TypeSessionStarted),
		string(events.TypeSessionOpened),
		string(TypeSessionRenamed),
		string(events.TypeSessionStopped),
		string(TypeSessionDeleted),
	}
	captured := h.capture.all()
	if len(captured) != len(wantTypes) {
		t.Fatalf("published %d events, want %d: %v", len(captured), len(wantTypes), h.capture.types())
	}
	previous := events.Revision(0)
	for i, event := range captured {
		if string(event.spec.Type) != wantTypes[i] {
			t.Fatalf("event %d type = %s, want %s", i, event.spec.Type, wantTypes[i])
		}
		if event.revision <= previous {
			t.Fatalf("event %d revision = %d, want greater than %d", i, event.revision, previous)
		}
		previous = event.revision
		if event.spec.Session != events.SessionID("s-000001") {
			t.Fatalf("event %d session = %q", i, event.spec.Session)
		}
		if event.spec.Attempt != events.AttemptGeneration(1) {
			t.Fatalf("event %d attempt = %d, want 1", i, event.spec.Attempt)
		}
		if event.spec.Time.Time().IsZero() {
			t.Fatalf("event %d carries no timestamp", i)
		}
	}
	if captured[0].revision != events.Revision(created.Revision) {
		t.Fatalf("first event revision = %d, want the creation's %d", captured[0].revision, created.Revision)
	}
}

func TestRefusedMutationsPublishNothing(t *testing.T) {
	h := newHarness(t)
	h.createRunning(t)
	baseline := h.capture.types()

	tests := []struct {
		name string
		run  func() error
	}{
		{
			name: "open an unknown session",
			run: func() error {
				_, err := h.svc.Open(context.Background(), OpenRequest{Ref: "s-nope", Holder: "cli"})
				return err
			},
		},
		{
			name: "restart a live attempt without force",
			run: func() error {
				_, err := h.svc.Restart(context.Background(), RestartRequest{Ref: "s-000001"})
				return err
			},
		},
		{
			name: "delete a running session",
			run: func() error {
				_, err := h.svc.Delete(context.Background(), DeleteRequest{Ref: "s-000001"})
				return err
			},
		},
		{
			name: "rename with an unusable name",
			run: func() error {
				_, err := h.svc.Rename(context.Background(), RenameRequest{Ref: "s-000001", Name: " x "})
				return err
			},
		},
		{
			name: "kill a session whose forced kill is refused",
			run: func() error {
				h.runtime.killErr = errors.New("operation not permitted")
				defer func() { h.runtime.killErr = nil }()
				_, err := h.svc.Kill(context.Background(), KillRequest{Ref: "s-000001"})
				return err
			},
		},
		{
			name: "kill a session whose reap cannot be confirmed",
			run: func() error {
				h.runtime.kill = killDeliveredOnly()
				defer func() { h.runtime.kill = reapConfirmed(137) }()
				_, err := h.svc.Kill(context.Background(), KillRequest{Ref: "s-000001"})
				return err
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); err == nil {
				t.Fatal("the use case was expected to fail")
			}
			if got := h.capture.types(); len(got) != len(baseline) {
				t.Fatalf("published events = %v, want the unchanged %v", got, baseline)
			}
		})
	}
}

func TestEventMetadataCarriesNoTerminalBytesOrSecrets(t *testing.T) {
	h := newHarness(t)
	// An argv full of things a metadata event must never carry, plus a name and a
	// workspace that are perfectly printable.
	h.createRunning(t, "--token", "s3cret", "--env", "API_KEY=abc")

	captured := h.capture.all()
	if len(captured) != 1 {
		t.Fatalf("published events = %v, want one", h.capture.types())
	}
	meta := captured[0].spec.Metadata
	for _, key := range meta.Keys() {
		value, found := meta.Lookup(key)
		if !found {
			t.Fatalf("key %q has no value", key)
		}
		if key == "argv" || key == "command" || key == "env" || key == "token" {
			t.Fatalf("metadata carries the forbidden field %q", key)
		}
		if value.Kind() == events.KindString {
			text, _ := value.AsString()
			for _, forbidden := range []string{"s3cret", "API_KEY", "abc", "--token"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("metadata field %q leaks %q", key, forbidden)
				}
			}
		}
	}
	// The argv is reachable from the row, never from the event.
	if argv := captured[0].spec.Metadata.Format(); strings.Contains(argv, "s3cret") {
		t.Fatalf("metadata format leaks the command line: %s", argv)
	}
	for _, want := range []string{"lifecycle", "attachment", "generation", "store_revision", "observed_at", "transition"} {
		if _, found := meta.Lookup(want); !found {
			t.Fatalf("metadata is missing the %q field: %v", want, meta.Format())
		}
	}
}

func TestEventMetadataPublishesActivityOnlyWhenEvidenced(t *testing.T) {
	h := newHarness(t)
	h.createRunning(t)
	captured := h.capture.all()
	if _, found := captured[0].spec.Metadata.Lookup("activity"); found {
		t.Fatalf("unevidenced activity was published: %v", captured[0].spec.Metadata.Format())
	}
	if captured[0].spec.Metadata.Len() >= events.MaxMetadataFields {
		t.Fatal("the metadata set left no room for an evidenced activity")
	}
}

func TestPublicationFailureIsReportedAfterTheMutationStands(t *testing.T) {
	h := newHarness(t)
	h.capture.err = errors.New("subscriber registry is closed")
	committed := h.svc.Revision()

	result, err := h.svc.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: testWorkspace,
	})
	typed := requireCode(t, err, session.CodeUnknown)
	if !contains(typed.Reason, "committed") {
		t.Fatalf("reason = %q, must say the mutation is committed", typed.Reason)
	}
	if !contains(typed.Hint, "resynchronize") {
		t.Fatalf("hint = %q, must name the resync", typed.Hint)
	}
	if result.Session.ID == "" {
		t.Fatal("the result must still describe the committed session")
	}
	if result.Revision <= committed {
		t.Fatalf("revision = %d, want the committed revision above %d", result.Revision, committed)
	}
	if h.store.count() != 1 {
		t.Fatal("a publication failure must not undo the mutation")
	}
}

func TestEventStreamOrdersRevisionsThroughTheRealPublisher(t *testing.T) {
	publisher := realPublisher()
	h := newHarness(t)
	// Swap in the real publisher so the metadata policy, the revision validation
	// and the fan-out are the production ones.
	h.svc.events = publisher
	sub, err := publisher.Subscribe(context.Background(), 16)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	created := h.createRunning(t)
	if _, err := h.svc.Rename(context.Background(), RenameRequest{Ref: "s-000001", Name: "renamed"}); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := h.svc.Open(context.Background(), OpenRequest{Ref: "s-000001", Holder: "cli-1"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := h.svc.Detach(context.Background(), DetachRequest{Ref: "s-000001"}); err != nil {
		t.Fatalf("Detach: %v", err)
	}

	received := recvAll(t, sub, 4)
	if len(received) != 4 {
		t.Fatalf("received %d events, want 4", len(received))
	}
	previous := events.Revision(0)
	for _, notification := range received {
		event, ok := notification.Event()
		if !ok {
			t.Fatalf("notification %v carries no event", notification)
		}
		if event.Revision() <= previous {
			t.Fatalf("revision %d arrived after %d", event.Revision(), previous)
		}
		previous = event.Revision()
		if event.SessionID() != events.SessionID(created.Session.ID) {
			t.Fatalf("session = %q, want %q", event.SessionID(), created.Session.ID)
		}
		if generation, ok := event.AttemptGeneration(); !ok || generation != 1 {
			t.Fatalf("attempt = %d/%v, want 1", generation, ok)
		}
		if event.Timestamp().Time().Location() != time.UTC {
			t.Fatalf("timestamp %s is not UTC", event.Timestamp())
		}
		if event.ID() == "" {
			t.Fatal("the publisher minted no event id")
		}
	}
	// The creation event is the first revision, so the ordering is observable.
	if first, _ := received[0].Event(); first.Type() != events.TypeSessionStarted {
		t.Fatalf("first event = %s, want %s", first.Type(), events.TypeSessionStarted)
	}
}

func TestDetachAfterFailedOpenPublishesNothingExtra(t *testing.T) {
	h := newHarness(t)
	h.createRunning(t)
	if _, err := h.svc.Open(context.Background(), OpenRequest{Ref: "s-000001", Holder: "cli-1"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	baseline := len(h.capture.all())
	if _, err := h.svc.Detach(context.Background(), DetachRequest{Ref: "s-000001"}); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	// A second detach has nothing to release and nothing to announce.
	_, err := h.svc.Detach(context.Background(), DetachRequest{Ref: "s-000001"})
	requireCode(t, err, session.CodeConflict)
	if len(h.capture.all()) != baseline+1 {
		t.Fatalf("published %d events, want exactly the one detach", len(h.capture.all())-baseline)
	}
}

// recvAll reads n notifications from a subscription. The publisher has already
// fanned them out synchronously, so this never waits.
func recvAll(t *testing.T, sub *events.Subscription, n int) []events.Notification {
	t.Helper()
	out := make([]events.Notification, 0, n)
	for i := 0; i < n; i++ {
		notification, err := sub.Recv(context.Background())
		if err != nil {
			t.Fatalf("Recv %d: %v", i, err)
		}
		out = append(out, notification)
	}
	return out
}
