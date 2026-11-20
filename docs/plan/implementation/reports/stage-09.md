# Stage 09 — Recovery và Lifecycle Hardening

Status: **COMPLETE** · Wave: W2 · Dependencies: Stage 06, Stage 08.

## Implemented

- **Startup Session Reconciliation (`internal/app/reconcile.go`, `internal/owner/runtime_linux.go`)**:
  - Implemented `ReconcileStartup(ctx)` passive `/proc` inspection pass before serving IPC.
  - Classified stored sessions accurately into Dead (`ReasonProcessGone`), Surviving Orphan (`AttachmentUnavailable` with `orphaned` note), Recycled PID / Stale Identity Mismatch, and Unknown.
  - Wired reconciliation into owner bootstrap sequence.

- **Surviving Orphan Hardening (`internal/app/lifecycle.go`)**:
  - Enforced refusal of `asd open` on surviving orphans with typed `SESSION_IO_FAILED` code and descriptive operator remediation.
  - Blocked automatic `asd restart` on surviving orphans to prevent abandoning live processes without explicit operator resolution.
  - Upheld strict invariant of no automatic kill or adopt of external unowned processes on startup.

- **Instance-Fenced Socket & Lock Management (`internal/store/lock.go`, `internal/ipc/owner_linux.go`)**:
  - Embedded unique `InstanceID` token in the owner lock file.
  - Implemented stale socket probing with automated cleanup on unheld/stale locks.
  - Enforced instance-fenced token verification during socket release and cleanup, ensuring a shutting-down previous owner never unlinks a newly started owner's active socket.

- **Bounded Graceful Shutdown & Signal Trapping (`internal/owner/shutdown_linux.go`, `internal/owner/signals_linux.go`)**:
  - Implemented bounded graceful shutdown delivering `SIGTERM` to active children with timeout enforcement.
  - Handled uncooperative children with truthful residual process reporting (`TimedOut: true`, `Exited: false`) without silent `SIGKILL` escalation.
  - Wired signal traps for `SIGTERM` and `SIGHUP` executing structured owner shutdown and state persistence before exit.

- **Per-Session Mutation Serialization & Idempotency (`internal/app/service.go`, `internal/app/lifecycle.go`)**:
  - Introduced granular per-session mutation locking, allowing concurrent operations on distinct sessions while serializing mutations on the same session.
  - Ensured store commit and event publication proceed under unified revision ordering, guaranteeing strict revision monotonicity.
  - Deduplicated repeated and in-flight `Restart` requests across IPC retries, returning existing attempts without double-spawning.
  - Fenced late probe and exit callbacks for superseded generations ($G < G_{current}$) with `CodeStaleAttempt`.
  - Guaranteed clean transaction rollback on client disconnect prior to store commit, and clean publish completion if committed.

- **Subscriber Buffer Policy & Synthetic Resync (`internal/events/publisher.go`, `internal/events/subscription.go`, `internal/tui/model.go`)**:
  - Bounded subscription buffer capacity (default 256).
  - Implemented drop-oldest policy on subscriber buffer overflow to prevent slow consumers from blocking owner mutations.
  - Emitted synthetic `resync.required` events upon overflow to notify consumers of stream gaps.
  - Handled `ResyncMsg` and `TypeResyncRequired` in TUI by automatically reloading the session table from the authoritative owner state.

- **Fault Injection Test Suite (`tests/recovery/`)**:
  - `owner_crash_reboot_test.go`: End-to-end integration test simulating owner crash, reboot on identical state directory, and verification of orphan classification and open/restart refusals.
  - `pid_reuse_test.go`: External sentinel process test proving ASD detects recycled PIDs and never signals foreign processes.
  - `uncooperative_child_test.go`: Integration test verifying truthful reporting on SIGTERM timeout and requiring explicit SIGKILL.
  - `store_fault_test.go`: Tests for `CORRUPT_STATE` refusal on corrupted state files without file resets, and write latching on read-only stores while reads stay available.

- **Diagnostic Sanitization (`internal/session/sanitize.go`)**:
  - Implemented secret redactor filtering Bearer tokens, API keys (`sk-...`, `ghp_...`), and sensitive environment variables from error details, hints, and JSON envelopes.

- **Documentation (`docs/recovery.md`, `docs/troubleshooting.md`)**:
  - Comprehensive recovery architecture and operator troubleshooting guide.

## Verification

- `make check`:
  - `fmt-check`: OK.
  - `lint`: `go vet -tags=integration` OK.
  - `test`: All unit tests pass across all packages (100% pass).
  - `test-integration`: All integration tests pass including recovery fault matrix.
  - `smoke`: Build and CLI smoke test OK.
- `make test-race`:
  - All concurrency, lifecycle, and recovery tests passed under Go race detector without data races.

## Completion Boundary

Stage 09 acceptance checks and repository gates passed. All deliverables completed.
