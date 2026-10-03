package app

import (
	"context"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// TestServiceIsUsableThroughTheClientInterface drives every use case through the
// Client interface, so a caller in another layer never needs a concrete service
// and cannot reach past the use cases into the ports.
func TestServiceIsUsableThroughTheClientInterface(t *testing.T) {
	h := newHarness(t)
	var client Client = h.svc

	scan, err := client.Scan(context.Background(), ScanRequest{})
	if err != nil || len(scan.Agents) != 1 {
		t.Fatalf("Scan = %+v, %v", scan, err)
	}
	created, err := client.Create(context.Background(), CreateRequest{
		Agent:         "opencode",
		WorkspacePath: testWorkspace,
		Name:          "kestrel",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := string(created.Session.ID)
	opened, err := client.Open(context.Background(), OpenRequest{Ref: id, Holder: "tui"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened.Terminal == nil || opened.Lease.Holder != "tui" {
		t.Fatalf("Open = %+v, want a stream and a lease", opened)
	}
	if _, err := client.Rename(context.Background(), RenameRequest{Ref: id, Name: "kestrel-final"}); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := client.Detach(context.Background(), DetachRequest{Ref: id}); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	h.runtime.stop = session.StopOutcome{Exited: true, ExitCode: intPtr(0)}
	if _, err := client.Stop(context.Background(), StopRequest{Ref: id}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	listed, err := client.List(context.Background(), ListRequest{})
	if err != nil || len(listed.Snapshot.Sessions) != 1 {
		t.Fatalf("List = %+v, %v", listed, err)
	}
	got, err := client.Get(context.Background(), GetRequest{Ref: id})
	if err != nil || got.Session.Name != "kestrel-final" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if _, err := client.Kill(context.Background(), KillRequest{Ref: id}); err == nil {
		t.Fatal("killing a stopped session must be refused")
	}
	if _, err := client.Restart(context.Background(), RestartRequest{Ref: id}); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if _, err := client.Stop(context.Background(), StopRequest{Ref: id}); err != nil {
		t.Fatalf("Stop after restart: %v", err)
	}
	deleted, err := client.Delete(context.Background(), DeleteRequest{Ref: id})
	if err != nil || deleted.ID != created.Session.ID {
		t.Fatalf("Delete = %+v, %v", deleted, err)
	}
	// Report is deliberately not on Client: only the process owner reports
	// observations, and no user-facing caller does.
	if _, err := h.svc.Report(context.Background(), ReportRequest{Ref: id, Kind: ReportChildExited, Reason: session.NaturalExit(0)}); err == nil {
		t.Fatal("a report for a deleted session must be refused")
	}
}

func TestRequestValidationRejectsEmptyReferences(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.Rename(context.Background(), RenameRequest{Name: "x"}); err == nil {
		t.Fatal("an empty reference must be refused")
	}
	if _, err := h.svc.Open(context.Background(), OpenRequest{Ref: "s-000001"}); err == nil {
		t.Fatal("an attach with no client name must be refused")
	}
	if _, err := h.svc.Kill(context.Background(), KillRequest{Ref: "s-000001", Signal: "SIGHUP"}); err == nil {
		t.Fatal("a signal this owner does not deliver must be refused")
	}
}
