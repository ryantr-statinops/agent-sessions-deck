package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
)

// These tests pin the durable round trip of a session record. The private state -
// the frozen argv and the attempt notes - is exactly what a stored record must
// never lose, and it is exactly what the default encoder drops, so each of these
// asserts the field survived rather than asserting the encoder's shape.

func TestCommandRoundTripsThroughJSON(t *testing.T) {
	for name, command := range map[string]agent.Command{
		"executable only": mustCommand(t, "/usr/local/bin/opencode"),
		"literal argv":    mustCommand(t, "/usr/local/bin/opencode", "--model", "gpt-5", "--", "a b", "*", "$HOME"),
		"empty argv":      mustCommand(t, "/usr/local/bin/opencode"),
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(command)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			decoded, err := agent.DecodeCommand(bytes.NewReader(encoded))
			if err != nil {
				t.Fatalf("DecodeCommand: %v", err)
			}
			if !decoded.Equal(command) {
				t.Fatalf("round trip changed the command: %q -> %q", command.String(), decoded.String())
			}
			if decoded.Executable() != command.Executable() {
				t.Fatalf("executable = %q, want %q", decoded.Executable(), command.Executable())
			}
			if got, want := decoded.Argv(), command.Argv(); !slicesEqual(got, want) {
				t.Fatalf("argv = %q, want %q", got, want)
			}
		})
	}
}

func TestCommandJSONRoundTripsTheZeroCommand(t *testing.T) {
	// An attempt that never resolved a command (T1) stores the zero command, so the
	// zero value has to survive the round trip instead of failing validation.
	encoded, err := json.Marshal(agent.Command{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	decoded, err := agent.DecodeCommand(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodeCommand: %v", err)
	}
	if !decoded.IsZero() {
		t.Fatalf("decoded command = %q, want the zero command", decoded.String())
	}
}

func TestCommandDecodeRefusesLostAndUnknownFields(t *testing.T) {
	cases := map[string]struct {
		document string
		want     string
	}{
		"unknown field": {
			document: `{"executable":"/bin/agent","args":[],"shell":true}`,
			want:     "unknown field",
		},
		"empty executable": {
			document: `{"executable":"","args":["--resume"]}`,
			want:     "executable is empty",
		},
		"empty argument": {
			document: `{"executable":"/bin/agent","args":["--resume",""]}`,
			want:     "is empty",
		},
		"nul byte": {
			document: `{"executable":"/bin/agent","args":["a\u0000b"]}`,
			want:     "NUL byte",
		},
		"trailing data": {
			document: `{"executable":"/bin/agent","args":[]}{"executable":"/bin/agent","args":[]}`,
			want:     "trailing data",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := agent.DecodeCommand(strings.NewReader(tc.document))
			if err == nil {
				t.Fatalf("%s was accepted", name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestSessionRoundTripPreservesCommandsNotesReasonsAndTimestamps(t *testing.T) {
	// A session that has lived: a running attempt whose graceful stop timed out, so
	// it carries a command, a verified identity, notes and timestamps that a plain
	// struct encoder would drop.
	original := stopTimeoutSession(t)

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Contains(encoded, []byte("kill-required")) {
		t.Fatalf("the encoded record lost the kill guidance note: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte("/usr/local/bin/opencode")) {
		t.Fatalf("the encoded record lost the resolved command: %s", encoded)
	}

	restored, err := DecodeSession(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodeSession: %v", err)
	}
	if err := restored.Validate(); err != nil {
		t.Fatalf("the restored record does not validate: %v", err)
	}
	assertSessionsEqual(t, restored, original)

	current, ok := restored.Current()
	if !ok {
		t.Fatal("the restored session lost its current attempt")
	}
	if !current.HasCommand() || current.Command.Executable() != "/usr/local/bin/opencode" {
		t.Fatalf("the restored attempt lost its resolved command: %q", current.Command.String())
	}
	if !current.HasIdentity() || current.Identity != testIdentity {
		t.Fatalf("the restored attempt lost its process identity: %s", current.Identity.Describe())
	}
	if !current.HasNote(NoteStopTimeout) || !current.HasNote(NoteKillGuidance) {
		t.Fatalf("the restored attempt lost its notes: %v", current.Notes())
	}
	if !current.StartedAt.Equal(original.Attempts[0].StartedAt) ||
		!current.RunningAt.Equal(original.Attempts[0].RunningAt) {
		t.Fatalf("the restored attempt lost its timestamps: %+v", current)
	}
	if !restored.CreatedAt.Equal(original.CreatedAt) || !restored.UpdatedAt.Equal(original.UpdatedAt) {
		t.Fatalf("the restored session lost its timestamps: %+v", restored)
	}
}
func TestDurableNestedFieldsUseStableSnakeCaseKeys(t *testing.T) {
	cases := []struct {
		name  string
		value any
		keys  []string
	}{
		{name: "reason", value: NaturalExit(3), keys: []string{"kind", "exit_code", "signal", "detail"}},
		{name: "identity", value: testIdentity, keys: []string{"pid", "pgid", "boot_id", "start_ticks", "owner_instance_id"}},
		{name: "activity evidence", value: Evidence{Source: "provider", Detail: "working", At: at(10)}, keys: []string{"source", "detail", "at"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatalf("unmarshal wire object: %v", err)
			}
			if len(fields) != len(tc.keys) {
				t.Fatalf("wire fields = %v, want exactly %v", fields, tc.keys)
			}
			for _, key := range tc.keys {
				if _, ok := fields[key]; !ok {
					t.Errorf("wire field %q missing from %s", key, encoded)
				}
			}
		})
	}
}

func TestSessionRoundTripPreservesEveryReasonShape(t *testing.T) {
	code := func(value int) *int { return &value }
	cases := map[string]struct {
		lifecycle Lifecycle
		reason    Reason
		exitCode  *int
	}{
		"natural exit":      {LifecycleExited, NaturalExit(137), code(137)},
		"stopped with code": {LifecycleExited, Stopped(0), code(0)},
		"stopped by signal": {LifecycleExited, StoppedBySignal("SIGTERM"), nil},
		"killed":            {LifecycleExited, Killed("SIGKILL"), nil},
		"process gone":      {LifecycleExited, ProcessGone("liveness=gone"), nil},
		"stale identity":    {LifecycleExited, StaleIdentity("liveness=alive detail=pid-reused"), nil},
		"probe timeout":     {LifecycleUnknown, ProbeInconclusive(ReasonProbeTimeout, "probe-timeout"), nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			current, _ := runningSession(t).Current()
			current.Lifecycle = tc.lifecycle
			current.Reason = tc.reason
			if tc.lifecycle.Terminal() {
				current.EndedAt = at(30)
			}
			if tc.exitCode != nil {
				current.ExitCode = *tc.exitCode
				current.HasExitCode = true
			} else {
				current.HasExitCode = false
			}
			mutated := freshSession(t)
			mutated.Generation = 1
			mutated.Attempts = []Attempt{current}
			if err := mutated.Validate(); err != nil {
				t.Fatalf("the %s record does not validate: %v", name, err)
			}

			encoded, err := json.Marshal(mutated)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			restored, err := DecodeSession(bytes.NewReader(encoded))
			if err != nil {
				t.Fatalf("DecodeSession: %v", err)
			}
			restoredCurrent, _ := restored.Current()
			if !restoredCurrent.Reason.Equal(tc.reason) {
				t.Fatalf("reason = %q, want %q", restoredCurrent.Reason.String(), tc.reason.String())
			}
			if restoredCurrent.HasExitCode != (tc.exitCode != nil) {
				t.Fatalf("exit-code presence = %v, want %v", restoredCurrent.HasExitCode, tc.exitCode != nil)
			}
			if tc.exitCode != nil && restoredCurrent.ExitCode != *tc.exitCode {
				t.Fatalf("exit code = %d, want %d", restoredCurrent.ExitCode, *tc.exitCode)
			}
		})
	}

	// A record with no attempt at all keeps its lifecycle-free shape.
	empty := freshSession(t)
	encoded, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	restored, err := DecodeSession(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("DecodeSession: %v", err)
	}
	if restored.State() != (State{Lifecycle: LifecycleCreated, Attachment: AttachmentUnavailable, Activity: ActivityUnknown}) {
		t.Fatalf("the restored empty record = %+v", restored.State())
	}
}

func TestSessionRoundTripSurvivesAStoreCycle(t *testing.T) {
	// The shape a store actually uses: write every session of one revision, read
	// them back, and get the same records.
	sessions := []Session{
		freshSession(t),
		runningSession(t),
		exitedSession(t),
		orphanSession(t),
		unknownSession(t),
		stopTimeoutSession(t),
	}
	var encoded bytes.Buffer
	if err := EncodeSessions(&encoded, sessions); err != nil {
		t.Fatalf("EncodeSessions: %v", err)
	}
	restored, err := DecodeSessions(bytes.NewReader(encoded.Bytes()))
	if err != nil {
		t.Fatalf("DecodeSessions: %v", err)
	}
	if len(restored) != len(sessions) {
		t.Fatalf("restored %d sessions, want %d", len(restored), len(sessions))
	}
	for i, want := range sessions {
		assertSessionsEqual(t, restored[i], want)
	}
}

func TestSessionDecodeRefusesRecordsItCannotTrust(t *testing.T) {
	encoded := string(mustMarshal(t, stopTimeoutSession(t)))
	withoutNotes := strings.Replace(encoded, `"notes":["stop-timeout","kill-required"]`, `"notes":[]`, 1)
	if withoutNotes == encoded {
		t.Fatalf("the fixture does not carry notes to strip: %s", encoded)
	}

	cases := map[string]struct {
		document string
		want     string
	}{
		"unknown session field": {
			document: strings.Replace(encoded, `"name":`, `"nickname":`, 1),
			want:     "unknown field",
		},
		"unknown attempt field": {
			document: strings.Replace(encoded, `"lifecycle":"running"`, `"lifecycle":"running","cwd":"/tmp"`, 1),
			want:     "unknown field",
		},
		"running without an identity": {
			document: replaceIdentity(encoded),
			want:     `lifecycle "running" without a captured process identity`,
		},
		"terminal without a reason": {
			document: strings.Replace(encoded, `"lifecycle":"running"`, `"lifecycle":"exited"`, 1),
			want:     "without an end timestamp",
		},
		"non-contiguous attempts": {
			document: strings.Replace(encoded, `"attempts":[{"generation":1,`, `"attempts":[{"generation":3,`, 1),
			want:     "generation 3",
		},
		"unusable exit code": {
			document: strings.Replace(encoded, `"has_exit_code":false`, `"has_exit_code":true,"exit_code":7`, 1),
			want:     "disagrees with reason",
		},
		"unevidenced activity": {
			document: strings.Replace(encoded, `"activity":"unknown"`, `"activity":"idle"`, 1),
			want:     "no provider evidence",
		},
		"identity owner disagreement": {
			document: strings.Replace(encoded, `"owner_instance_id":"owner-1"`, `"owner_instance_id":"owner-9"`, 1),
			want:     "identity owner",
		},
		"trailing data": {
			document: encoded + encoded,
			want:     "trailing data",
		},
		"truncated": {
			document: encoded[:len(encoded)/2],
			want:     "",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			restored, err := DecodeSession(strings.NewReader(tc.document))
			if err == nil {
				t.Fatalf("%s was accepted as %+v", name, restored)
			}
			if got := CodeOf(err); got != CodeCorruptState {
				t.Fatalf("code = %s, want %s (%v)", got, CodeCorruptState, err)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.want)
			}
			if restored.ID != "" || restored.Generation != 0 || restored.AttemptCount() != 0 {
				t.Fatalf("a refused record was returned anyway: %+v", restored)
			}
		})
	}

	// Stripping the notes is not detectable as invalid - the notes are diagnostics -
	// so it is proven separately: a reloaded attempt keeps the T9 guidance that
	// tells a reader the kill is still the user's next action.
	reloaded, err := DecodeSession(strings.NewReader(withoutNotes))
	if err != nil {
		t.Fatalf("a record with no notes was refused: %v", err)
	}
	current, _ := reloaded.Current()
	if current.HasNote(NoteStopTimeout) {
		t.Fatalf("the strip failed, so the test proves nothing: %v", current.Notes())
	}
	if !current.HasCommand() || current.Command.Executable() != "/usr/local/bin/opencode" {
		t.Fatalf("the reloaded attempt lost its frozen command: %q", current.Command.String())
	}
	if !current.HasIdentity() {
		t.Fatal("the reloaded attempt lost its verified identity")
	}
}

func TestSessionDecodeRefusesARecordThatWasNeverPersisted(t *testing.T) {
	// The failing case from the review: the default encoder wrote `"Command":{}`
	// and no notes, and the reloaded attempt validated anyway. A decoded attempt
	// that claims a lifecycle the record cannot support must never pass.
	if _, err := DecodeSession(strings.NewReader(`{"id":"s-1","name":"n","agent_id":"opencode",` +
		`"workspace_id":"/tmp/ws","created_at":"2026-02-03T04:05:06Z","updated_at":"2026-02-03T04:05:06Z",` +
		`"last_seen_at":"2026-02-03T04:05:06Z","generation":1,"attempts":[{"generation":1,` +
		`"lifecycle":"running","attachment":"detached","activity":"unknown"}]}`)); err == nil {
		t.Fatal("a record with a running attempt, no identity and no command was accepted")
	}
}

func TestAttemptRoundTripPreservesProviderEvidence(t *testing.T) {
	annotated := runningSession(t)
	current, _ := annotated.Current()
	current.Activity = ActivityWorking
	current.ActivityEvidence = Evidence{Source: "opencode-adapter", Detail: "status line", At: at(30)}
	annotated.replaceCurrent(current)
	if err := annotated.Validate(); err != nil {
		t.Fatalf("the annotated record does not validate: %v", err)
	}

	encoded, err := json.Marshal(annotated.Attempts[0])
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var restored Attempt
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if restored.Activity != ActivityWorking || restored.ActivityEvidence != current.ActivityEvidence {
		t.Fatalf("the restored attempt lost the activity evidence: %+v", restored)
	}
	if !restored.Identity.Equal(testIdentity) {
		t.Fatalf("the restored attempt lost its identity: %s", restored.Identity.Describe())
	}
	if restored.Command.Executable() != current.Command.Executable() {
		t.Fatalf("the restored attempt lost its command: %q", restored.Command.String())
	}
}

// replaceIdentity blanks the recorded identity, which is the hand-edited or
// older-schema record the running-implies-identity invariant has to catch.
func replaceIdentity(encoded string) string {
	return strings.NewReplacer(
		`"identity":{"pid":4242,"pgid":4242,"boot_id":"boot-a1b2c3","start_ticks":987654,"owner_instance_id":"owner-1"}`,
		`"identity":{"pid":0,"pgid":0,"boot_id":"","start_ticks":0,"owner_instance_id":""}`,
	).Replace(encoded)
}

func slicesEqual(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func assertSessionsEqual(t *testing.T, got, want Session) {
	t.Helper()
	if got.ID != want.ID || got.Name != want.Name || got.AgentID != want.AgentID ||
		got.WorkspaceID != want.WorkspaceID || got.Generation != want.Generation {
		t.Fatalf("identity changed:\n got %+v\nwant %+v", got, want)
	}
	if got.AttemptCount() != want.AttemptCount() {
		t.Fatalf("attempt count = %d, want %d", got.AttemptCount(), want.AttemptCount())
	}
	for i := range want.Attempts {
		a, b := got.Attempts[i], want.Attempts[i]
		if a.State() != b.State() || a.Identity != b.Identity || a.ExitCode != b.ExitCode ||
			a.HasExitCode != b.HasExitCode || a.OwnerInstanceID != b.OwnerInstanceID {
			t.Fatalf("attempt %d changed:\n got %+v\nwant %+v", i+1, a, b)
		}
		if !a.Command.Equal(b.Command) {
			t.Fatalf("attempt %d command changed: %q -> %q", i+1, b.Command.String(), a.Command.String())
		}
		if !a.Reason.Equal(b.Reason) {
			t.Fatalf("attempt %d reason changed: %q -> %q", i+1, b.Reason.String(), a.Reason.String())
		}
		if !slicesEqual(a.Notes(), b.Notes()) {
			t.Fatalf("attempt %d notes changed: %v -> %v", i+1, b.Notes(), a.Notes())
		}
		if a.Activity != b.Activity || a.ActivityEvidence != b.ActivityEvidence {
			t.Fatalf("attempt %d activity changed: %+v -> %+v", i+1, b, a)
		}
		if !a.StartedAt.Equal(b.StartedAt) || !a.RunningAt.Equal(b.RunningAt) || !a.EndedAt.Equal(b.EndedAt) {
			t.Fatalf("attempt %d timestamps changed: %+v -> %+v", i+1, b, a)
		}
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return encoded
}

func reduce(t *testing.T, from Session, event Event) Outcome {
	t.Helper()
	outcome := Reduce(from, event)
	mustApply(t, outcome)
	return outcome
}

var _ = errors.Is
