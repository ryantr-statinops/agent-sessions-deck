# ADR 0003 — State layout (XDG), single-writer store, and concurrency

Status: **Proposed** (Stage 00 contract slice; starting point from the plan
README decision 8, not an approved product fact).

## Context

PRODUCT.md §20 places config and both state files under `~/.config/asd/`
(`config.yaml`, `sessions.json`, `state.json`), which mixes configuration,
mutable state, and runtime artifacts in one directory and contradicts the XDG
Base Directory split the plan README proposes. Concurrent CLI/TUI writers to
plain JSON also risk lost updates and half-written files after a crash. This
ADR fixes paths, write discipline, and concurrency so Stage 03 can implement
against a frozen contract.

## Decision (proposed)

### Layout

| Kind | Path | Notes |
|---|---|---|
| Config | `$XDG_CONFIG_HOME/asd/config.yaml` (default `~/.config/asd/config.yaml`) | User-edited; strict parse, unknown keys rejected (Stage 03). |
| Persistent state | `$XDG_STATE_HOME/asd/` (default `~/.local/state/asd/`), files `sessions.json`, `state.json` with a `schema_version` + `revision` envelope | Owner-written only while an owner is live. |
| Runtime | `$XDG_RUNTIME_DIR/asd/control.sock` | Owner control socket; same-user access only; private fallback with documented cleanup when `XDG_RUNTIME_DIR` is absent (Stage 06 decides the fallback path). |
| Lock | `<state-dir>/.lock` held by the owner for its whole lifetime | Short-term acquisition allowed for the offline mutations the CLI contract permits. |

`[PRODUCT-DELTA]` This moves `sessions.json`/`state.json` out of
`~/.config/asd/` into the XDG state directory. PRODUCT.md §20 must be updated
to this table (minimal patch in this slice), and Stage 03 ships a migration:
if a legacy `~/.config/asd/sessions.json` exists, import it once under
documented rules, keep a backup, and never delete the source silently.

### Single writer and revisions

- While an owner is live, it is the only store writer. Clients mutate via IPC;
  the owner serializes mutations per session (one lifecycle transaction at a
  time) plus store-level transactions for cross-session work.
- Every mutation bumps a `revision` (store-wide) and carries a request ID when
  the operation is retryable, so a retried IPC request cannot double-apply
  (duplicate request ID → return the first result).
- One transaction updates logical Session + Attempt + revision together, so no
  observable state spans two files half-written. If the two-file layout cannot
  be made atomic, Stage 03 must use an aggregate file or a journal/commit
  marker — decided at implementation time, recorded in the Stage 03 handoff.
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
4. Whether the two-file (`sessions.json` + `state.json`) layout survives or
   collapses into an aggregate/journal design (Stage 03, with a recorded
   rationale either way).
