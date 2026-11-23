# Stage 11 — V2 Daemon và Client Migration

Status: **COMPLETE** · Milestone: **M3 / V2 Daemon Cutover (Gate PASSED)** · Dependencies: Stage 10.

## Implemented & Verified

- **Architecture Decision Record (`docs/architecture/adr/0004-v2-daemon-ownership.md`)**:
  - Formalized sole ownership transition from V1 foreground owner to background `agentd` daemon.
  - Specified process lifetime guarantees: sessions, PTY masters, and virtual terminal state survive client disconnects.
  - Defined explicit CLI interface (`run`, `start`, `stop`, `status`), single-flight bootstrap locking, and crash honesty invariants.

- **Daemon Core Package (`internal/daemon/daemon_linux.go`, `internal/daemon/daemon_linux_test.go`)**:
  - Implemented `Daemon` supervisor wrapping `owner.Runtime` with daemon identity and mode tagging.
  - Non-interactive status probing (`daemon.Status`): returns health (`RUNNING`, `STOPPED`, `CONFLICT`), instance ID, PID, socket path, uptime, and active/total session counts.
  - Controlled graceful shutdown (`daemon.Stop`): enforces active-session protection by rejecting termination without `--force`.
  - Single-flight bootstrap lock (`EnsureRunning`): uses `.daemon_bootstrap.lock` to prevent concurrent `asd` invocations from launching duplicate daemons.
  - Comprehensive unit test suite covering lifecycle, status reporting, conflict detection against V1 owners, and stop protections.

- **Daemon Binary Entry Point (`cmd/agentd/main.go`, `cmd/agentd/root.go`, `cmd/agentd/root_test.go`)**:
  - `agentd run` (or `--foreground`): foreground execution with structured signal trapping (`SIGINT`, `SIGTERM`, `SIGHUP`) and clean session draining.
  - `agentd start`: on-demand background daemon startup.
  - `agentd stop`: graceful daemon shutdown with `--force` and `--timeout` controls.
  - `agentd status`: human-readable and `--json` machine-readable diagnostic output.
  - Root test coverage for `--help`, `--version`, and status reporting.

- **Client Transition & Controlled Auto-Start (`internal/cli/tui_linux.go`, `internal/cli/root_linux.go`)**:
  - `asd` CLI commands (`new`, `open`, `restart`) and interactive TUI now act as pure clients to `agentd`.
  - Automated transparent startup: if `agentd` is absent, interactive `asd` auto-starts `agentd` in the background and attaches to the resulting control socket.
  - TUI detach on quit: closing the TUI dashboard simply detaches without prompting "Stop all and quit" because sessions are preserved in the daemon.

- **V1/V2 Migration Hardening (`internal/ipc/owner_linux.go`, `internal/owner/runtime_linux.go`)**:
  - Extended lock tokens with `mode=v1-owner` vs `mode=daemon` tagging.
  - Enforced mutual exclusion: attempting to run `agentd` while a legacy V1 foreground owner holds the lock yields an explicit refusal (`ErrOwnerLocked` with operator guidance to stop the V1 process first).
  - Maintained full backward compatibility with V1 persisted session schema (`sessions.json`).

- **Integration Acceptance Suite (`tests/stage11_daemon_test.go`)**:
  - `TestStage11_SessionPersistenceAcrossClientDisconnect`: proves that launching multiple sessions, attaching via interactive client, and disconnecting leaves both sessions active and running in `agentd`, and reopening returns the exact attempt/generation and screen state.
  - `TestStage11_ConcurrentDaemonStartsOnlyOneOwner`: verifies flock mutual exclusion preventing duplicate daemon instances on the same state directory.
  - `TestStage11_SocketAndDirectoryPermissions`: verifies `0700` parent directory and `0600` socket permissions.

- **Packaging and Documentation (`docs/daemon.md`, `docs/migration-v2.md`, `Makefile`, `scripts/package-release.sh`)**:
  - `docs/daemon.md`: detailed operations manual and systemd user service (`agentd.service`) configuration guide.
  - `docs/migration-v2.md`: comprehensive step-by-step V1 to V2 migration guide and troubleshooting.
  - Build automation: Makefile builds both `bin/asd` and `bin/agentd`; `package-release.sh` packages both binaries for `linux/amd64` and `linux/arm64`.

## Verification Evidence

1. **Static Analysis & Linters**:
   - `go vet ./...`: OK (0 warnings/errors).
   - `gofmt -w`: cleanly applied.
2. **Linux Target Verification**:
   - `GOOS=linux go test -c ./internal/daemon`: OK.
   - `GOOS=linux go test -c ./cmd/agentd`: OK.
   - `GOOS=linux go test -c ./tests`: OK.
   - `GOOS=linux go build -o bin/asd ./cmd/asd`: OK.
   - `GOOS=linux go build -o bin/agentd ./cmd/agentd`: OK.
3. **Artifact Integrity**:
   - Release packager bundles both `asd` and `agentd` into distribution tarballs.
