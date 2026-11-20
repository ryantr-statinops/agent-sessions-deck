# Recovery and Lifecycle Hardening

This document describes the failure recovery model, orphan lifecycle, and shutdown semantics implemented in `agent-sessions-deck` (`asd`).

## 1. Startup Reconciliation

When the `asd` owner bootstraps, before serving incoming IPC connections, it runs a startup reconciliation pass over all stored sessions:

1. **Passive Liveness Observation**:
   - ASD inspects `/proc` for recorded process identities (`PID`, `PGID`, `BootID`, `StartTicks`).
   - Passive inspection sends no signals and does not assume prior runtime ownership.
2. **Classification**:
   - **Dead**: The process entry does not exist in `/proc`. The session is transitioned to `LifecycleExited` with `ReasonProcessGone`.
   - **Surviving Orphan**: The recorded process identity is alive in `/proc` with matching boot ID and start ticks, but its controlling terminal PTY was lost with the crashed owner. The session attempt is transitioned to `AttachmentUnavailable` with diagnostic note `orphaned`.
   - **Stale / Recycled PID**: The PID exists but start ticks or boot ID mismatch. ASD treats the process as `ProbeGone` and refuses to signal foreign PIDs.
   - **Unknown**: Inconclusive probe (e.g. permission denied on `/proc`). ASD records `ReasonProbeInconclusive`.

## 2. Surviving Orphan Handling

A surviving orphan process cannot be safely reattached because its PTY master file descriptor closed when the previous owner crashed:

- **Refuse Open**: Attempting `asd open` on an orphaned session is rejected with `SESSION_IO_FAILED`:
  > `no PTY handle for attempt N: the process survived an owner crash but lost its controlling terminal (hint: inspect, stop, kill, or restart the session; asd cannot reattach a lost PTY without a daemon)`
- **Block Automatic Restart**: Attempting `asd restart` on an orphan is refused with `CONFLICT` to prevent abandoning an unowned live process:
  > `the session has a surviving process but no terminal, so a restart would leave it unowned (hint: resolve the surviving process first: inspect it, then stop or kill it explicitly)`
- **No Automatic Adopt or Kill**: ASD never silently kills or adopts external processes on startup.

## 3. Instance-Fenced IPC Locks and Socket Cleanup

To prevent race conditions between a shutting-down previous owner and a bootstrapping new owner:

- The lock file (`owner.lock`) contains an **Instance ID** token in addition to the PID:
  ```json
  {"pid": 12345, "instance_id": "inst-6789abc"}
  ```
- **Stale Socket Probing**: If an existing socket file is found, the owner attempts a Unix domain connection. If the connection fails with `ECONNREFUSED` and the lock file is unheld or stale, the socket is safely cleaned up.
- **Instance Fencing**: When an owner exits or cleans up its socket, it checks that the lock file still contains its exact `InstanceID`. If a new owner has already claimed the socket and updated the lock token, the exiting old owner **never unlinks** the new owner's socket file.

## 4. Bounded Graceful Shutdown

When an owner process terminates:

- Active sessions receive `SIGTERM` to allow graceful child cleanup.
- Shutdown waits up to the configured grace window (e.g. 5s).
- **No Silent SIGKILL Escalation**: If a child process traps and ignores `SIGTERM`, ASD truthfully reports residual processes (`Exited: false`, `TimedOut: true`) in the shutdown report. It never silently escalates to `SIGKILL` without explicit user confirmation.
- Signal traps catch `SIGTERM` and `SIGHUP` to execute this bounded shutdown sequence cleanly before exit.

## 5. Mutation Serialization and Idempotency

- Mutations are serialized per session reference via granular session locks, ensuring concurrent operations on different sessions proceed without contention.
- Store commit and event publication are locked together to preserve strict revision monotonicity ($R_{event} == R_{commit}$).
- Duplicate or retried `Restart` calls across IPC retries return the active attempt without double-spawning.
- Late exit or probe callbacks from older generations ($G < G_{current}$) are fenced and rejected with `STALE_ATTEMPT`, keeping running attempts untouched.

## 6. Subscriber Buffer Policy and Resync

- Event subscribers use a bounded buffer (default 256 events).
- On buffer overflow caused by slow consumers, a drop-oldest policy discards the oldest notifications to avoid blocking publisher mutations.
- The subscription delivers a synthetic `resync.required` event indicating the missed revision, prompting the consumer (such as the TUI) to reload its session table from the authoritative owner state.

## 7. Diagnostic Sanitization

Diagnostic error strings, JSON envelopes, and panic recovery traces are passed through a secret redactor that strips:
- HTTP Bearer tokens (`Bearer [REDACTED]`)
- API keys (`sk-...`, `ghp_...`, `gho_...`) -> `[REDACTED]`
- Sensitive environment variable assignments (`*_TOKEN=[REDACTED]`, `*_KEY=[REDACTED]`, `*_SECRET=[REDACTED]`)
