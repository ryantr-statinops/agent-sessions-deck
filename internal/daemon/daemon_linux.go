//go:build linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ryantr-statinops/agent-sessions-deck/internal/app"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/config"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/ipc"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/owner"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/session"
	"github.com/ryantr-statinops/agent-sessions-deck/internal/store"
)

// Daemon wraps the background session runtime supervisor.
type Daemon struct {
	runtime *owner.Runtime
}

// New initializes a daemon instance as the sole owner of the state home.
func New(ctx context.Context, paths config.Paths, cfg config.Config) (*Daemon, error) {
	rt, err := owner.StartDaemon(ctx, paths, cfg)
	if err != nil {
		return nil, err
	}
	return &Daemon{runtime: rt}, nil
}

// Serve runs the daemon server loop until shutdown.
func (d *Daemon) Serve(ctx context.Context) error {
	return d.runtime.Serve(ctx)
}

// Shutdown initiates graceful termination of all active sessions and releases locks.
func (d *Daemon) Shutdown(ctx context.Context, grace time.Duration) (owner.ShutdownReport, error) {
	return d.runtime.Shutdown(ctx, grace)
}

// Close releases daemon resources without waiting for session shutdown.
func (d *Daemon) Close() error {
	return d.runtime.Close()
}

// InstanceID returns the unique daemon instance token.
func (d *Daemon) InstanceID() string {
	return d.runtime.InstanceID()
}

// StatusReport provides non-interactive diagnostic and health information for agentd.
type StatusReport struct {
	Status         string  `json:"status"` // "RUNNING", "STOPPED", "CONFLICT", "UNRESPONSIVE"
	PID            int     `json:"pid,omitempty"`
	InstanceID     string  `json:"instance_id,omitempty"`
	SocketPath     string  `json:"socket_path"`
	ActiveSessions int     `json:"active_sessions"`
	TotalSessions  int     `json:"total_sessions"`
	UptimeSeconds  float64 `json:"uptime_seconds,omitempty"`
	Mode           string  `json:"mode,omitempty"`
	Message        string  `json:"message,omitempty"`
}

// Status queries the status of the background daemon for the given state paths.
func Status(ctx context.Context, paths config.Paths) (StatusReport, error) {
	socketPath, err := ipc.SocketPath(paths)
	if err != nil {
		return StatusReport{}, err
	}

	report := StatusReport{
		SocketPath: socketPath,
		Status:     "STOPPED",
	}

	tokenData, readErr := os.ReadFile(paths.LockPath())
	var pid int
	var instanceID, mode string
	if readErr == nil {
		instanceID, pid, mode = parseLockToken(string(tokenData))
		report.PID = pid
		report.InstanceID = instanceID
		report.Mode = mode
	}

	client := ipc.NewClient(socketPath)
	pingCtx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()

	if err := client.Ping(pingCtx); err == nil {
		if mode == "v1-owner" {
			report.Status = "CONFLICT"
			report.Message = "A legacy V1 foreground owner is holding the state home lock."
			return report, nil
		}
		report.Status = "RUNNING"
		listRes, listErr := client.List(ctx, app.ListRequest{})
		if listErr == nil {
			report.TotalSessions = len(listRes.Snapshot.Sessions)
			active := 0
			for _, s := range listRes.Snapshot.Sessions {
				if !s.Lifecycle.Terminal() {
					active++
				}
			}
			report.ActiveSessions = active
		}
		return report, nil
	}

	// Ping failed; determine if process is alive or if there is a conflict
	if pid > 0 && isProcessAlive(pid) {
		if mode == "v1-owner" {
			report.Status = "CONFLICT"
			report.Message = "A legacy V1 foreground owner is holding the state home lock."
		} else {
			report.Status = "UNRESPONSIVE"
			report.Message = "Daemon process is running but control socket is not responding."
		}
		return report, nil
	}

	report.Status = "STOPPED"
	report.Message = "Daemon is not running."
	return report, nil
}

// Stop sends a graceful termination signal to the running daemon.
func Stop(ctx context.Context, paths config.Paths, force bool, timeout time.Duration) error {
	rep, err := Status(ctx, paths)
	if err != nil {
		return err
	}

	if rep.Status == "STOPPED" {
		return nil
	}

	if rep.Status == "CONFLICT" {
		return session.NewError(session.CodeConflict, "agentd",
			"state home is locked by a V1 foreground owner; stop it directly",
			"exit the V1 asd process before running agentd commands")
	}

	if rep.ActiveSessions > 0 && !force {
		return session.NewError(session.CodeConflict, "agentd",
			fmt.Sprintf("cannot stop daemon: %d active session(s) are running", rep.ActiveSessions),
			"pass --force to terminate running sessions and stop agentd")
	}

	if rep.PID <= 0 {
		return errors.New("daemon PID could not be determined")
	}

	if rep.PID == os.Getpid() {
		// In-process test runner: do not signal or terminate the current process.
		return nil
	}

	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	// Send SIGTERM to the daemon process
	if err := syscall.Kill(rep.PID, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("signal daemon PID %d: %w", rep.PID, err)
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !isProcessAlive(rep.PID) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}

	if force {
		_ = syscall.Kill(rep.PID, syscall.SIGKILL)
		time.Sleep(200 * time.Millisecond)
		if !isProcessAlive(rep.PID) {
			return nil
		}
	}

	return fmt.Errorf("timed out waiting for agentd (PID %d) to terminate after %s", rep.PID, timeout)
}

// EnsureRunning guarantees that agentd is running, performing a single-flight background spawn if absent.
func EnsureRunning(ctx context.Context, paths config.Paths, cfg config.Config) error {
	socketPath, err := ipc.SocketPath(paths)
	if err != nil {
		return err
	}

	client := ipc.NewClient(socketPath)
	probeCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	err = client.Ping(probeCtx)
	cancel()
	if err == nil {
		return nil
	}

	// Check if a V1 owner is holding the lock
	tokenData, readErr := os.ReadFile(paths.LockPath())
	if readErr == nil {
		_, pid, mode := parseLockToken(string(tokenData))
		if mode == "v1-owner" && pid > 0 && isProcessAlive(pid) {
			return session.NewError(session.CodeConflict, "agentd",
				"a legacy V1 foreground owner already holds this state home",
				"stop the running V1 asd process before starting or migrating to agentd")
		}
	}

	// Acquire single-flight bootstrap lock
	bootstrapLockFile := filepath.Join(paths.StateDir, ".daemon_bootstrap.lock")
	lock, err := acquireBootstrapLockWithRetry(ctx, bootstrapLockFile, 5*time.Second)
	if err != nil {
		// Could not acquire lock within timeout; check if daemon became ready while waiting
		checkCtx, checkCancel := context.WithTimeout(ctx, 1*time.Second)
		defer checkCancel()
		if client.Ping(checkCtx) == nil {
			return nil
		}
		return fmt.Errorf("failed to acquire daemon bootstrap lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	// Double-check if daemon became ready while waiting for lock
	checkCtx, checkCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	readyErr := client.Ping(checkCtx)
	checkCancel()
	if readyErr == nil {
		return nil
	}

	// Spawn agentd in background
	agentdPath, err := findAgentdExecutable()
	if err != nil {
		return err
	}

	if err := store.EnsurePrivateDir(paths.StateDir); err != nil {
		return err
	}
	logPath := filepath.Join(paths.StateDir, "agentd.log")
	logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log file: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(agentdPath, "run")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn agentd process: %w", err)
	}

	// Poll socket readiness
	waitDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(waitDeadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}

		pingCtx, pingCancel := context.WithTimeout(ctx, 300*time.Millisecond)
		err := client.Ping(pingCtx)
		pingCancel()
		if err == nil {
			return nil
		}
	}

	return fmt.Errorf("spawned agentd (PID %d) but socket %s did not become ready within 5s; check %s", cmd.Process.Pid, socketPath, logPath)
}

func acquireBootstrapLockWithRetry(ctx context.Context, lockPath string, timeout time.Duration) (*store.Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		lock, err := store.AcquireLock(lockPath)
		if err == nil {
			return lock, nil
		}
		if !errors.Is(err, store.ErrLocked) {
			return nil, err
		}
		if time.Now().After(deadline) {
			return nil, store.ErrLocked
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func findAgentdExecutable() (string, error) {
	if self, err := os.Executable(); err == nil {
		dir := filepath.Dir(self)
		candidate := filepath.Join(dir, "agentd")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if p, err := exec.LookPath("agentd"); err == nil {
		return p, nil
	}
	return "", errors.New("agentd executable not found in PATH or adjacent to asd")
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func parseLockToken(token string) (instanceID string, pid int, mode string) {
	parts := strings.Fields(token)
	if len(parts) > 0 {
		instanceID = parts[0]
	}
	for _, p := range parts[1:] {
		if strings.HasPrefix(p, "pid=") {
			pid, _ = strconv.Atoi(strings.TrimPrefix(p, "pid="))
		} else if strings.HasPrefix(p, "mode=") {
			mode = strings.TrimPrefix(p, "mode=")
		}
	}
	return
}
