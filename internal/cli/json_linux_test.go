//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func TestInspectReportsIOLivenessOnlyForTheCurrentOwner(t *testing.T) {
	live := session.SessionSnapshot{Authority: session.AuthorityLive, Lifecycle: session.LifecycleRunning, WorkspaceID: workspace.ID(t.TempDir()), HasIdentity: true, Identity: session.ProcessIdentity{OwnerInstanceID: "owner-a"}}
	current := inspectDocumentOf(context.Background(), app.GetResult{Session: live}, "owner-a")
	if current.IOAvailability != "available" {
		t.Fatalf("current owner I/O availability = %q", current.IOAvailability)
	}
	stored := live
	stored.Authority = session.AuthorityStored
	offline := inspectDocumentOf(context.Background(), app.GetResult{Session: stored}, "")
	if offline.IOAvailability != "unknown" {
		t.Fatalf("stored I/O availability = %q", offline.IOAvailability)
	}
}

func TestStopTimeoutJSONCarriesFailureAndStillRunningState(t *testing.T) {
	result := app.StopResult{TimedOut: true, Session: session.SessionSnapshot{ID: "session", Authority: session.AuthorityLive, Lifecycle: session.LifecycleRunning, ObservedAt: time.Now().UTC()}, Revision: 7}
	doc := stopDocumentOf(result)
	if doc.Code != session.CodeSessionIOFailed || doc.TimedOut == nil || !*doc.TimedOut || doc.Session.Lifecycle != session.LifecycleRunning {
		t.Fatalf("timeout JSON document = %+v", doc)
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"command", "authority", "observed_at", "revision", "session", "timed_out", "code", "reason", "hint"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("timeout JSON missing %q: %s", key, encoded)
		}
	}
}
