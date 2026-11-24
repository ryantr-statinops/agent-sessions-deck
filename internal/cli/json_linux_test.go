//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/metrics"
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

func TestWorkspacesDocumentJSON(t *testing.T) {
	wsDir := "/repo/sub"
	ws, _ := workspace.New(wsDir)
	ws, _ = ws.WithGit(workspace.Git{
		Root:   "/repo",
		Branch: "dev",
		Dirty:  true,
	})
	now := time.Now().UTC()
	result := app.WorkspacesResult{
		Authority:  session.AuthorityLive,
		ObservedAt: now,
		Workspaces: []workspace.WorkspaceSummary{
			{
				Workspace:      ws,
				Git:            &workspace.Git{Root: "/repo", Branch: "dev", Dirty: true},
				ActiveCount:    1,
				TotalCount:     2,
				SessionIDs:     []string{"s1", "s2"},
				LastObservedAt: now,
			},
		},
	}
	doc := workspacesDocumentOf(result)
	if doc.Command != "workspaces" || doc.Authority != session.AuthorityLive || len(doc.Workspaces) != 1 {
		t.Fatalf("unexpected workspaces doc: %+v", doc)
	}
	item := doc.Workspaces[0]
	if !item.IsSubdirectory {
		t.Fatalf("expected IsSubdirectory to be true for /repo/sub")
	}
	if item.ActiveCount != 1 || item.TotalCount != 2 || item.Git == nil || item.Git.Branch != "dev" {
		t.Fatalf("unexpected item fields: %+v", item)
	}

	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["command"] != "workspaces" {
		t.Fatalf("json command mismatch: %v", raw["command"])
	}
}

func TestInspectDocumentWithMetricsJSON(t *testing.T) {
	now := time.Now().UTC()
	result := app.GetResult{
		ObservedAt: now,
		Metrics: metrics.ProcessMetrics{
			SampledAt:      now,
			PID:            4129,
			PGID:           4129,
			State:          "S",
			Uptime:         120 * time.Second,
			CPUPercent:     12.5,
			MemoryRSSBytes: 52428800,
			TreeRSSBytes:   104857600,
			ProcessCount:   3,
		},
	}
	doc := inspectDocumentOf(context.Background(), result, "")
	if doc.Metrics == nil {
		t.Fatalf("expected doc.Metrics to be non-nil")
	}
	if doc.Metrics.PID != 4129 || doc.Metrics.CPUPercent != 12.5 || doc.Metrics.UptimeSeconds != 120.0 {
		t.Fatalf("unexpected doc.Metrics: %+v", doc.Metrics)
	}
}
