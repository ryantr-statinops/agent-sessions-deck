# ADR 0003 — State layout (XDG), single-writer store, and concurrency

Status: **Accepted for Stage 03 implementation** (coordinator resolution of the Stage 00 proposal; Product paths unchanged).

## Context

When ADR 0003 was drafted, PRODUCT.md §20 placed config and state files under `~/.config/asd/`, mixing configuration and mutable state.
PRODUCT.md §20 now uses the XDG split; this ADR records the Stage 03 resolution for schema, migration, and transaction boundaries.
Concurrent CLI/TUI writers to plain JSON risk lost updates and half-written files after a crash.
This decision keeps the current XDG paths and gives the session store a single atomic aggregate.

## Decision (resolved for Stage 03)

### Layout

| Kind | Path | Notes |
|---|---|---|
| Config | `$XDG_CONFIG_HOME/asd/config.yaml` (default `~/.config/asd/config.yaml`) | User-edited; strict parse, unknown keys rejected (Stage 03). |
| Persistent state | `$XDG_STATE_HOME/asd/` (default `~/.local/state/asd/`), files `sessions.json` and `state.json` | Each file has an independent `schema_version` + `revision` envelope. `sessions.json` owns the full session/attempt snapshot; `state.json` owns `recent_workspaces`. |
| Runtime | `$XDG_RUNTIME_DIR/asd/control.sock` | Owner control socket; same-user access only; private fallback with documented cleanup when `XDG_RUNTIME_DIR` is absent (Stage 06 decides the fallback path). |
| Lock | `<state-dir>/.lock` held by the owner for its whole lifetime | Short-term acquisition allowed for the offline mutations the CLI contract permits. |

### Legacy migration
PRODUCT.md §20 already reflects the XDG path split.
If `~/.config/asd/sessions.json` exists and the XDG destination is absent, strictly decode the Stage 02 session array, keep a same-directory backup, and atomically write schema version 1.
If a valid XDG destination exists, it is authoritative; never merge or overwrite it from the legacy source.
If data is corrupt or unsupported, preserve it and fail with recovery guidance; never delete the legacy source silently.

### Single writer and revisions

- While an owner is live, it is the only store writer. Clients mutate via IPC;
  the owner serializes mutations per session (one lifecycle transaction at a
  time) plus store-level transactions for cross-session work.
- Each file has an independent monotonic `revision`. `sessions.json` uses the revision returned by Stage 02 `Store.Load`/`Commit`; `state.json` revision applies only to recent-workspace changes.
- Retryable lifecycle mutations carry a request ID, so a retry cannot double-apply.
  A duplicate request ID returns the first result.
- The sessions store is one aggregate `sessions.json` envelope: `schema_version`, `revision`, and the complete session/attempt array from Stage 02 `EncodeSessions`. `Store.Commit` updates this snapshot and its revision in one atomic file replacement; no two-file journal is needed for this transaction.
- `state.json` holds recent-workspace state with its own `schema_version` and revision. No Stage 03 operation spans both files; any future cross-file atomic mutation requires a new design decision before implementation.
- Clocks are UTC; persisted snapshots carry `observed_at` and an authority
  marker (`live` vs. `stored`) so offline readers cannot mistake stored
  `running` for liveness (see CLI contract).

### Durability

- Writes go to a temp file in the same directory, flush + fsync, atomic
  rename, directory sync where applicable. Disk-full or write failure keeps
  the previous version intact and reports a recovery instruction — never a
  truncated file presented as current.
- Corrupt/truncated JSON is detected, preserved as-is for diagnosis, and
  reported with recovery instructions. The runtime never auto-resets state to
  empty. Unknown future `schema_version` fails safe with guidance.
- New state/config files are created `0600`, directories `0700`. Existing
  user files are never re-chmodded destructively without notice.

### Concurrency and process rules

- Exactly one waiter per child (`Wait` called once); exit callbacks check the
  Attempt generation so a stale callback cannot overwrite a newer Attempt.
- Generation fencing on IPC callbacks: late events/timeouts from an old
  generation never mutate the current Attempt.
- Slow event subscribers never hold lifecycle locks; overflow uses a
  documented drop/coalesce + revision-resync policy (Stage 09).
- No raw terminal bytes and no environment secrets are persisted. `argv` may
  contain secrets, so command-line logging is off by default and display
  paths support redaction (Stage 03 documents exact usage).

### What is deferred

- SQLite: not in V1. JSON stays until event/session-history volume forces a
  measured decision at Stage 13. No `pkg/api` public surface is created early
  (plan README decision 7).

## Alternatives considered

- **Keep everything in `~/.config/asd/`.** Rejected: mixes config with
  mutable state, breaks backup/sync expectations for config dirs, and has no
  home for the runtime socket.
- **SQLite from the start.** Rejected: V1 state is small and local; an event
  database is justified only by measured history/scale needs (Stage 13 gate).
- **Multi-writer JSON with merge.** Rejected: session/attempt/revision
  triples cannot merge safely; single-writer + IPC serialization is simpler
  and testable.
- **Persisting PTY transcripts by default.** Rejected: unbounded growth and
  secret retention; transcripts, if ever added, need explicit scope, bounds,
  and redaction (post-V1 decision).

## Assumptions and evidence

- Assumptions: Linux file semantics (atomic rename, fsync, `flock`-style
  locking); single user per state home; JSON volume stays small through V1.
- Evidence at Stage 00: none executed by this slice. Falsifiable hooks for
  Stage 03/09: atomic-write fault tests (pre-or-post image, never half JSON),
  concurrent-writer lock refusal, migration round-trip preserving
  ID/name/exit metadata, permission tests in an isolated temp home (never the
  real `$HOME`), and client-direct-write refusal while an owner holds the
  lock.

## Maintainer decisions and resolution status

1. **License — MIT selected by the maintainer.** The root `LICENSE` contains the canonical notice. Preserve it in source distributions and release artifacts; changing the license requires a new maintainer decision.
2. **Go module identity — resolved for this repository in Stage 01.** `go.mod` uses `github.com/ryantr-statinops/agent-sessions-deck`, matching verified `origin`. If renamed or transferred, update `go.mod`, `go.sum`, `vendor/` and import paths together.
3. Exact private fallback runtime path when `XDG_RUNTIME_DIR` is unset
   (Stage 06).
4. **Resolved for Stage 03:** `sessions.json` is the atomic aggregate session store; `state.json` stores recent-workspace metadata with an independent revision. Session, Attempt and session revision never span files.
