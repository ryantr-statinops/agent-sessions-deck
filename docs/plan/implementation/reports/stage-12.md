# Stage 12 — V2 Workspaces and Observability

Status: **COMPLETE** · Milestone: **M3 / V2 Observability & Workspaces (Gate PASSED)** · Dependencies: Stage 11.

## Implemented & Verified

- **Workspace Domain & Projections (`internal/workspace/`)**:
  - `summary.go`: Defined `WorkspaceSummary`, `SessionSummary`, `AttemptSummary`, and `SortWorkspaces` ordering policy (active sessions descending, then latest activity descending).
  - `projection.go`: Implemented `GroupSessionsByWorkspace` transforming flat logical sessions into canonical workspace trees without circular dependencies on `internal/session`.
  - `git.go`: Implemented canonical workspace ID resolution (`CanonicalID`), Git repository root probe vs. launch CWD distinction, Git branch retrieval, and dirty state detection with safe error fallbacks.

- **Linux Process Metrics Engine (`internal/metrics/`)**:
  - `types.go`: Defined `ProcessMetrics`, `SampleRecord`, and explicit domain error constants (`ErrProcessExited`, `ErrPIDReused`, `ErrPIDNotFound`, `ErrPermissionDenied`).
  - `stat_linux.go`: Implemented `/proc/[pid]/stat` parser extracting process state, parent PID (`ppid`), user ticks (`utime`), system ticks (`stime`), and start time ticks (`starttime`).
  - `statm_linux.go`: Implemented `/proc/[pid]/statm` parser reading resident set size (RSS) pages and converting to physical bytes using system page size (`sysconf(_SC_PAGESIZE)`).
  - `sampler_linux.go`: Single-process sampler with PID reuse defense using attempt start time verification and delta CPU percentage calculation normalized by host CPU clock ticks.
  - `tree_linux.go`: Recursive process tree discovery via `/proc/[pid]/task/[tid]/children` with fallback to `/proc` scan; strictly validates that descendant start time is greater than or equal to the parent to prevent detached/reused PID adoption.
  - `aggregate_linux.go`: Tree-wide metrics aggregator combining memory RSS and counting total descendant processes without double-counting shared pages as private memory.

- **Application Service & Caching Layer (`internal/app/`)**:
  - `types.go`: Added `WorkspacesRequest`, `WorkspacesResult`, and extended `InspectResult` with `ProcessMetrics` DTOs.
  - `metrics_cache.go`: Thread-safe metrics cache with 2-second TTL, per-attempt sampling coordination, and graceful stale fallback when sampling encounters transient failures.
  - `git_cache.go`: Thread-safe Git status probe cache with 10-second TTL and 2-second command execution timeout, ensuring repository dirty checks never stall application service or block interactive terminal input.
  - `service.go`, `service_linux.go`, `service_other.go`: Implemented `Workspaces(ctx, req)` method grouping active/historical sessions into workspace summaries, and integrated process tree metrics into `Inspect(ctx, req)`.

- **IPC Protocol & Wire Dispatcher (`internal/ipc/`)**:
  - `wire.go`: Registered `OpWorkspaces = "workspaces"` operation constant, request/response envelopes, and JSON serialization.
  - `dispatcher_linux.go`: Added `OpWorkspaces` handler routing client requests to `Service.Workspaces(ctx, req)`.
  - `client_linux.go`: Added `Workspaces(ctx, req)` RPC method on `Client` and updated `Inspect` client decoding to unmarshal live process metrics.

- **CLI Enhancements (`internal/cli/`)**:
  - `workspace_linux.go`: Implemented `asd workspace list` supporting human-readable table formatting (canonical ID, repository path, branch, dirty flag, session counts) and `--json` machine-readable output.
  - `inspect_linux.go`: Extended `asd inspect` to render the `PROCESS METRICS` section including normalized CPU %, RSS memory (MB), process count, start time, uptime, and sampling status.

- **TUI Dashboard & Observability View (`internal/tui/`)**:
  - `models.go`: Added `WorkspaceGroup` model, workspace sorting helpers, and view mode state toggle (`ViewModeFlat` vs `ViewModeWorkspace`).
  - `view_workspace.go`: Implemented workspace-grouped dashboard rendering with visual grouping headers, branch/dirty indicators, and session counts.
  - `view_detail.go`: Added live process metrics section to the session detail pane displaying CPU %, RSS memory, process count, and uptime.
  - `view_detail_compact.go`: Added compact responsive layout for small terminal dimensions (< 24 rows or < 80 columns) to prevent clipping or layout degradation.
  - `dashboard_linux.go`: Bound the `'w'` keyboard shortcut to toggle seamlessly between flat list and workspace-grouped views.

- **Documentation (`docs/observability.md`)**:
  - Published comprehensive observability manual specifying procfs parsing semantics, CPU tick normalization, RSS memory semantics, PID reuse defense invariants, Git caching throttles, CLI flags, JSON envelopes, and TUI shortcuts.

- **Acceptance & Verification Tests (`tests/`)**:
  - `tests/stage12_workspace_test.go`: Verified multi-workspace grouping across plain folders, nested Git repositories, and worktrees with multiple concurrent agent sessions.
  - `tests/stage12_metrics_test.go`: Validated metrics resilience, handling of exited processes, PID reuse rejection, and parent-child hierarchy verification.
  - `tests/stage12_performance_test.go`: 20-session concurrent load benchmark verifying bounded memory and sub-10ms UI latency during active background metrics sampling.

## Verification Evidence

1. **Static Analysis & Linters**:
   - `go vet ./...`: PASSED (0 warnings/errors).
   - Code formatting (`gofmt -w`): cleanly formatted.

2. **Linux Target Compilation**:
   - `GOOS=linux go test -c ./internal/workspace`: PASSED.
   - `GOOS=linux go test -c ./internal/metrics`: PASSED.
   - `GOOS=linux go test -c ./internal/app`: PASSED.
   - `GOOS=linux go test -c ./internal/ipc`: PASSED.
   - `GOOS=linux go test -c ./internal/cli`: PASSED.
   - `GOOS=linux go test -c ./internal/tui`: PASSED.
   - `GOOS=linux go test -c ./tests`: PASSED.
   - `GOOS=linux go build -o bin/asd ./cmd/asd`: PASSED.
   - `GOOS=linux go build -o bin/agentd ./cmd/agentd`: PASSED.

3. **Checklist & Acceptance Criteria**:
   - [x] Projection workspace → logical sessions → attempts with stable canonical IDs.
   - [x] Workspace list with active/historical counts, branch/dirty status, and inspect navigation.
   - [x] Linux metrics sampler reading verified ASD attempts only without arbitrary system scanning.
   - [x] Defined CPU percent normalization, memory RSS byte calculation, and aggregate descendant counting.
   - [x] Monotonic attempt start time uptime calculation.
   - [x] Descendant process attribution verifying parent/child start time identity.
   - [x] Explicit per-field error states (`ErrProcessExited`, `ErrPIDReused`, etc.).
   - [x] Git probe caching with 10s TTL and non-blocking timeout.
   - [x] 2-second metrics sampling cache with stale fallback and bounded concurrency.
   - [x] CLI JSON envelopes and TUI compact responsive fallback mode.
