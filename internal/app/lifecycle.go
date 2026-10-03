package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/agent"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/events"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/workspace"
)

// Open attaches one client to a session.
//
// Attaching is a two-part claim: the lease broker decides who holds the session,
// and the stream is only handed out afterwards. The reducer is asked first, so an
// orphaned session, a session that never launched and a session another client
// already holds are all refused with no lease taken and no stream opened - and a
// refused open never touches the process, which may still be running.
//
// The attach is one transaction: the lease, the stream and the recorded
// attachment either all happen or none does. An attach that cannot be persisted
// is rolled back completely - stream closed, lease released, bookkeeping
// forgotten, attachment undone - because the caller never receives the lease or
// the stream and therefore has no handle with which to clean up after us.
func (s *Service) Open(ctx context.Context, req OpenRequest) (OpenResult, error) {
	if err := req.Validate(); err != nil {
		return OpenResult{}, err
	}
	release, err := s.enterMutation(req.Ref)
	if err != nil {
		return OpenResult{}, err
	}
	defer release()

	current, err := s.require(req.Ref)
	if err != nil {
		return OpenResult{}, err
	}
	now := s.now()
	attachEvent := session.Event{
		Kind:       session.EventAttached,
		Generation: current.Generation,
		At:         now,
	}
	if _, err := authorize(current, attachEvent); err != nil {
		return OpenResult{}, err
	}

	// A session without an attempt cannot hold a lease, and the reducer has
	// already refused it above; asking the broker anyway would only produce a
	// second, less specific refusal.
	var lease InteractiveLease
	if current.Generation != 0 {
		lease, err = s.leases.Acquire(ctx, session.Lease{
			SessionID:  current.ID,
			Generation: current.Generation,
			Holder:     req.Holder,
			At:         now,
		})
		if err != nil {
			return OpenResult{}, leaseError(current, "the interactive lease could not be granted", err)
		}
	}

	if _, err := s.apply(current.ID, attachEvent); err != nil {
		if rollbackErr := s.rollbackOpen(ctx, current, lease, nil); rollbackErr != nil {
			return OpenResult{}, rollbackErr
		}
		return OpenResult{}, err
	}

	var subscription TerminalSubscription
	if lease.SessionID != "" {
		subscription, err = s.terminals.Subscribe(ctx, lease)
		if err != nil {
			if rollbackErr := s.rollbackOpen(ctx, current, lease, nil); rollbackErr != nil {
				return OpenResult{}, rollbackErr
			}
			return OpenResult{}, session.WrapError(session.CodeSessionIOFailed, string(current.ID),
				"the session has no usable terminal: "+err.Error(),
				"the session and its process are untouched; restart the session to get a terminal", err).
				ForAttempt(current.Generation)
		}
		s.rememberLease(current.ID, current.Generation, heldLease{lease: lease, subscription: subscription})
	}

	revision, err := s.commit(ctx)
	if err != nil {
		if rollbackErr := s.rollbackOpen(ctx, current, lease, subscription); rollbackErr != nil {
			return OpenResult{}, rollbackErr
		}
		return OpenResult{}, err
	}
	row, found := s.rowOf(current.ID)
	if !found {
		return OpenResult{}, session.NewError(session.CodeNotFound, string(current.ID),
			"the session disappeared while it was being attached", "list the sessions to see what is stored")
	}
	event, perr := s.publish(revision, events.TypeSessionOpened, row, s.now(), eventExtras{transition: attachTransition})
	if perr != nil {
		return OpenResult{Session: row, Revision: revision, Lease: lease, Terminal: subscription}, perr
	}
	return OpenResult{Session: row, Revision: revision, Lease: lease, Terminal: subscription, Event: event}, nil
}

// The attachment axis has no numbered row of its own: attaching and detaching
// are deliberately outside the lifecycle table.
const attachTransition = "attach"

// rollbackOpen undoes an attach that could not be completed. It returns nil when
// the attach was undone completely, and the failure that stopped it otherwise, so
// the caller reports either its own cause or a rollback that could not finish.
//
// The rollback runs in the reverse order of the attach: the bookkeeping goes
// first so the owner cannot forget a lease it still owns, then the stream and the
// lease are released through their ports, and only then is the recorded
// attachment undone. Undoing the attachment is an in-memory reconciliation, not a
// second mutation - the failure that brought us here has already latched the
// owner, so nothing may be written to the store - and it is skipped when the
// attachment was never recorded.
func (s *Service) rollbackOpen(ctx context.Context, current session.Session, lease InteractiveLease, subscription TerminalSubscription) error {
	if lease.SessionID == "" {
		return nil
	}
	s.forgetLease(current.ID, current.Generation)
	var failures []error
	if subscription != nil {
		if err := subscription.Close(); err != nil {
			failures = append(failures, fmt.Errorf("close the terminal stream: %w", err))
		}
	}
	if err := s.leases.Release(ctx, lease); err != nil {
		failures = append(failures, fmt.Errorf("release the interactive lease: %w", err))
	}
	if len(failures) > 0 {
		return releaseError(current.ID, current.Generation,
			"releasing the interactive claim of the unpersisted attach", errors.Join(failures...))
	}
	recorded, found := s.engine.Get(current.ID)
	if !found {
		return nil
	}
	attempt, hasAttempt := recorded.Current()
	if !hasAttempt || attempt.Attachment != session.AttachmentAttached {
		return nil
	}
	if _, err := s.apply(current.ID, session.Event{
		Kind:       session.EventDetached,
		Generation: current.Generation,
		At:         s.now(),
	}); err != nil {
		return err
	}
	return nil
}

// Detach releases an interactive lease and changes only the attachment.
//
// Leaving the interactive view is never a lifecycle event: the reducer's detach
// row touches the attachment axis alone, so a client disconnect cannot terminate
// an agent that is still running.
func (s *Service) Detach(ctx context.Context, req DetachRequest) (DetachResult, error) {
	if err := req.Validate(); err != nil {
		return DetachResult{}, err
	}
	release, err := s.enterMutation(req.Ref)
	if err != nil {
		return DetachResult{}, err
	}
	defer release()

	current, err := s.require(req.Ref)
	if err != nil {
		return DetachResult{}, err
	}
	detachEvent := session.Event{
		Kind:       session.EventDetached,
		Generation: current.Generation,
		At:         s.now(),
	}
	if _, err := authorize(current, detachEvent); err != nil {
		return DetachResult{}, err
	}

	// Releasing a lease for a finished attempt is never an error, so a client that
	// goes away mid-restart still detaches cleanly.
	if held, heldHere := s.takeLease(current.ID, current.Generation); heldHere {
		if held.subscription != nil {
			if err := held.subscription.Close(); err != nil {
				return DetachResult{}, releaseError(current.ID, current.Generation, "closing the terminal stream", err)
			}
		}
		if err := s.leases.Release(ctx, held.lease); err != nil {
			return DetachResult{}, releaseError(current.ID, current.Generation, "releasing the interactive lease", err)
		}
	}

	if _, err := s.apply(current.ID, detachEvent); err != nil {
		return DetachResult{}, err
	}
	revision, err := s.commit(ctx)
	if err != nil {
		return DetachResult{}, err
	}
	row, found := s.rowOf(current.ID)
	if !found {
		return DetachResult{}, session.NewError(session.CodeNotFound, string(current.ID),
			"the session disappeared while it was being detached", "list the sessions to see what is stored")
	}
	event, perr := s.publish(revision, events.TypeSessionDetached, row, s.now(), eventExtras{transition: detachTransition})
	if perr != nil {
		return DetachResult{Session: row, Revision: revision}, perr
	}
	return DetachResult{Session: row, Revision: revision, Event: event}, nil
}

const detachTransition = "detach"

// Stop asks for a graceful stop.
//
// The stop is one SIGTERM to the verified owned process group plus a bounded wait.
// If the child outlives the window the session stays running and the error says
// so, because escalating to SIGKILL is an explicit separate action and a stop
// timeout is never reported as a kill.
//
// The exit is recorded only from the evidence the runtime brought back, and the
// evidence is checked rather than trusted: a child killed by SIGTERM is reaped
// with no exit code at all, which is a successful stop, while a reap that names
// neither a code, a signal nor an observation is not an exit and leaves the
// session recoverable instead of wedged in stopping.
func (s *Service) Stop(ctx context.Context, req StopRequest) (StopResult, error) {
	if err := req.Validate(); err != nil {
		return StopResult{}, err
	}
	release, err := s.enterMutation(req.Ref)
	if err != nil {
		return StopResult{}, err
	}
	defer release()

	current, err := s.require(req.Ref)
	if err != nil {
		return StopResult{}, err
	}
	attempt, hasAttempt := current.Current()
	if !hasAttempt {
		return StopResult{}, session.NewError(session.CodeNotRunning, string(current.ID),
			"the session has no attempt to stop", "start the session first, then stop it")
	}
	grace := req.Grace
	if grace <= 0 {
		grace = s.grace
	}
	stopEvent := session.Event{
		Kind:       session.EventStopRequested,
		Generation: current.Generation,
		At:         s.now(),
	}
	if _, err := authorize(current, stopEvent); err != nil {
		return StopResult{}, err
	}
	if _, err := s.apply(current.ID, stopEvent); err != nil {
		return StopResult{}, err
	}

	outcome, observeErr := s.runtime.Stop(ctx, attempt.Identity, grace)
	// A failure to observe the child is not evidence that it exited, and neither is
	// a reap the runtime could not describe: both are recorded as a timeout, so the
	// session stays running and honest.
	reason, evidenceErr := stoppedReason(outcome)
	if observeErr == nil && evidenceErr == nil {
		if _, err := s.apply(current.ID, session.Event{
			Kind:       session.EventChildExited,
			Generation: current.Generation,
			At:         s.now(),
			Reason:     reason,
		}); err != nil {
			return s.stoppedByAnotherObserver(ctx, current, err)
		}
		row, revision, perr := s.commitAndRow(ctx, current.ID, events.TypeSessionStopped, session.RowStopped, false)
		if perr != nil {
			return StopResult{Stopped: true, Session: row, Revision: revision}, perr
		}
		// The exit is persisted before the claim is released: the exit is the fact
		// the caller asked for, and a stream that refuses to close must not leave it
		// unrecorded.
		if err := s.releaseTerminalAttempt(ctx, current.ID, current.Generation); err != nil {
			return StopResult{Stopped: true, Session: row, Revision: revision}, err
		}
		return StopResult{Stopped: true, Session: row, Revision: revision}, nil
	}

	if _, err := s.apply(current.ID, session.Event{
		Kind:       session.EventStopTimeout,
		Generation: current.Generation,
		At:         s.now(),
	}); err != nil {
		return StopResult{}, err
	}
	row, revision, perr := s.commitAndRow(ctx, current.ID, TypeSessionStopTimeout, session.RowStopTimeout, true)
	if perr != nil {
		return StopResult{TimedOut: true, Session: row, Revision: revision}, perr
	}
	return StopResult{TimedOut: true, Session: row, Revision: revision}, stopNotReaped(current, row, outcome, observeErr, evidenceErr)
}

// stoppedReason renders the recorded cause of a reaped child inside the graceful
// stop window, and refuses to render anything at all without terminal evidence.
//
// The runtime's own outcome is the authority on how the child ended: its exit
// code, its terminating signal or, for a runtime whose wait status carries
// neither, its observation line. That evidence line is what keeps the ordinary
// POSIX case - a child killed by SIGTERM and reaped with WIFSIGNALED - from
// being refused, which would leave the session in stopping forever.
func stoppedReason(outcome session.StopOutcome) (session.Reason, error) {
	if !outcome.Exited {
		return session.Reason{}, errors.New("the child was not reaped inside the graceful stop window")
	}
	reason := outcome.StoppedReason()
	if err := reason.Validate(); err != nil {
		return session.Reason{}, fmt.Errorf("the runtime reported a reap with no exit code, terminating signal or evidence line: %w", err)
	}
	return reason, nil
}

// stoppedByAnotherObserver reconciles a stop whose exit was already recorded.
//
// The owner's single waiter reaps the child, and it may land its report while
// this stop is still inside its grace window. The reducer then refuses the exit
// as a duplicate - correctly, since the attempt is already closed - but the stop
// achieved exactly what it was asked to do, so it reports success instead of a
// failure that reads as if nothing stopped. Nothing is written here: the observer
// that won the race committed the state, and this use case only re-reads it.
func (s *Service) stoppedByAnotherObserver(ctx context.Context, current session.Session, cause error) (StopResult, error) {
	latest, found := s.engine.Get(current.ID)
	if !found {
		return StopResult{}, cause
	}
	ended, ok := latest.FindAttempt(current.Generation)
	if !ok || !ended.Lifecycle.Terminal() {
		return StopResult{}, cause
	}
	if err := s.releaseTerminalAttempt(ctx, current.ID, current.Generation); err != nil {
		return StopResult{Stopped: true}, err
	}
	row, ok := s.rowOf(current.ID)
	if !ok {
		return StopResult{Stopped: true}, cause
	}
	return StopResult{Stopped: true, Session: row, Revision: s.Revision()}, nil
}

// stopNotReaped reports the T9 outcome. It names which of the three
// unverifiable situations happened and never reads as a kill: in all of them the
// session is still running.
func stopNotReaped(current session.Session, row session.SessionSnapshot, outcome session.StopOutcome, observeErr, evidenceErr error) error {
	switch {
	case observeErr != nil:
		return session.WrapError(session.CodeConflict, string(current.ID),
			"the graceful stop could not be observed, so the session is still running: "+observeErr.Error(),
			"check the process by its identity, then stop again or end it explicitly with asd kill "+string(current.ID)+" --yes",
			observeErr).ForAttempt(current.Generation)
	case outcome.Exited:
		// The runtime claimed a reap but named nothing that could be recorded, so
		// the exit is not evidence and the attempt stays recoverable rather than
		// wedged in stopping.
		return session.WrapError(session.CodeConflict, string(current.ID),
			"the runtime reported the child reaped but named no exit code, terminating signal or reap evidence, "+
				"so no exit was recorded and the session is still recorded as running: "+evidenceErr.Error(),
			"reconcile the session against its recorded process identity, then stop it again",
			evidenceErr).ForAttempt(current.Generation)
	default:
		return stopTimeoutError(row)
	}
}

// Kill terminates a session through an explicit forced kill of its verified owned
// process group.
//
// Only SIGKILL is a kill: a graceful stop is the separate Stop use case, and a
// record that conflates the two loses the only evidence of which one happened.
// Nothing is recorded before the reap is confirmed either - the reducer refuses an
// exit without terminal evidence, and a session that claimed to be killed while
// its child is still running would be a lie no later observation could repair,
// because the reducer refuses observations on a terminal attempt.
func (s *Service) Kill(ctx context.Context, req KillRequest) (KillResult, error) {
	if err := req.Validate(); err != nil {
		return KillResult{}, err
	}
	release, err := s.enterMutation(req.Ref)
	if err != nil {
		return KillResult{}, err
	}
	defer release()

	current, err := s.require(req.Ref)
	if err != nil {
		return KillResult{}, err
	}
	attempt, hasAttempt := current.Current()
	if !hasAttempt {
		return KillResult{}, session.NewError(session.CodeNotRunning, string(current.ID),
			"the session has no attempt to kill", "start the session first, then kill it")
	}
	killEvent := session.Event{
		Kind:       session.EventKillRequested,
		Generation: current.Generation,
		At:         s.now(),
		Identity:   attempt.Identity,
		Reason:     session.Killed(string(session.SignalKill)),
	}
	// Authorize first, so a finished attempt, an unconfirmed spawn or an attempt
	// with no verified identity is refused before anything is signalled.
	if _, err := authorize(current, killEvent); err != nil {
		return KillResult{}, err
	}

	outcome, killErr := s.runtime.ForceKill(ctx, attempt.Identity, s.grace)
	if killErr != nil {
		return KillResult{}, signalError(current.ID, current.Generation, session.SignalKill, killErr)
	}
	if err := outcome.Validate(); err != nil {
		return KillResult{}, unconfirmedKillError(current, fmt.Errorf(
			"the runtime returned neither a delivered signal nor a confirmed reap: %w", err))
	}
	reason, err := outcome.KilledReason()
	if err != nil {
		return KillResult{}, unconfirmedKillError(current, err)
	}

	killEvent.At = s.now()
	killEvent.Reason = reason
	if _, err := s.apply(current.ID, killEvent); err != nil {
		return KillResult{}, err
	}
	revision, err := s.commit(ctx)
	if err != nil {
		return KillResult{Signal: session.SignalKill}, err
	}
	row, found := s.rowOf(current.ID)
	if !found {
		return KillResult{Signal: session.SignalKill}, session.NewError(session.CodeNotFound, string(current.ID),
			"the session disappeared while it was being killed", "list the sessions to see what is stored")
	}
	// The confirmed exit is persisted before the claim is released: the exit is the
	// fact the caller asked for, and a stream that refuses to close must not leave it
	// unrecorded.
	if err := s.releaseTerminalAttempt(ctx, current.ID, current.Generation); err != nil {
		return KillResult{Session: row, Signal: session.SignalKill, Revision: revision}, err
	}
	event, perr := s.publish(revision, events.TypeSessionKilled, row, s.now(), eventExtras{transition: session.RowKilled})
	if perr != nil {
		return KillResult{Session: row, Signal: session.SignalKill, Revision: revision}, perr
	}
	return KillResult{Session: row, Signal: session.SignalKill, Revision: revision, Event: event}, nil
}

// unconfirmedKillError reports a force kill that was not confirmed by a reap.
//
// The session stays active and the record stays unchanged: a delivered SIGKILL is
// evidence that a signal reached the verified group, not that the group is gone.
// A member that survived the signal, or a runtime that returned before the reap,
// would otherwise be recorded as an exit nothing could ever correct.
func unconfirmedKillError(current session.Session, cause error) error {
	return session.WrapError(session.CodeConflict, string(current.ID),
		"no reap was confirmed for the forced kill of the verified process group, so no exit was recorded and the session is still recorded as running: "+
			cause.Error(),
		"check the process by its recorded identity, then kill again or stop it explicitly with asd stop "+string(current.ID),
		cause).ForAttempt(current.Generation)
}

// Restart opens a new attempt of an existing session.
//
// The session id never changes; the attempt generation does. Restarting a live
// attempt needs Force and is serialized as stop-then-open, so there is never more
// than one live attempt, and a stop window that closes with the child still alive
// leaves the old attempt running and the restart unfinished rather than opening a
// second one. Restart re-runs the resolved command: it never resumes a vendor
// conversation, so the previous attempt's exit reason stays immutable history.
func (s *Service) Restart(ctx context.Context, req RestartRequest) (RestartResult, error) {
	if err := req.Validate(); err != nil {
		return RestartResult{}, err
	}
	release, err := s.enterMutation(req.Ref)
	if err != nil {
		return RestartResult{}, err
	}
	defer release()

	current, err := s.require(req.Ref)
	if err != nil {
		return RestartResult{}, err
	}
	attempt, hasAttempt := current.Current()
	if !hasAttempt {
		return RestartResult{}, session.NewError(session.CodeConflict, string(current.ID),
			"the session has no attempt to restart", "start the session first, then restart it")
	}
	if current.Orphaned() {
		return RestartResult{}, session.NewError(session.CodeConflict, string(current.ID),
			"the session has a surviving process but no terminal, so a restart would leave it unowned",
			"resolve the surviving process first: inspect it, then stop or kill it explicitly").ForAttempt(current.Generation)
	}
	if !req.Force && !attempt.Lifecycle.Terminal() {
		return RestartResult{}, session.NewError(session.CodeConflict, string(current.ID),
			"restarting a live attempt (lifecycle "+attempt.Lifecycle.String()+") needs an explicit force confirmation",
			"stop or kill the session first, or confirm the interruption with --force").ForAttempt(current.Generation)
	}

	ws, command, err := s.restartCommand(ctx, current, attempt)
	if err != nil {
		return RestartResult{}, err
	}

	forcedLive := !attempt.Lifecycle.Terminal()
	if forcedLive {
		// T11: stop the live attempt and mark it restart-pending. The next attempt
		// opens only after the exit has actually been observed.
		stopEvent := session.Event{
			Kind:        session.EventRestartRequested,
			Generation:  current.Generation,
			At:          s.now(),
			Command:     command,
			WorkspaceID: current.WorkspaceID,
			Forced:      true,
		}
		if _, err := authorize(current, stopEvent); err != nil {
			return RestartResult{}, err
		}
		if _, err := s.apply(current.ID, stopEvent); err != nil {
			return RestartResult{}, err
		}
		outcome, observeErr := s.runtime.Stop(ctx, attempt.Identity, s.grace)
		reason, evidenceErr := stoppedReason(outcome)
		if observeErr != nil || evidenceErr != nil {
			return s.abandonRestart(ctx, current, firstOf(observeErr, evidenceErr))
		}
		if _, err := s.apply(current.ID, session.Event{
			Kind:       session.EventChildExited,
			Generation: current.Generation,
			At:         s.now(),
			Reason:     reason,
		}); err != nil {
			return RestartResult{}, err
		}
		// The attempt this claim is bound to is over, so the next generation may
		// attach without anyone clearing a lease by hand. The release happens before
		// the new attempt is spawned, because a claim that could not be released must
		// stop the restart: the new attempt would not be attachable either, and
		// opening it anyway would leave the client attached to a dead session.
		if err := s.releaseTerminalAttempt(ctx, current.ID, current.Generation); err != nil {
			return RestartResult{}, err
		}
	}

	latest, found := s.engine.Get(current.ID)
	if !found {
		return RestartResult{}, session.NewError(session.CodeNotFound, string(current.ID),
			"the session disappeared while it was restarting", "list the sessions to see what is stored")
	}
	generation := latest.Generation
	// T12: open the next generation on the same session id.
	openEvent := session.Event{
		Kind:        session.EventRestartRequested,
		Generation:  generation,
		At:          s.now(),
		Command:     command,
		WorkspaceID: current.WorkspaceID,
	}
	if _, err := authorize(latest, openEvent); err != nil {
		return RestartResult{}, err
	}
	if _, err := s.apply(current.ID, openEvent); err != nil {
		return RestartResult{}, err
	}

	launchReq := session.LaunchRequest{
		SessionID:  current.ID,
		Generation: generation + 1,
		Command:    command,
		Workspace:  ws,
	}
	if err := launchReq.Validate(); err != nil {
		return RestartResult{}, err
	}
	identity, launchErr := s.runtime.Launch(ctx, launchReq)
	if launchErr != nil {
		return s.failRestart(ctx, current.ID, launchReq.Generation,
			"the process runtime refused to spawn the child: "+launchErr.Error())
	}
	if verr := identity.Validate(); verr != nil {
		return s.failRestart(ctx, current.ID, launchReq.Generation,
			"the spawn did not return a verifiable process identity: "+verr.Error()+
				"; the child could not be cleaned up because its identity is unknown")
	}
	if _, err := s.apply(current.ID, session.Event{
		Kind:       session.EventSpawned,
		Generation: launchReq.Generation,
		At:         s.now(),
		Identity:   identity,
	}); err != nil {
		return RestartResult{}, err
	}

	revision, err := s.commit(ctx)
	if err != nil {
		return RestartResult{}, s.cleanupAfterPersistFailure(ctx, current.ID, launchReq.Generation, identity, err)
	}
	row, found := s.rowOf(current.ID)
	if !found {
		return RestartResult{}, session.NewError(session.CodeNotFound, string(current.ID),
			"the session disappeared while it was restarting", "list the sessions to see what is stored")
	}
	event, perr := s.publish(revision, events.TypeSessionRestarted, row, s.now(), eventExtras{transition: session.RowRestartTerminal})
	if perr != nil {
		return RestartResult{Session: row, PreviousGeneration: current.Generation, Revision: revision}, perr
	}
	return RestartResult{Session: row, PreviousGeneration: current.Generation, Revision: revision, Event: event}, nil
}

// restartCommand returns the workspace and the command the next attempt re-runs.
//
// An attempt normally carries the frozen command it launched, and restart re-runs
// exactly that command in exactly that workspace. Only an attempt that never
// resolved a command has to ask the ports again, and then the stored workspace
// identity is resolved like an explicit candidate: it is a directory the owner
// already verified once.
func (s *Service) restartCommand(ctx context.Context, current session.Session, attempt session.Attempt) (workspace.Workspace, agent.Command, error) {
	if attempt.HasCommand() {
		ws, err := workspace.New(current.WorkspaceID.Path())
		if err != nil {
			return workspace.Workspace{}, agent.Command{}, session.WrapError(session.CodeCorruptState, string(current.ID),
				"the stored workspace identity cannot be read: "+err.Error(),
				"repair the stored workspace path by hand; asd never rewrites it silently", err)
		}
		return ws, attempt.Command, nil
	}
	ws, err := s.resolveWorkspace(ctx, CreateRequest{
		Agent:           current.AgentID,
		WorkspacePath:   current.WorkspaceID.Path(),
		WorkspaceSource: workspace.SourceExplicit,
	})
	if err != nil {
		return workspace.Workspace{}, agent.Command{}, err
	}
	command, err := s.resolveCommand(ctx, CreateRequest{}, current.AgentID, ws, current.Name)
	if err != nil {
		return workspace.Workspace{}, agent.Command{}, err
	}
	return ws, command, nil
}

// abandonRestart records the T9 outcome of a forced restart whose stop window
// closed and refuses to open a second attempt.
func (s *Service) abandonRestart(ctx context.Context, current session.Session, cause error) (RestartResult, error) {
	if _, err := s.apply(current.ID, session.Event{
		Kind:       session.EventStopTimeout,
		Generation: current.Generation,
		At:         s.now(),
	}); err != nil {
		return RestartResult{}, err
	}
	row, revision, perr := s.commitAndRow(ctx, current.ID, TypeSessionStopTimeout, session.RowStopTimeout, true)
	if perr != nil {
		return RestartResult{Session: row, PreviousGeneration: current.Generation, Revision: revision}, perr
	}
	timeout := stopTimeoutError(row)
	if cause != nil {
		timeout = session.WrapError(timeout.(*session.Error).Code, string(current.ID),
			"the graceful restart could not be observed, so the previous attempt is still running: "+cause.Error(),
			"wait longer and restart again, or end it explicitly with asd kill "+string(current.ID)+" --yes",
			cause).ForAttempt(current.Generation)
	}
	return RestartResult{Session: row, PreviousGeneration: current.Generation, Revision: revision}, timeout
}

// failRestart records a failed attempt of a restart. The session id and the new
// generation stay: the failure belongs to an attempt, so the next retry is another
// restart rather than a new session.
func (s *Service) failRestart(ctx context.Context, id session.ID, generation session.Generation, reason string) (RestartResult, error) {
	if _, err := s.apply(id, session.Event{
		Kind:       session.EventSpawnFailed,
		Generation: generation,
		At:         s.now(),
		Failure:    reason,
	}); err != nil {
		return RestartResult{}, err
	}
	revision, commitErr := s.commit(ctx)
	if commitErr != nil {
		return RestartResult{}, commitErr
	}
	row, found := s.rowOf(id)
	if !found {
		return RestartResult{}, session.NewError(session.CodeNotFound, string(id),
			"the session disappeared while its restart was failing", "list the sessions to see what is stored")
	}
	if _, err := s.publish(revision, TypeSessionLaunchFailed, row, s.now(), eventExtras{transition: session.RowSpawnFailed}); err != nil {
		return RestartResult{Session: row, PreviousGeneration: generation - 1, Revision: revision}, err
	}
	return RestartResult{Session: row, PreviousGeneration: generation - 1, Revision: revision},
		launchError(id, generation, reason)
}

// Report applies an asynchronous observation the runtime owner observed.
//
// The observation names the attempt it was made for, and the reducer fences it: a
// report from an attempt that has already been replaced is reported as stale and
// changes nothing, which is exactly what a late callback from a restarted attempt
// must do. A report for an attempt the session has not reached is a different
// failure - the runtime mis-attributed the observation - and it is raised as a
// typed conflict rather than tolerated as a benign late callback.
func (s *Service) Report(ctx context.Context, req ReportRequest) (ReportResult, error) {
	if err := req.Validate(); err != nil {
		return ReportResult{}, err
	}
	release, err := s.enterMutation(req.Ref)
	if err != nil {
		return ReportResult{}, err
	}
	defer release()

	current, err := s.require(req.Ref)
	if err != nil {
		return ReportResult{}, err
	}
	generation := req.Attempt
	switch {
	case generation == 0:
		generation = current.Generation
	case generation > current.Generation:
		return ReportResult{}, futureGenerationError(current, generation)
	}
	event := session.Event{
		Kind:       reportEventKind(req.Kind),
		Generation: generation,
		At:         req.at(s.clock),
	}
	switch req.Kind {
	case ReportChildExited:
		event.Reason = req.Reason
	default:
		event.Liveness = req.Liveness
	}
	if _, err := authorize(current, event); err != nil {
		return s.staleOrError(current, err)
	}
	outcome, err := s.apply(current.ID, event)
	if err != nil {
		return s.staleOrError(current, err)
	}
	revision, err := s.commit(ctx)
	if err != nil {
		return ReportResult{}, err
	}
	updated, found := s.engine.Get(current.ID)
	if !found {
		return ReportResult{}, session.NewError(session.CodeNotFound, string(current.ID),
			"the session disappeared while an observation was being applied", "list the sessions to see what is stored")
	}
	row := s.row(updated, s.now())
	// An attempt that just ended has no stream to read and no client to serve, so
	// its generation-bound claim is released here too. Otherwise a session that
	// ends without an explicit stop or kill would keep its lease until the next
	// generation, and the next attach would be refused by a broker nobody can clear.
	// The observation is persisted first: it is the fact the report is about.
	if outcome.Session.State().Lifecycle.Terminal() {
		if err := s.releaseTerminalAttempt(ctx, current.ID, generation); err != nil {
			return ReportResult{Session: row, Row: outcome.Row, Revision: revision}, err
		}
	}
	eventOut, perr := s.publish(revision, reportEventType(outcome.Row, updated), row, s.now(), eventExtras{transition: outcome.Row})
	if perr != nil {
		return ReportResult{Session: row, Row: outcome.Row, Revision: revision}, perr
	}
	return ReportResult{Session: row, Row: outcome.Row, Revision: revision, Event: eventOut}, nil
}

// staleOrError reports a fenced callback as the designed outcome it is, and
// every other refusal as the typed error it is.
func (s *Service) staleOrError(current session.Session, err error) (ReportResult, error) {
	if !isStaleAttempt(err) {
		return ReportResult{}, err
	}
	return ReportResult{
		Session: s.row(current, s.now()),
		Row:     session.RowStaleCallback,
		Stale:   true,
	}, nil
}

// futureGenerationError reports an observation for an attempt the session has not
// reached. It is deliberately not the benign stale-callback outcome: a runtime
// that attributes an exit to the wrong attempt is a defect, and tolerating it
// silently would let a live child look dead or a dead child look alive.
func futureGenerationError(current session.Session, generation session.Generation) error {
	return session.WrapError(session.CodeConflict, string(current.ID),
		fmt.Sprintf("the observation names attempt %d, but the session is at attempt %d", uint64(generation), uint64(current.Generation)),
		"read the session state, then report the observation for the attempt that owns the child",
		session.ErrFutureGeneration).ForAttempt(generation)
}

func reportEventKind(kind ReportKind) session.EventKind {
	switch kind {
	case ReportChildExited:
		return session.EventChildExited
	case ReportPTYLost:
		return session.EventPTYLost
	case ReportProbeInconclusive:
		return session.EventProbeInconclusive
	default:
		return session.EventReconciled
	}
}

// reportEventType names what the applied observation did to the session, not what
// the caller asked for.
//
// It is derived from the contract row the reducer implemented, because the four
// answers a liveness observation can produce are genuinely different facts and
// only one of them is a death: a reap or a vanished process closed the attempt, a
// live pid behind another identity closed the old observation (stale), a
// surviving process without I/O left it running (orphaned), and an inconclusive
// probe or an unverifiable PTY loss left it running but unknown. Publishing
// session.dead for the last two would tell a consumer to mark a live session dead,
// which is the one thing the state machine forbids (T15, T18).
func reportEventType(row string, applied session.Session) events.Type {
	switch row {
	case session.RowNaturalExit, session.RowStopped, session.RowProcessGone:
		return events.TypeSessionDead
	case session.RowIdentityMismatch:
		return TypeSessionStale
	case session.RowPTYLostAlive:
		return TypeSessionOrphaned
	case session.RowPTYLostUnverifiable, session.RowProbeInconclusive:
		return TypeSessionUnknown
	}
	if applied.Orphaned() {
		return TypeSessionOrphaned
	}
	if applied.Lifecycle().Terminal() {
		return events.TypeSessionDead
	}
	// Positive liveness confirmed: the shared event set has no separate liveness
	// kind, and session.started is the PRODUCT kind that means "this session has a
	// live child".
	return events.TypeSessionStarted
}

func isStaleAttempt(err error) bool {
	var typed *session.Error
	return errors.As(err, &typed) && typed.Code == session.CodeStaleAttempt
}

func firstOf(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Interactive lease bookkeeping
// ---------------------------------------------------------------------------

// rememberLease records the claim the owner granted for one attempt.
func (s *Service) rememberLease(id session.ID, generation session.Generation, held heldLease) {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	s.held[attemptKey{session: id, generation: generation}] = held
}

// takeLease removes and returns the claim the owner holds for one attempt.
func (s *Service) takeLease(id session.ID, generation session.Generation) (heldLease, bool) {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	key := attemptKey{session: id, generation: generation}
	held, ok := s.held[key]
	delete(s.held, key)
	return held, ok
}

// forgetLease drops bookkeeping for a claim whose lease and stream are already
// gone. The ledger is never authoritative about the broker: a broker that
// outlives the caller's bookkeeping must not leave the owner unable to remember
// that it is finished with an attempt.
func (s *Service) forgetLease(id session.ID, generation session.Generation) {
	s.ioMu.Lock()
	defer s.ioMu.Unlock()
	delete(s.held, attemptKey{session: id, generation: generation})
}

// releaseClaim closes the stream and releases the lease of one attempt, and
// forgets it.
//
// It is idempotent: an attempt with no remembered claim releases nothing, which is
// exactly what a stale call needs. The bookkeeping is dropped before the ports are
// called, so a lease that cannot be released is never retried against a claim the
// owner no longer holds, and the release failures are joined rather than dropped -
// a caller has to learn about a stream that did not close even when the lease did.
func (s *Service) releaseClaim(ctx context.Context, id session.ID, generation session.Generation) error {
	held, ok := s.takeLease(id, generation)
	if !ok {
		return nil
	}
	var failures []error
	if held.subscription != nil {
		if err := held.subscription.Close(); err != nil {
			failures = append(failures, fmt.Errorf("close the terminal stream: %w", err))
		}
	}
	if err := s.leases.Release(ctx, held.lease); err != nil {
		failures = append(failures, fmt.Errorf("release the interactive lease: %w", err))
	}
	return errors.Join(failures...)
}

// releaseTerminalAttempt releases the interactive claim of an attempt that has
// just ended, whichever way it ended.
//
// A lease is bound to its attempt, and a terminal attempt has no stream to read:
// leaving it held would keep a file descriptor open, leave the client attached to
// a dead session, and block the next generation of the same session from attaching
// - a failure no client can clear, because the only thing that releases a lease is
// a detach and a finished session has nothing to detach.
func (s *Service) releaseTerminalAttempt(ctx context.Context, id session.ID, generation session.Generation) error {
	if err := s.releaseClaim(ctx, id, generation); err != nil {
		return releaseError(id, generation, "releasing the interactive lease of the finished attempt", err)
	}
	return nil
}

func leaseError(current session.Session, reason string, cause error) error {
	code := session.CodeOf(cause)
	if code == "" || code == session.CodeUnknown {
		code = session.CodeConflict
	}
	return session.WrapError(code, string(current.ID), reason+": "+cause.Error(),
		"another client holds this session; detach it there or wait for it to leave", cause).
		ForAttempt(current.Generation)
}

// commitAndRow writes the committed mutation and announces it.
func (s *Service) commitAndRow(ctx context.Context, id session.ID, typ events.Type, transition string, stopTimedOut bool) (session.SessionSnapshot, uint64, error) {
	revision, err := s.commit(ctx)
	if err != nil {
		return session.SessionSnapshot{}, 0, err
	}
	row, found := s.rowOf(id)
	if !found {
		return session.SessionSnapshot{}, revision, session.NewError(session.CodeNotFound, string(id),
			"the session disappeared while the mutation was being written", "list the sessions to see what is stored")
	}
	if _, err := s.publish(revision, typ, row, s.now(), eventExtras{transition: transition, stopTimedOut: stopTimedOut}); err != nil {
		return row, revision, err
	}
	return row, revision, nil
}

// rowOf projects one registered session at the owner's authority.
func (s *Service) rowOf(id session.ID) (session.SessionSnapshot, bool) {
	current, found := s.engine.Get(id)
	if !found {
		return session.SessionSnapshot{}, false
	}
	return s.row(current, s.now()), true
}
