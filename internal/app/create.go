package app

import (
	"context"
	"strings"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// TypeSessionLaunchFailed reports a recorded attempt that never produced a
// child. It is deliberately not TypeSessionStarted, because nothing started.
const TypeSessionLaunchFailed events.Type = "session.launch-failed"

// maxNameLen mirrors the domain's session-name bound so a derived name can be
// trimmed before it reaches a record.
const maxNameLen = 128

// Create resolves a workspace and a provider command, mints a stable session
// identity, launches one attempt, persists the result and announces it.
//
// The order is the whole point of the use case. Nothing is spawned until the
// workspace is verified, the provider has resolved a launchable command and the
// reducer has accepted the launch; the child is only ever signalled through the
// identity the runtime captured for it; and the metadata event is published only
// after the store accepted the mutation. A launch that cannot be persisted is
// cleaned up through that verified identity and reported, never left behind.
func (s *Service) Create(ctx context.Context, req CreateRequest) (CreateResult, error) {
	if err := req.Validate(); err != nil {
		return CreateResult{}, err
	}
	release, err := s.enterMutation(req.Agent.String())
	if err != nil {
		return CreateResult{}, err
	}
	defer release()

	now := s.now()

	ws, err := s.resolveWorkspace(ctx, req)
	if err != nil {
		return CreateResult{}, err
	}
	name := req.Name
	if name == "" {
		name = defaultSessionName(req.Agent, ws)
	}
	command, err := s.resolveCommand(ctx, req, req.Agent, ws, name)
	if err != nil {
		return CreateResult{}, err
	}

	id, err := s.ids.MintID(ctx)
	if err != nil {
		return CreateResult{}, session.WrapError(session.CodeConflict, "session",
			"could not mint a session identity: "+err.Error(),
			"retry the launch; if it keeps failing, the identity source is unavailable", err)
	}
	if !id.Valid() {
		return CreateResult{}, session.NewError(session.CodeInvalidConfiguration, string(id),
			"the minted session identity is not a usable id", "this is a contract bug; report the id that was minted")
	}
	fresh, err := session.New(id, name, req.Agent, ws.ID(), now)
	if err != nil {
		return CreateResult{}, session.WrapError(session.CodeInvalidConfiguration, string(id),
			"the session record could not be built: "+err.Error(),
			"pass a name without control characters or surrounding whitespace", err)
	}

	// The launch request is validated before anything is spawned, so an
	// unlaunchable command never reaches the runtime.
	launchReq := session.LaunchRequest{
		SessionID:  id,
		Generation: fresh.Generation + 1,
		Command:    command,
		Workspace:  ws,
	}
	if err := launchReq.Validate(); err != nil {
		return CreateResult{}, err
	}

	launchEvent := session.Event{
		Kind:        session.EventLaunchRequested,
		Generation:  fresh.Generation,
		At:          now,
		Command:     command,
		WorkspaceID: ws.ID(),
	}
	// Authorize first: a refusal must leave no record, no process and no event.
	if _, err := authorize(fresh, launchEvent); err != nil {
		return CreateResult{}, err
	}
	if err := s.engine.Add(fresh); err != nil {
		return CreateResult{}, engineError(id, "register the new session", err)
	}
	if _, err := s.apply(id, launchEvent); err != nil {
		return CreateResult{}, err
	}

	identity, err := s.runtime.Launch(ctx, launchReq)
	if err != nil {
		return s.failLaunch(ctx, id, launchReq.Generation, "the process runtime refused to spawn the child: "+err.Error())
	}
	// A spawn that cannot prove its identity is a failed launch, not a running
	// session: ASD never records a PID it could not verify, and it never signals
	// one it did not record.
	if verr := identity.Validate(); verr != nil {
		return s.failLaunch(ctx, id, launchReq.Generation,
			"the spawn did not return a verifiable process identity: "+verr.Error()+
				"; the child could not be cleaned up because its identity is unknown")
	}

	if _, err := s.apply(id, session.Event{
		Kind:       session.EventSpawned,
		Generation: launchReq.Generation,
		At:         s.now(),
		Identity:   identity,
	}); err != nil {
		return CreateResult{}, err
	}

	revision, err := s.commit(ctx)
	if err != nil {
		return CreateResult{}, s.cleanupAfterPersistFailure(ctx, id, launchReq.Generation, identity, err)
	}

	current, found := s.engine.Get(id)
	if !found {
		return CreateResult{}, session.NewError(session.CodeNotFound, string(id),
			"the session disappeared right after it was created", "list the sessions to see what is stored")
	}
	row := s.row(current, s.now())
	event, perr := s.publish(revision, events.TypeSessionStarted, row, s.now(), eventExtras{transition: session.RowSpawned})
	if perr != nil {
		return CreateResult{Session: row, Revision: revision}, perr
	}
	return CreateResult{Session: row, Revision: revision, Event: event}, nil
}

// failLaunch records the failed attempt and reports the launch failure.
//
// The failure is persisted before it is returned: a failed attempt is immutable
// history, so the next attempt is a restart rather than a second guess at what
// went wrong.
func (s *Service) failLaunch(ctx context.Context, id session.ID, generation session.Generation, reason string) (CreateResult, error) {
	if _, err := s.apply(id, session.Event{
		Kind:       session.EventSpawnFailed,
		Generation: generation,
		At:         s.now(),
		Failure:    reason,
	}); err != nil {
		return CreateResult{}, err
	}
	revision, commitErr := s.commit(ctx)
	if commitErr != nil {
		return CreateResult{}, commitErr
	}
	current, found := s.engine.Get(id)
	if !found {
		return CreateResult{}, session.NewError(session.CodeNotFound, string(id),
			"the session disappeared while its launch was failing", "list the sessions to see what is stored")
	}
	row := s.row(current, s.now())
	// The mutation is committed, so it is announced; the launch failure code
	// still goes back to the caller.
	if _, err := s.publish(revision, TypeSessionLaunchFailed, row, s.now(), eventExtras{transition: session.RowSpawnFailed}); err != nil {
		return CreateResult{Session: row, Revision: revision}, err
	}
	return CreateResult{Session: row, Revision: revision}, launchError(id, generation, reason)
}

// cleanupAfterPersistFailure handles the one sequence in this codebase where a
// child exists but the metadata does not (transition T5).
//
// The reducer refuses to hand back a cleanup effect without a verified identity,
// so the child is ended only through the identity the runtime captured for it,
// and only through a forced kill that waits for the reap: a delivered signal
// proves nothing, and a process nobody confirmed is gone cannot be reported as
// cleaned up. Nothing is persisted and no event is published, because no mutation
// was committed; the in-memory record keeps the failure and the cleanup note
// visible so the operator can see what happened.
//
// Every message states what is actually known. A confirmed cleanup says the child
// was reaped; an unconfirmed one says the child may still be running and names the
// workspace to look for it in.
func (s *Service) cleanupAfterPersistFailure(ctx context.Context, id session.ID, generation session.Generation, identity session.ProcessIdentity, cause error) error {
	outcome, err := s.apply(id, session.Event{
		Kind:       session.EventPersistFailed,
		Generation: generation,
		At:         s.now(),
		Failure:    cause.Error(),
	})
	if err != nil {
		return err
	}
	code := session.CodeOf(cause)
	if code == "" || code == session.CodeUnknown {
		code = session.CodeUnknown
	}
	for _, effect := range outcome.Effects {
		if effect.Kind != session.EffectCleanupOwnedChild {
			continue
		}
		if !effect.Identity.Valid() {
			// Fail closed: without a verified identity ASD signals nothing at all,
			// because a recycled PID is not proof of ownership.
			return session.WrapError(code, string(id),
				"the session was launched but its metadata could not be persisted, and its child could not be cleaned up safely",
				"find the process by its executable and workspace, then end it by hand; ASD refuses to signal an unverified pid", cause).
				ForAttempt(generation)
		}
		cleanup, cleanupErr := s.runtime.ForceKill(ctx, effect.Identity, s.grace)
		if cleanupErr != nil {
			return session.WrapError(code, string(id),
				"the session was launched, its metadata could not be persisted, and its child could not be cleaned up: "+cleanupErr.Error(),
				"find the surviving process by its workspace and end it by hand", cause).ForAttempt(generation)
		}
		if _, confirmed := cleanup.Terminal(); !confirmed {
			return session.WrapError(code, string(id),
				"the session was launched and a "+string(effect.Signal)+" was delivered to its verified process group, "+
					"but no reap was confirmed: the child may still be running, and its metadata could not be persisted: "+
					cause.Error(),
				"find the child by its workspace and end it by hand, then retry the launch",
				cause).ForAttempt(generation)
		}
	}
	return session.WrapError(code, string(id),
		"the child was launched, its process group was reaped, but its metadata could not be persisted: "+cause.Error(),
		"retry the launch; if it keeps failing, inspect the state file and its permissions", cause).
		ForAttempt(generation)
}

// resolveWorkspace turns the caller's candidate into a verified directory.
func (s *Service) resolveWorkspace(ctx context.Context, req CreateRequest) (workspace.Workspace, error) {
	candidate, err := workspace.NewResolveRequest(req.WorkspacePath, req.source())
	if err != nil {
		return workspace.Workspace{}, session.WrapError(session.CodeOf(err), req.WorkspacePath,
			"the workspace candidate is not valid", "pass a directory the agent can run in", err)
	}
	ws, err := s.workspaces.Resolve(ctx, candidate)
	if err != nil {
		return workspace.Workspace{}, session.WrapError(session.CodeOf(err), req.WorkspacePath,
			"the workspace could not be resolved: "+err.Error(),
			"pass an existing, readable directory", err)
	}
	// The resolver must fail honestly: a workspace whose identity does not
	// validate is refused rather than stored.
	if verr := ws.ID().Validate(); verr != nil {
		return workspace.Workspace{}, session.WrapError(session.CodeInvalidConfiguration, req.WorkspacePath,
			"the resolver returned a workspace identity that is not canonical: "+verr.Error(),
			"this is a contract bug; report the path the resolver returned", verr)
	}
	return ws, nil
}

// resolveCommand turns the workspace and the extra argv into one frozen
// command.
//
// The extra arguments are copied into the provider's request and come back inside
// the immutable command, so they reach the process exactly as written: never
// re-split, never globbed, never expanded by a shell.
func (s *Service) resolveCommand(ctx context.Context, req CreateRequest, agentID agent.ID, ws workspace.Workspace, name string) (agent.Command, error) {
	provider, err := s.RequireProvider(agentID, agent.CapabilityLaunch)
	if err != nil {
		return agent.Command{}, err
	}
	resolveReq, err := agent.NewResolveRequest(ws.Path(), name, req.ExtraArgs)
	if err != nil {
		return agent.Command{}, session.WrapError(session.CodeOf(err), string(agentID),
			"the provider request is not valid", "fix the name or the workspace path", err)
	}
	command, err := provider.Resolve(ctx, resolveReq)
	if err != nil {
		return agent.Command{}, session.WrapError(session.CodeOf(err), string(agentID),
			"the agent definition could not resolve a launchable command: "+err.Error(),
			"check the agent's executable path and its argument shape", err)
	}
	if verr := command.ValidateForLaunch(); verr != nil {
		return agent.Command{}, session.WrapError(session.CodeLaunchFailed, string(agentID),
			"the resolved command cannot be launched: "+verr.Error(),
			"the command must be an absolute executable path", verr)
	}
	return command, nil
}

// defaultSessionName derives a label from the agent and the workspace when the
// caller did not supply one.
func defaultSessionName(agentID agent.ID, ws workspace.Workspace) string {
	candidate := agentID.String() + "-" + ws.DisplayName()
	if candidate != "" && len(candidate) <= maxNameLen && strings.TrimSpace(candidate) == candidate {
		return candidate
	}
	return agentID.String()
}
