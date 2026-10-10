//go:build linux

package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/pty"
	domain "github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/terminal"
)

const (
	defaultColumns = 80
	defaultRows    = 24
	drainGrace     = 500 * time.Millisecond
)

// Runtime owns children launched by this process and refuses to signal a PID or
// process group unless the exact launch identity is still observable.
type Runtime struct {
	mu        sync.Mutex
	ownerID   string
	children  map[int]*ownedChild
	terminals *terminal.Manager
}

type ownedChild struct {
	id         domain.ID
	generation domain.Generation
	identity   domain.ProcessIdentity
	terminal   *terminal.Session
	child      *pty.Child
	done       chan struct{}
	status     domain.ExitStatus
	waitErr    error
	waitClaim  bool
}

// NewRuntime creates a Linux PTY process owner with bounded terminal state.
func NewRuntime(ownerID string, options terminal.Options) (*Runtime, error) {
	if strings.TrimSpace(ownerID) == "" {
		return nil, errors.New("process runtime owner instance id is empty")
	}
	terminals, err := terminal.NewManager(options)
	if err != nil {
		return nil, err
	}
	return &Runtime{ownerID: ownerID, children: make(map[int]*ownedChild), terminals: terminals}, nil
}

// Terminals exposes the runtime-owned terminal source for application wiring.
func (r *Runtime) Terminals() *terminal.Manager { return r.terminals }

// Launch starts the immutable command under a controlling PTY and registers its
// terminal drain before returning. The caller's context bounds launch only.
func (r *Runtime) Launch(ctx context.Context, req domain.LaunchRequest) (domain.ProcessIdentity, error) {
	if err := ctx.Err(); err != nil {
		return domain.ProcessIdentity{}, err
	}
	if err := req.Validate(); err != nil {
		return domain.ProcessIdentity{}, err
	}
	r.mu.Lock()
	var previous *ownedChild
	for _, candidate := range r.children {
		if candidate.id == req.SessionID && (previous == nil || candidate.generation > previous.generation) {
			previous = candidate
		}
	}
	r.mu.Unlock()
	if previous != nil {
		if req.Generation <= previous.generation {
			return domain.ProcessIdentity{}, fmt.Errorf("launch generation %d does not follow completed generation %d", req.Generation, previous.generation)
		}
		select {
		case <-previous.done:
		default:
			return domain.ProcessIdentity{}, errors.New("cannot start a new attempt while the previous owned child is running")
		}
		if err := r.terminals.Remove(req.SessionID, previous.generation); err != nil {
			return domain.ProcessIdentity{}, fmt.Errorf("remove completed terminal attempt %d: %w", previous.generation, err)
		}
	}
	argv := req.Command.Args()
	child, err := pty.Start(req.Command.Executable(), argv, req.Workspace.Path(), pty.Size{Columns: defaultColumns, Rows: defaultRows})
	if err != nil {
		return domain.ProcessIdentity{}, fmt.Errorf("start PTY child: %w", err)
	}
	identity, err := Capture(child.PID(), r.ownerID)
	if err != nil || identity.PGID != identity.PID {
		cleanupErr := cleanupUnverifiedChild(child)
		if err == nil {
			err = fmt.Errorf("PTY child is not leader of its process group: pid=%d pgid=%d", identity.PID, identity.PGID)
		}
		return domain.ProcessIdentity{}, errors.Join(fmt.Errorf("capture launched process identity: %w", err), cleanupErr)
	}
	if err := identity.Validate(); err != nil {
		cleanupErr := cleanupUnverifiedChild(child)
		return domain.ProcessIdentity{}, errors.Join(err, cleanupErr)
	}
	terminalSession, err := r.terminals.Register(req.SessionID, req.Generation, child, pty.Size{Columns: defaultColumns, Rows: defaultRows})
	if err != nil {
		cleanupErr := cleanupUnverifiedChild(child)
		return domain.ProcessIdentity{}, errors.Join(fmt.Errorf("register PTY terminal: %w", err), cleanupErr)
	}
	entry := &ownedChild{id: req.SessionID, generation: req.Generation, identity: identity, terminal: terminalSession, child: child, done: make(chan struct{})}
	r.mu.Lock()
	if existing := r.children[identity.PID]; existing != nil {
		select {
		case <-existing.done:
		default:
			r.mu.Unlock()
			cleanupErr := cleanupUnverifiedChild(child)
			return domain.ProcessIdentity{}, errors.Join(fmt.Errorf("process runtime already owns live pid %d", identity.PID), cleanupErr)
		}
	}
	r.children[identity.PID] = entry
	r.mu.Unlock()
	go r.reap(entry)
	return identity, nil
}

func cleanupUnverifiedChild(child *pty.Child) error {
	if child == nil {
		return nil
	}
	// The os.Process handle is from this exact successful Start call; unlike a
	// persisted PID, it remains the safe cleanup capability before identity capture.
	killErr := child.Kill()
	closeErr := child.Close()
	waitErr := child.Wait()
	if errors.Is(waitErr, os.ErrProcessDone) {
		waitErr = nil
	}
	return errors.Join(killErr, closeErr, waitErr)
}

func (r *Runtime) reap(entry *ownedChild) {
	waitErr := entry.child.Wait()
	status := exitStatus(entry.child, waitErr)
	if status.Valid() {
		waitErr = nil // A nonzero exit code is terminal evidence, not a wait failure.
	}
	_ = entry.child.Close()
	drainCtx, cancel := context.WithTimeout(context.Background(), drainGrace)
	drainErr := entry.terminal.WaitOutput(drainCtx)
	cancel()
	if drainErr != nil {
		status.Evidence += fmt.Sprintf("; terminal drain deadline expired: %v", drainErr)
	}
	if residual := residualGroupEvidence(entry.identity.PGID, drainGrace); residual != "" {
		status.Evidence += "; " + residual
	}
	r.mu.Lock()
	entry.status = status
	entry.waitErr = waitErr
	close(entry.done)
	r.mu.Unlock()
}

func residualGroupEvidence(pgid int, grace time.Duration) string {
	deadline := time.Now().Add(grace)
	for {
		members, err := GroupMembers(pgid)
		if err != nil {
			return fmt.Sprintf("process-group scan incomplete after leader reap: %v", err)
		}
		if len(members) == 0 {
			return ""
		}
		if !time.Now().Before(deadline) {
			return fmt.Sprintf("process-group members remain after leader reap: %v", members)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func exitStatus(child *pty.Child, waitErr error) domain.ExitStatus {
	status := domain.ExitStatus{At: time.Now().UTC()}
	if ps := child.ProcessState(); ps != nil {
		if code := ps.ExitCode(); code >= 0 {
			status.ExitCode = &code
		} else if ws, ok := ps.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			status.Signal = signalName(ws.Signal())
		}
		if ws, ok := ps.Sys().(syscall.WaitStatus); ok {
			status.Evidence = fmt.Sprintf("reaped pid=%d wait-status=%#x", child.PID(), uint32(ws))
		}
	}
	if status.ExitCode == nil && status.Signal == "" && status.Evidence == "" {
		status.Evidence = fmt.Sprintf("reaped pid=%d wait-error=%v", child.PID(), waitErr)
	}
	return status
}

func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGSTOP:
		return "SIGSTOP"
	default:
		return fmt.Sprintf("SIG%d", sig)
	}
}

func (r *Runtime) entryFor(recorded domain.ProcessIdentity) (*ownedChild, error) {
	if err := recorded.Validate(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	entry := r.children[recorded.PID]
	r.mu.Unlock()
	if entry == nil || !entry.identity.Equal(recorded) {
		return nil, fmt.Errorf("process identity is not owned by this runtime")
	}
	return entry, nil
}

func (r *Runtime) Observe(ctx context.Context, recorded domain.ProcessIdentity) (domain.LivenessObservation, error) {
	if err := ctx.Err(); err != nil {
		return domain.LivenessObservation{}, err
	}
	if _, err := r.entryFor(recorded); err != nil {
		return domain.LivenessObservation{}, err
	}
	return Observe(recorded), nil
}

func (r *Runtime) Signal(ctx context.Context, recorded domain.ProcessIdentity, signal domain.SignalKind) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entry, err := r.entryFor(recorded)
	if err != nil {
		return err
	}
	if signal != domain.SignalTerm && signal != domain.SignalKill {
		return fmt.Errorf("unsupported owned-process signal %q", signal)
	}
	select {
	case <-entry.done:
		return errors.New("owned process leader has exited; refusing to signal a possibly reused process group")
	default:
	}
	observation := Observe(recorded)
	if observation.Outcome != domain.ProbeAlive || !observation.Identity.Equal(recorded) {
		return fmt.Errorf("owned process identity is not verified: %s", observation.EvidenceLine())
	}
	var sig syscall.Signal
	if signal == domain.SignalTerm {
		sig = syscall.SIGTERM
	} else {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(-recorded.PGID, sig); err != nil {
		return fmt.Errorf("signal verified process group %d: %w", recorded.PGID, err)
	}
	return nil
}

func (r *Runtime) Stop(ctx context.Context, recorded domain.ProcessIdentity, grace time.Duration) (domain.StopOutcome, error) {
	if grace < 0 {
		return domain.StopOutcome{}, errors.New("stop grace cannot be negative")
	}
	entry, err := r.entryFor(recorded)
	if err != nil {
		return domain.StopOutcome{}, err
	}
	select {
	case <-entry.done:
		return stopOutcome(entry), nil
	default:
	}
	if err := r.Signal(ctx, recorded, domain.SignalTerm); err != nil {
		return domain.StopOutcome{}, err
	}
	if waitEntry(ctx, entry, grace) {
		return stopOutcome(entry), nil
	}
	observation := Observe(recorded)
	return domain.StopOutcome{Observation: observation}, nil
}

func stopOutcome(entry *ownedChild) domain.StopOutcome {
	status := cloneStatus(entry.status)
	return domain.StopOutcome{Exited: true, ExitCode: status.ExitCode, Signal: status.Signal, Evidence: status.Evidence,
		Observation: domain.LivenessObservation{Outcome: domain.ProbeGone, At: status.At}}
}

func (r *Runtime) WaitExit(ctx context.Context, wait domain.ExitWait) (domain.ExitStatus, error) {
	if err := wait.Validate(); err != nil {
		return domain.ExitStatus{}, err
	}
	entry, err := r.entryFor(wait.Identity)
	if err != nil {
		return domain.ExitStatus{}, err
	}
	if entry.id != wait.SessionID || entry.generation != wait.Generation {
		return domain.ExitStatus{}, errors.New("exit wait does not match the owned attempt")
	}
	r.mu.Lock()
	if entry.waitClaim {
		r.mu.Unlock()
		return domain.ExitStatus{}, domain.ErrExitAlreadyReaped
	}
	entry.waitClaim = true
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return domain.ExitStatus{}, ctx.Err()
	case <-entry.done:
		r.mu.Lock()
		defer r.mu.Unlock()
		return cloneStatus(entry.status), entry.waitErr
	}
}

func (r *Runtime) ForceKill(ctx context.Context, recorded domain.ProcessIdentity, grace time.Duration) (domain.KillOutcome, error) {
	if grace < 0 {
		return domain.KillOutcome{}, errors.New("kill grace cannot be negative")
	}
	entry, err := r.entryFor(recorded)
	if err != nil {
		return domain.KillOutcome{}, err
	}
	select {
	case <-entry.done:
		status := cloneStatus(entry.status)
		return domain.KillOutcome{Terminated: status}, nil
	default:
	}
	if err := r.Signal(ctx, recorded, domain.SignalKill); err != nil {
		return domain.KillOutcome{}, err
	}
	if waitEntry(ctx, entry, grace) {
		return domain.KillOutcome{Delivered: true, Signal: string(domain.SignalKill), Terminated: cloneStatus(entry.status)}, nil
	}
	return domain.KillOutcome{Delivered: true, Signal: string(domain.SignalKill), Timeout: true}, nil
}

func waitEntry(ctx context.Context, entry *ownedChild, grace time.Duration) bool {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-entry.done:
		return true
	case <-timer.C:
		return false
	case <-ctx.Done():
		return false
	}
}

func cloneStatus(status domain.ExitStatus) domain.ExitStatus {
	if status.ExitCode != nil {
		code := *status.ExitCode
		status.ExitCode = &code
	}
	return status
}

var _ domain.ChildRuntime = (*Runtime)(nil)
