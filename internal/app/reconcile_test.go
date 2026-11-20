package app

import (
	"context"
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

func createSessionWithIdentity(t *testing.T, id string, pid int) session.Session {
	t.Helper()
	s := sessionWithoutAttempt(t, id)
	cmd, err := agent.NewCommand("/bin/sh", nil)
	if err != nil {
		t.Fatalf("NewCommand: %v", err)
	}
	ident := testIdentity(pid)
	outcome := session.ApplyAll(s,
		session.Event{
			Kind:        session.EventLaunchRequested,
			Generation:  0,
			At:          baseTime,
			Command:     cmd,
			WorkspaceID: workspace.ID(testWorkspace),
		},
		session.Event{
			Kind:       session.EventSpawned,
			Generation: 1,
			At:         baseTime.Add(time.Second),
			Identity:   ident,
		},
		session.Event{
			Kind:       session.EventAttached,
			Generation: 1,
			At:         baseTime.Add(2 * time.Second),
		},
	)
	if !outcome.Applied {
		t.Fatalf("fixture ApplyAll failed: %v", outcome.Rejection)
	}
	if err := outcome.Session.Validate(); err != nil {
		t.Fatalf("fixture validate: %v", err)
	}
	return outcome.Session
}

func TestReconcileStartup(t *testing.T) {
	// 1. Session where process died while owner was away (ProbeGone)
	sessDead := createSessionWithIdentity(t, "sess-dead", 1001)

	// 2. Session where process survived (ProbeAlive)
	sessOrphan := createSessionWithIdentity(t, "sess-orphan", 1002)

	// 3. Session where PID was reused by another process (IdentityMismatch)
	sessStale := createSessionWithIdentity(t, "sess-stale", 1003)

	h := newHarness(t, withSeed(sessDead, sessOrphan, sessStale))

	h.runtime.observeFn = func(id session.ProcessIdentity) (session.LivenessObservation, error) {
		switch id.PID {
		case 1001:
			return session.LivenessObservation{
				Outcome: session.ProbeGone,
				At:      baseTime.Add(3 * time.Second),
			}, nil
		case 1002:
			return session.LivenessObservation{
				Outcome:  session.ProbeAlive,
				Identity: id,
				At:       baseTime.Add(3 * time.Second),
			}, nil
		case 1003:
			return session.LivenessObservation{
				Outcome: session.ProbeAlive,
				Identity: session.ProcessIdentity{
					PID:             1003,
					PGID:            9999, // different PGID & start time
					BootID:          "different-boot",
					StartTicks:      999999,
					OwnerInstanceID: "other-owner",
				},
				At: baseTime.Add(3 * time.Second),
			}, nil
		}
		return session.LivenessObservation{Outcome: session.ProbeUnverifiable, At: baseTime}, nil
	}

	summary, err := h.svc.ReconcileStartup(context.Background())
	if err != nil {
		t.Fatalf("ReconcileStartup failed: %v", err)
	}

	if summary.TotalChecked != 3 {
		t.Fatalf("expected 3 checked, got %d", summary.TotalChecked)
	}
	if summary.ReconciledDead != 1 {
		t.Fatalf("expected 1 ReconciledDead, got %d", summary.ReconciledDead)
	}
	if summary.ReconciledOrphan != 1 {
		t.Fatalf("expected 1 ReconciledOrphan, got %d", summary.ReconciledOrphan)
	}
	if summary.ReconciledStale != 1 {
		t.Fatalf("expected 1 ReconciledStale, got %d", summary.ReconciledStale)
	}

	// Verify states in engine
	rowDead, _ := h.svc.Get(context.Background(), GetRequest{Ref: "sess-dead"})
	if rowDead.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("sess-dead expected LifecycleExited, got %v", rowDead.Session.Lifecycle)
	}

	rowOrphan, _ := h.svc.Get(context.Background(), GetRequest{Ref: "sess-orphan"})
	if !rowOrphan.Session.Orphaned() {
		t.Fatalf("sess-orphan expected Orphaned == true, got %v", rowOrphan.Session.Orphaned())
	}

	rowStale, _ := h.svc.Get(context.Background(), GetRequest{Ref: "sess-stale"})
	if rowStale.Session.Lifecycle != session.LifecycleExited {
		t.Fatalf("sess-stale expected LifecycleExited, got %v", rowStale.Session.Lifecycle)
	}
}
