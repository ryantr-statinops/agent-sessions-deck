package session

import (
	"testing"
	"time"
)

// TestSnapshotAuthorityDistinguishesPersistedFromLive is the offline-read
// acceptance: a stored running row must never read as a live process.
func TestSnapshotAuthorityDistinguishesPersistedFromLive(t *testing.T) {
	sessions := []Session{runningSession(t), exitedSession(t), freshSession(t)}

	live, err := NewSnapshot(AuthorityLive, at(500), 12, sessions)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	if err := live.Validate(); err != nil {
		t.Fatalf("live snapshot is invalid: %v", err)
	}
	if live.Revision != 12 || !live.ObservedAt.Equal(at(500)) {
		t.Fatalf("snapshot lost its revision or observed-at stamp: %+v", live)
	}

	row, ok := live.Find(runningSession(t).ID)
	if !ok {
		t.Fatalf("live snapshot is missing the running session")
	}
	if !row.ClaimsLiveness() {
		t.Fatalf("a live running row must claim liveness")
	}

	stored := live.AsStored()
	storedRow, ok := stored.Find(row.ID)
	if !ok {
		t.Fatalf("stored snapshot is missing the running session")
	}
	if storedRow.ClaimsLiveness() {
		t.Fatalf("a stored running row must not claim liveness")
	}
	if storedRow.Lifecycle != LifecycleRunning {
		t.Fatalf("downgrading authority must not change observations: %q", storedRow.Lifecycle)
	}
	if !storedRow.ObservedAt.Equal(at(500)) {
		t.Fatalf("downgrading authority must keep observed-at")
	}
	if stored.Revision != live.Revision {
		t.Fatalf("downgrading authority must keep the revision")
	}

	if _, err := NewSnapshot("cached", at(1), 1, nil); err == nil {
		t.Fatalf("an unknown authority was accepted")
	}
	if _, err := NewSnapshot(AuthorityLive, time.Time{}, 1, nil); err == nil {
		t.Fatalf("a snapshot without observed-at was accepted")
	}
}

// TestSnapshotCarriesOnlySafeFields keeps raw terminal bytes and environment
// secrets out of the read model by construction.
func TestSnapshotCarriesOnlySafeFields(t *testing.T) {
	sessions := []Session{orphanSession(t)}
	live, err := NewSnapshot(AuthorityLive, at(600), 3, sessions)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	row, _ := live.Find(sessions[0].ID)
	if !row.Orphaned() {
		t.Fatalf("an orphan must be visible as running plus unavailable")
	}
	if row.Deletable() {
		t.Fatalf("an orphan must not be deletable")
	}
	if !row.HasIdentity {
		t.Fatalf("the orphan row must keep the verified identity as evidence")
	}
	if len(row.Notes) == 0 {
		t.Fatalf("the orphan row must keep its diagnostic notes")
	}
}

// TestSnapshotRejectsAnInvalidSession proves a corrupt record surfaces as
// CORRUPT_STATE instead of being reset to empty.
func TestSnapshotRejectsAnInvalidSession(t *testing.T) {
	corrupt := runningSession(t)
	corrupt.Attempts[0].Generation = 0
	_, err := NewSnapshot(AuthorityStored, at(1), 1, []Session{corrupt})
	if err == nil {
		t.Fatalf("a corrupt session was published")
	}
	if got := CodeOf(err); got != CodeCorruptState {
		t.Fatalf("code = %s, want %s", got, CodeCorruptState)
	}
}

// TestSnapshotFiltersAndPrefixResolution covers the list helpers, including the
// refusal to guess an ambiguous session id.
func TestSnapshotFiltersAndPrefixResolution(t *testing.T) {
	first := runningSession(t)
	second := exitedSession(t)
	second.ID = ID("01JQASDWSDECK0002")
	third := freshSession(t)
	third.ID = ID("01JQOTHER000003")

	snapshot, err := NewSnapshot(AuthorityLive, at(700), 9, []Session{third, second, first})
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	if snapshot.Sessions[0].ID != first.ID || snapshot.Sessions[2].ID != third.ID {
		t.Fatalf("snapshot rows are not ordered by session id: %v", ids(snapshot))
	}

	terminal := snapshot.Filter(func(row SessionSnapshot) bool { return row.Deletable() })
	if len(terminal) != 1 || terminal[0].ID != second.ID {
		t.Fatalf("filter returned %v", ids(snapshot))
	}

	row, err := snapshot.FindByPrefix("01JQASDWSDECK000")
	if err == nil {
		t.Fatalf("an ambiguous prefix was resolved to %s", row.ID)
	}
	if got := CodeOf(err); got != CodeConflict {
		t.Fatalf("ambiguous prefix code = %s, want %s", got, CodeConflict)
	}

	row, err = snapshot.FindByPrefix("01JQASDWSDECK0002")
	if err != nil {
		t.Fatalf("an unambiguous prefix was refused: %v", err)
	}
	if row.ID != second.ID {
		t.Fatalf("prefix resolved to %s, want %s", row.ID, second.ID)
	}

	if _, err := snapshot.FindByPrefix("missing"); CodeOf(err) != CodeNotFound {
		t.Fatalf("unknown prefix code = %s, want %s", CodeOf(err), CodeNotFound)
	}
}

// TestSnapshotRowsAreCopies proves a caller cannot edit a published snapshot.
func TestSnapshotRowsAreCopies(t *testing.T) {
	sessions := []Session{orphanSession(t)}
	snapshot, err := NewSnapshot(AuthorityLive, at(800), 1, sessions)
	if err != nil {
		t.Fatalf("NewSnapshot: %v", err)
	}
	rows := snapshot.Filter(func(SessionSnapshot) bool { return true })
	if len(rows) != 1 {
		t.Fatalf("filter returned %d rows, want 1", len(rows))
	}
	rows[0].Notes[0] = "tampered"
	if snapshot.Sessions[0].Notes[0] == "tampered" {
		t.Fatalf("snapshot notes were mutated through a copy")
	}
}

func ids(snapshot Snapshot) []ID {
	out := make([]ID, 0, len(snapshot.Sessions))
	for _, row := range snapshot.Sessions {
		out = append(out, row.ID)
	}
	return out
}
