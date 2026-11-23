# ADR 0004 — V2 Daemon Ownership, Lifecycle, and Migration

Status: **Accepted** (Milestone M3 / Stage 11).
Author: ASD Architecture & Core Team.
Supersedes / Extends: ADR 0001 (V1 foreground owner).

## Context

In V1 (Milestone M2), ASD implemented a single foreground owner architecture (ADR 0001). Under V1, the first interactive `asd` invocation acquired the state-home lock, hosted the `SessionManager`, managed PTY handles, and served a local Unix control socket. When the foreground owner terminated, all active child sessions had to be stopped or marked as orphaned/io_failed because the owner held their controlling PTY file descriptors.

Milestone M3 (Stage 11) introduces `agentd`, a local background daemon that permanently assumes sole ownership of the state home, process supervision, PTY handles, and IPC control socket. The CLI and TUI (`asd`) become pure clients. Closing a client (detaching) must never terminate active coding agent sessions.

## Decision

1. **Sole Ownership by `agentd` in V2**:
   - `agentd` replaces the V1 foreground owner as the sole writer and runtime supervisor for a given state home (`$XDG_STATE_HOME/asd`).
   - `agentd` acquires the state-home lock file (`.lock`) upon startup and holds it for the duration of its lifecycle.
   - If a V1 foreground owner is currently holding the lock, `agentd` refuses to start and fails with `CodeOwnerUnavailable` ("a V1 owner is already running; stop it before starting agentd").
   - Conversely, a V1 foreground owner will fail to start if `agentd` holds the state-home lock.

2. **Process Lifetime & Detach Guarantees**:
   - Agent processes and their PTYs belong to `agentd`.
   - When an interactive client (`asd` TUI or `asd open`) detaches or terminates unexpectedly, only the client's IPC connection and interactive input lease are closed.
   - `agentd` continues to drain the child PTY, maintain terminal emulator screen state, append to the bounded scrollback ring buffer (10,000 lines), and monitor process lifecycle.
   - Client reconnect (`asd open` or reopening the TUI dashboard) fetches the current screen state and monotonic revision counter without interrupting the running agent.

3. **Daemon Control Interface (`cmd/agentd`)**:
   `agentd` provides an explicit, minimal CLI interface:
   - `agentd run` (or `agentd --foreground`): runs in the foreground, logging to stdout/stderr. Ideal for systemd user services, containers, or terminal debugging.
   - `agentd start`: ensures a daemon instance is running in the background for the current user/state home. Uses a single-flight startup lock to prevent duplicate daemon spawning from concurrent calls.
   - `agentd stop [--force] [--timeout <duration>]`: gracefully signals the daemon to shut down. If active sessions exist and `--force` is not passed, it rejects termination to protect ongoing work.
   - `agentd status [--json]`: queries daemon health, PID, socket path, instance ID, uptime, and active session count without requiring terminal interaction or PTY allocation.

4. **Client Bootstrap & Controlled Auto-Start**:
   - When `asd` is invoked:
     - It checks for an active control socket.
     - If the daemon is absent and `asd` is run interactively (or with auto-start enabled), `asd` initiates a controlled auto-start of `agentd`, acquiring a temporary bootstrap lock to prevent duplicate concurrent daemon launches, waiting up to 5 seconds for socket readiness.
     - If auto-start is disabled or fails, `asd` prints clear instructions directing the user to run `agentd start` or start the systemd user service.

5. **Migration and V1 Compatibility**:
   - **No Live FD Transfer**: Live PTY file descriptors cannot be safely passed between detached processes without complex, platform-fragile SCM_RIGHTS FD-passing harnesses. Therefore, users must stop or complete running V1 sessions before switching to `agentd`.
   - **Persistent State Compatibility**: The persistent store format (`sessions.json`) remains backward compatible. When `agentd` boots up on a V1 state directory, it runs startup reconciliation:
     - Any process whose PID/PGID or starttime does not match or whose parent is dead is classified as `SESSION_IO_FAILED` / `EXITED` / `ORPHANED`.
     - No unverified foreign processes are killed or adopted.

6. **Security & Permissions**:
   - Communication uses local Unix stream sockets only (`0600` permissions), located in `$XDG_RUNTIME_DIR/asd/` (fallback `/tmp/asd-<uid>/` with `0700` parent directory).
   - Peer UID verification (`SO_PEERCRED`) ensures only the same user can interact with the daemon.
   - TCP listeners are strictly forbidden by default.
   - Secrets in session arguments or environment variables are never persisted in plaintext and are masked (`[REDACTED]`) in status output.

7. **Crash and Recovery Honesty**:
   - If `agentd` crashes or receives an uncatchable `SIGKILL`, PTY descriptors are lost.
   - Upon restart, `agentd` performs honest classification: surviving child processes whose PTYs were lost are classified as orphaned/IO-failed; `agentd` never synthesizes phantom screens or attempts auto-reconnection to dead PTYs.

## Consequences & Verification

- Users can close and reopen their terminal / TUI at will without killing active coding agents.
- The daemon can be managed directly via CLI (`agentd start|stop|status`) or as an unprivileged systemd user service (`systemd --user`).
- Clean separation between presentation layer (`asd`) and background daemon (`agentd`).
