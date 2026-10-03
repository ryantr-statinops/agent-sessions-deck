package session

import (
	"testing"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

var (
	// baseTime is a fixed UTC instant so every timestamp assertion is
	// deterministic and independent of the wall clock.
	baseTime = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

	testSessionID = ID("01JQASDWSDECK0001")
	testWorkspace = workspace.ID("/home/tester/kestrel")
	testAgentID   = agent.ID("opencode")
)

// testIdentity is a complete ProcessIdentity: a PID alone would never be enough.
var testIdentity = ProcessIdentity{
	PID:             4242,
	PGID:            4242,
	BootID:          "boot-a1b2c3",
	StartTicks:      987654,
	OwnerInstanceID: "owner-1",
}

// foreignIdentity is the same pid recycled by another boot and owner, the shape a
// pid-reuse fixture produces.
var foreignIdentity = ProcessIdentity{
	PID:             4242,
	PGID:            5151,
	BootID:          "boot-d4e5f6",
	StartTicks:      111222,
	OwnerInstanceID: "owner-9",
}

func at(seconds int) time.Time { return baseTime.Add(time.Duration(seconds) * time.Second) }

func mustCommand(t *testing.T, executable string, args ...string) agent.Command {
	t.Helper()
	cmd, err := agent.NewCommand(executable, args)
	if err != nil {
		t.Fatalf("NewCommand(%q, %q): %v", executable, args, err)
	}
	return cmd
}

func launchCommand(t *testing.T) agent.Command { return mustCommand(t, "/usr/local/bin/opencode") }

// freshSession returns a session record with no attempt: lifecycle created.
func freshSession(t *testing.T) Session {
	t.Helper()
	s, err := New(testSessionID, "kestrel run", testAgentID, testWorkspace, baseTime)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// startingSession applies T2 so the attempt exists in lifecycle starting.
func startingSession(t *testing.T) Session {
	t.Helper()
	outcome := Reduce(freshSession(t), Event{
		Kind:        EventLaunchRequested,
		Generation:  0,
		At:          at(0),
		Command:     launchCommand(t),
		WorkspaceID: testWorkspace,
	})
	mustApply(t, outcome)
	return outcome.Session
}

// runningSession applies T2 and T3, so the attempt has a verified identity.
func runningSession(t *testing.T) Session {
	t.Helper()
	outcome := Reduce(startingSession(t), Event{
		Kind:       EventSpawned,
		Generation: 1,
		At:         at(10),
		Identity:   testIdentity,
	})
	mustApply(t, outcome)
	return outcome.Session
}

// stoppingSession applies T7 on top of running.
func stoppingSession(t *testing.T) Session {
	t.Helper()
	outcome := Reduce(runningSession(t), Event{
		Kind:       EventStopRequested,
		Generation: 1,
		At:         at(20),
	})
	mustApply(t, outcome)
	return outcome.Session
}

// stopTimeoutSession applies T9 on top of stopping, so the attempt is running
// again carrying the stop-timeout and kill-guidance notes.
func stopTimeoutSession(t *testing.T) Session {
	t.Helper()
	outcome := Reduce(stoppingSession(t), Event{
		Kind:       EventStopTimeout,
		Generation: 1,
		At:         at(40),
	})
	mustApply(t, outcome)
	return outcome.Session
}

// unknownSession applies T18 on top of running: liveness could not be established.
func unknownSession(t *testing.T) Session {
	t.Helper()
	outcome := Reduce(runningSession(t), Event{
		Kind:       EventProbeInconclusive,
		Generation: 1,
		At:         at(20),
		Liveness: LivenessObservation{
			Outcome: ProbeUnverifiable,
			Detail:  "probe-timeout",
			At:      at(20),
		},
	})
	mustApply(t, outcome)
	return outcome.Session
}

// orphanSession applies T14: the process is verified alive but the PTY is gone.
func orphanSession(t *testing.T) Session {
	t.Helper()
	outcome := Reduce(runningSession(t), Event{
		Kind:       EventPTYLost,
		Generation: 1,
		At:         at(20),
		Liveness: LivenessObservation{
			Outcome:  ProbeAlive,
			Identity: testIdentity,
			At:       at(20),
		},
	})
	mustApply(t, outcome)
	return outcome.Session
}

// exitedSession applies T6: a natural exit with a nonzero code.
func exitedSession(t *testing.T) Session {
	t.Helper()
	outcome := Reduce(runningSession(t), Event{
		Kind:       EventChildExited,
		Generation: 1,
		At:         at(30),
		Reason:     NaturalExit(3),
	})
	mustApply(t, outcome)
	return outcome.Session
}

// failedSession applies T1: launch validation failed, so there is no child.
func failedSession(t *testing.T) Session {
	t.Helper()
	outcome := Reduce(freshSession(t), Event{
		Kind:       EventLaunchRequested,
		Generation: 0,
		At:         at(0),
		Failure:    "agent binary not found",
	})
	mustApply(t, outcome)
	return outcome.Session
}

func mustApply(t *testing.T, outcome Outcome) {
	t.Helper()
	if !outcome.Applied {
		t.Fatalf("event %s was rejected: %v", outcome.Row, outcome.Err())
	}
	if err := outcome.Validate(); err != nil {
		t.Fatalf("reducer produced an invalid session: %v", err)
	}
}
