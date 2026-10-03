package app

import (
	"context"
	"slices"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
)

// Scan reports the agent definitions this owner knows about.
//
// The scan is offline-safe by construction: it reads the registry and nothing
// else, so it never probes an executable, never resolves a command and never
// spawns anything. Capabilities are reported split into the ASD core lifecycle
// and the native vendor operations, so a caller can see at a glance which of the
// vendor operations this build cannot serve.
func (s *Service) Scan(_ context.Context, req ScanRequest) (ScanResult, error) {
	for _, id := range req.Agents {
		if err := id.Validate(); err != nil {
			return ScanResult{}, session.WrapError(session.CodeInvalidConfiguration, string(id),
				"the requested agent id is not valid", "run a scan to see valid agent ids", err)
		}
	}
	wanted := slices.Clone(req.Agents)
	slices.Sort(wanted)

	ids := s.registry.IDs()
	slices.Sort(ids)
	summaries := make([]AgentSummary, 0, len(ids))
	for _, id := range ids {
		if len(wanted) > 0 && !slices.Contains(wanted, id) {
			continue
		}
		if verr := id.Validate(); verr != nil {
			// A registry that indexes an unusable id is a configuration bug, not a
			// reason to hide the agents that are fine.
			return ScanResult{}, session.WrapError(session.CodeInvalidConfiguration, string(id),
				"the registry indexes an agent id that is not valid: "+verr.Error(),
				"fix the configured agent id; asd will not guess what it was meant to be", verr)
		}
		provider, found := s.registry.Lookup(id)
		if !found || provider == nil {
			return ScanResult{}, session.NewError(session.CodeNotFound, string(id),
				"the registry lists an agent id it cannot resolve",
				"this is a contract bug; report the agent id with the build you are running")
		}
		caps := provider.Capabilities()
		summaries = append(summaries, AgentSummary{
			ID:           id,
			Capabilities: caps,
			Launchable:   caps.Has(agent.CapabilityLaunch),
			Interactive:  caps.Has(agent.CapabilityInteractive),
			Native:       caps.Native().List(),
		})
	}
	now := s.now()
	return ScanResult{Agents: summaries, Authority: s.authority, ObservedAt: now, Revision: s.Revision()}, nil
}

// List returns the session rows the owner knows about.
//
// Every row keeps the authority it was observed at, the store revision and the
// instant of the observation, so an offline reader can show a persisted running
// row without ever presenting it as a live process.
func (s *Service) List(_ context.Context, req ListRequest) (ListResult, error) {
	if err := req.Validate(); err != nil {
		return ListResult{}, err
	}
	snap, err := s.snapshot(s.now())
	if err != nil {
		return ListResult{}, err
	}
	keep := func(row session.SessionSnapshot) bool {
		if len(req.Lifecycles) > 0 && !slices.Contains(req.Lifecycles, row.Lifecycle) {
			return false
		}
		if req.AgentID != "" && row.AgentID != req.AgentID {
			return false
		}
		if req.Workspace != "" && row.WorkspaceID != req.Workspace {
			return false
		}
		return true
	}
	filtered := snap.Filter(keep)
	if filtered == nil {
		// Filter reports "no rows" as nil; an empty list is still an empty list,
		// not a missing value, so the caller can render it directly.
		filtered = []session.SessionSnapshot{}
	}
	snap.Sessions = filtered
	return ListResult{Snapshot: snap}, nil
}

// Get returns one session's live and stored readings.
//
// The two rows are two different sources, not one row twice. The live row is the
// owner's own in-memory state; the stored row is read back from the store, because
// after a failed write the two disagree - the engine holds state the store never
// received - and relabelling the current state as "stored" would show an operator
// metadata that does not exist. Both rows keep the revision and the observation
// instant so the difference stays visible, and Persisted says outright whether the
// store still holds the record.
func (s *Service) Get(ctx context.Context, req GetRequest) (GetResult, error) {
	if err := validateRef(req.Ref); err != nil {
		return GetResult{}, err
	}
	now := s.now()
	current, err := s.require(req.Ref)
	if err != nil {
		return GetResult{}, err
	}
	revision := s.Revision()
	stored, persisted, err := s.storedRow(ctx, current.ID, now)
	if err != nil {
		return GetResult{}, err
	}
	return GetResult{
		Session:    session.SessionSnapshotOf(current, s.authority, now, revision),
		Stored:     stored,
		Persisted:  persisted,
		Revision:   revision,
		ObservedAt: now,
	}, nil
}

// storedRow reads one session's persisted record back from the store.
//
// The row is stamped at the revision the read observed, not at the revision the
// owner last wrote: this reading is of the store's current content, and claiming
// the owner's newer revision would be a lie. A session the store does not hold is
// reported as absent rather than as a downgraded copy of in-memory state, and a
// store that cannot be read is a typed failure rather than an excuse to invent one.
func (s *Service) storedRow(ctx context.Context, id session.ID, at time.Time) (session.SessionSnapshot, bool, error) {
	stored, storedRevision, err := s.loadPersisted(ctx)
	if err != nil {
		return session.SessionSnapshot{}, false, storeError("read the session metadata", s.Revision(), err)
	}
	for _, candidate := range stored {
		if candidate.ID != id {
			continue
		}
		if err := candidate.Validate(); err != nil {
			return session.SessionSnapshot{}, false, session.WrapError(session.CodeCorruptState, string(id),
				"the stored session metadata is not valid: "+err.Error(),
				"repair the stored record by hand; asd never resets session metadata for you", err)
		}
		return session.SessionSnapshotOf(candidate, session.AuthorityStored, at, storedRevision), true, nil
	}
	return session.SessionSnapshot{}, false, nil
}

// Rename edits a session's user-visible label.
//
// A rename never touches the lifecycle, the attempt history or the session
// identity: the label is metadata, so a historical record stays editable offline
// and the id a user already knows keeps working.
func (s *Service) Rename(ctx context.Context, req RenameRequest) (RenameResult, error) {
	if err := req.Validate(); err != nil {
		return RenameResult{}, err
	}
	release, err := s.enterMutation(req.Ref)
	if err != nil {
		return RenameResult{}, err
	}
	defer release()

	current, err := s.require(req.Ref)
	if err != nil {
		return RenameResult{}, err
	}
	renamed, err := s.engine.Rename(current.ID, req.Name, s.now())
	if err != nil {
		return RenameResult{}, engineError(current.ID, "rename the session", err)
	}
	revision, err := s.commit(ctx)
	if err != nil {
		return RenameResult{}, err
	}
	row := s.row(renamed, s.now())
	event, perr := s.publish(revision, TypeSessionRenamed, row, s.now(), eventExtras{})
	if perr != nil {
		return RenameResult{Session: row, Revision: revision}, perr
	}
	return RenameResult{Session: row, Revision: revision, Event: event}, nil
}

// Delete removes a finished session's metadata.
//
// Deleting a running session is refused (transition T20) and the record stays in
// place: a live session must be stopped or killed first, because deleting the
// metadata of a live process would orphan it with nothing left to reconcile.
//
// The rule is checked twice, before the store write and again before the record
// leaves memory. The whole use case runs under the owner-wide mutation lock, so no
// other mutation can open a new attempt between the two checks; the second one is
// what makes the invariant hold even if the lock's scope is ever narrowed, because
// a record removed from memory while a live attempt exists can never be found
// again.
func (s *Service) Delete(ctx context.Context, req DeleteRequest) (DeleteResult, error) {
	if err := req.Validate(); err != nil {
		return DeleteResult{}, err
	}
	release, err := s.enterMutation(req.Ref)
	if err != nil {
		return DeleteResult{}, err
	}
	defer release()

	current, err := s.require(req.Ref)
	if err != nil {
		return DeleteResult{}, err
	}
	// Ask before touching the store, so a refusal writes nothing.
	if err := current.EnsureDeletable(); err != nil {
		return DeleteResult{}, err
	}
	revision, err := s.deletePersisted(ctx, current.ID)
	if err != nil {
		return DeleteResult{}, err
	}
	// Re-read before the record is forgotten: the persisted write has already
	// happened, so a refusal here means the store and memory disagree about this
	// session and the operator has to know.
	if latest, found := s.engine.Get(current.ID); found {
		if err := latest.EnsureDeletable(); err != nil {
			return DeleteResult{ID: current.ID, Revision: revision}, err
		}
	}
	if err := s.engine.Delete(current.ID); err != nil {
		return DeleteResult{ID: current.ID, Revision: revision}, engineError(current.ID, "forget the deleted session", err)
	}
	row := s.row(current, s.now())
	// The row was deleted, so the snapshot is stamped after the write and carries
	// the authority it had; the event is what tells a subscriber the row is gone.
	event, perr := s.publish(revision, TypeSessionDeleted, row, s.now(), eventExtras{})
	if perr != nil {
		return DeleteResult{ID: current.ID, Revision: revision}, perr
	}
	return DeleteResult{ID: current.ID, Revision: revision, Event: event}, nil
}
