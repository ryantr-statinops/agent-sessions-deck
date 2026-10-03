package session

import (
	"errors"
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// TestProcessIdentityRequiresEveryComponent keeps a bare PID from ever being
// treated as proof of ownership.
func TestProcessIdentityRequiresEveryComponent(t *testing.T) {
	cases := []struct {
		name     string
		identity ProcessIdentity
		wantErr  bool
	}{
		{name: "complete identity", identity: testIdentity},
		{name: "no pid", identity: ProcessIdentity{PGID: 1, BootID: "b", StartTicks: 1, OwnerInstanceID: "o"}, wantErr: true},
		{name: "no pgid", identity: ProcessIdentity{PID: 1, BootID: "b", StartTicks: 1, OwnerInstanceID: "o"}, wantErr: true},
		{name: "no boot id", identity: ProcessIdentity{PID: 1, PGID: 1, StartTicks: 1, OwnerInstanceID: "o"}, wantErr: true},
		{name: "no start ticks", identity: ProcessIdentity{PID: 1, PGID: 1, BootID: "b", OwnerInstanceID: "o"}, wantErr: true},
		{name: "no owner instance", identity: ProcessIdentity{PID: 1, PGID: 1, BootID: "b", StartTicks: 1}, wantErr: true},
		{name: "zero value", identity: ProcessIdentity{}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.identity.Validate()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("identity %s was accepted", tc.identity.Describe())
				}
				var invalid *InvalidIdentityError
				if !errors.As(err, &invalid) {
					t.Fatalf("error %v is not typed", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("complete identity was refused: %v", err)
			}
		})
	}

	if testIdentity.Equal(foreignIdentity) {
		t.Fatalf("a recycled pid compared equal to the recorded identity")
	}
}

// TestClassifyFailsClosed walks the identity verdicts behind T14-T18.
func TestClassifyFailsClosed(t *testing.T) {
	cases := []struct {
		name        string
		recorded    ProcessIdentity
		observation LivenessObservation
		want        Classification
	}{
		{
			name:        "verified live child",
			recorded:    testIdentity,
			observation: LivenessObservation{Outcome: ProbeAlive, Identity: testIdentity, At: at(1)},
			want:        ClassificationVerified,
		},
		{
			name:        "same pid after reboot is stale",
			recorded:    testIdentity,
			observation: LivenessObservation{Outcome: ProbeAlive, Identity: ProcessIdentity{PID: testIdentity.PID, PGID: testIdentity.PGID, BootID: "boot-next", StartTicks: testIdentity.StartTicks, OwnerInstanceID: testIdentity.OwnerInstanceID}, At: at(1)},
			want:        ClassificationStale,
		},
		{
			name:        "record from another owner is stale",
			recorded:    testIdentity,
			observation: LivenessObservation{Outcome: ProbeAlive, Identity: ProcessIdentity{PID: testIdentity.PID, PGID: testIdentity.PGID, BootID: testIdentity.BootID, StartTicks: testIdentity.StartTicks, OwnerInstanceID: "owner-2"}, At: at(1)},
			want:        ClassificationStale,
		},
		{
			name:        "gone process",
			recorded:    testIdentity,
			observation: LivenessObservation{Outcome: ProbeGone, At: at(1)},
			want:        ClassificationGone,
		},
		{
			name:        "timeout keeps the record unverifiable",
			recorded:    testIdentity,
			observation: LivenessObservation{Outcome: ProbeUnverifiable, Detail: "probe-timeout", At: at(1)},
			want:        ClassificationUnverifiable,
		},
		{
			name:        "incomplete record is unverifiable",
			recorded:    ProcessIdentity{PID: 9},
			observation: LivenessObservation{Outcome: ProbeAlive, Identity: ProcessIdentity{PID: 9, PGID: 9, BootID: "b", StartTicks: 9, OwnerInstanceID: "o"}, At: at(1)},
			want:        ClassificationUnverifiable,
		},
		{
			name:        "malformed observation is unverifiable",
			recorded:    testIdentity,
			observation: LivenessObservation{Outcome: ProbeAlive, Identity: testIdentity},
			want:        ClassificationUnverifiable,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.recorded.Classify(tc.observation); got != tc.want {
				t.Fatalf("classification = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestLivenessObservationValidation keeps an unusable probe from reaching the
// reducer.
func TestLivenessObservationValidation(t *testing.T) {
	cases := []struct {
		name        string
		observation LivenessObservation
		wantErr     bool
	}{
		{name: "alive with identity", observation: LivenessObservation{Outcome: ProbeAlive, Identity: testIdentity, At: at(1)}},
		{name: "alive without identity", observation: LivenessObservation{Outcome: ProbeAlive, At: at(1)}, wantErr: true},
		{name: "unverifiable without detail", observation: LivenessObservation{Outcome: ProbeUnverifiable, At: at(1)}, wantErr: true},
		{name: "unverifiable with detail", observation: LivenessObservation{Outcome: ProbeUnverifiable, Detail: "probe-timeout", At: at(1)}},
		{name: "missing timestamp", observation: LivenessObservation{Outcome: ProbeGone}, wantErr: true},
		{name: "unknown outcome", observation: LivenessObservation{Outcome: "maybe", At: at(1)}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.observation.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("validation error = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}

// TestEvidenceLinePreservesTheOriginalObservation proves the raw observation
// survives beside the derived state.
func TestEvidenceLinePreservesTheOriginalObservation(t *testing.T) {
	gone := LivenessObservation{Outcome: ProbeGone, At: at(1)}.EvidenceLine()
	if gone != "liveness=gone" {
		t.Fatalf("evidence line = %q", gone)
	}
	alive := LivenessObservation{Outcome: ProbeAlive, Identity: testIdentity, At: at(1)}.EvidenceLine()
	if alive != "liveness=alive observed="+testIdentity.Describe() {
		t.Fatalf("evidence line = %q", alive)
	}
	denied := LivenessObservation{Outcome: ProbeUnverifiable, Detail: "permission-denied", At: at(1)}.EvidenceLine()
	if denied != "liveness=unverifiable detail=permission-denied" {
		t.Fatalf("evidence line = %q", denied)
	}
}

// TestReasonValidationKeepsExitCodesDistinct.
func TestReasonValidationKeepsExitCodesDistinct(t *testing.T) {
	if _, ok := NaturalExit(0).ExitStatus(); !ok {
		t.Fatalf("exit code 0 must be recorded as an observed code")
	}
	if _, ok := Killed("SIGKILL").ExitStatus(); ok {
		t.Fatalf("a kill must not carry an exit code")
	}
	if err := NaturalExit(0).Validate(); err != nil {
		t.Fatalf("a natural exit without detail was refused: %v", err)
	}
	noCode := Reason{Kind: ReasonNaturalExit}
	if err := noCode.Validate(); err == nil {
		t.Fatalf("a natural exit without an exit code was accepted")
	}
	withCode := Reason{Kind: ReasonKilled, ExitCode: intPtr(0)}
	if err := withCode.Validate(); err == nil {
		t.Fatalf("a kill carrying an exit code was accepted")
	}
	if !NaturalExit(1).Equal(NaturalExit(1)) || NaturalExit(1).Equal(NaturalExit(2)) {
		t.Fatalf("reason equality is wrong")
	}
	if NaturalExit(1).Equal(Killed("SIGKILL")) {
		t.Fatalf("a natural exit compared equal to a kill")
	}
}

// TestCodeOfTranslatesDomainErrors keeps one taxonomy for user-visible failures.
func TestCodeOfTranslatesDomainErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Code
	}{
		{name: "nil", err: nil, want: ""},
		{name: "typed session error", err: NewError(CodeOwnerUnavailable, "owner", "no owner", "start one"), want: CodeOwnerUnavailable},
		{name: "rejection", err: &Rejection{Err: NewError(CodeConflict, "s", "active", "stop it")}, want: CodeConflict},
		{name: "agent unsupported capability", err: &agent.UnsupportedError{AgentID: "claude", Capability: agent.CapabilityResume}, want: CodeUnsupported},
		{name: "agent invalid command", err: &agent.InvalidCommandError{Problem: "executable is empty"}, want: CodeInvalidConfiguration},
		{name: "agent invalid id", err: &agent.InvalidIDError{ID: "Bad Id", Problem: "byte 3 is not allowed"}, want: CodeInvalidConfiguration},
		{name: "workspace invalid path", err: &workspace.InvalidPathError{Path: "relative", Problem: "must be absolute"}, want: CodeInvalidConfiguration},
		{name: "workspace invalid git", err: &workspace.InvalidGitError{Root: "/x", Problem: "branch is empty"}, want: CodeInvalidConfiguration},
		{name: "untyped", err: errors.New("boom"), want: CodeUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CodeOf(tc.err); got != tc.want {
				t.Fatalf("CodeOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestErrorNamesSubjectReasonAndHint keeps the CLI contract's error rule.
func TestErrorNamesSubjectReasonAndHint(t *testing.T) {
	err := NewError(CodeConflict, "01JQ", "lifecycle running is active", "stop it first").ForAttempt(3)
	message := err.Error()
	for _, fragment := range []string{"CONFLICT", "01JQ", "attempt 3", "lifecycle running is active", "hint: stop it first"} {
		if !strings.Contains(message, fragment) {
			t.Fatalf("error %q is missing %q", message, fragment)
		}
	}
	wrapped := WrapError(CodeLaunchFailed, "01JQ", "spawn failed", "check the binary", errors.New("exec: permission denied"))
	if !errors.Is(wrapped, wrapped.Err) {
		t.Fatalf("the cause is not reachable with errors.Is")
	}
	if !strings.Contains(wrapped.Error(), "exec: permission denied") {
		t.Fatalf("wrapped error %q hides its cause", wrapped.Error())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle ||
		len(needle) == 0 || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
