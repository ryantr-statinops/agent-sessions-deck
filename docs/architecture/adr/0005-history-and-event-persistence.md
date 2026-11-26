# ADR 0005 — History and Event Persistence Architecture

- **Status:** Accepted (Stage 13)
- **Date:** 2026-11-26
- **Context:** Milestone M3 (V2 Control Plane), Stage 13 (V2 history and event persistence)
- **Supersedes / Extends:** ADR 0003 (State layout and single-writer store), ADR 0004 (V2 daemon ownership)

---

## 1. Context and Problem Statement

In V1 (Milestone M2), ASD implemented an atomic snapshot single-writer persistence model (ADR 0003). All session records were serialized into a single `sessions.json` document under `$XDG_STATE_HOME/asd/sessions.json`. While each session entity contained private slices of attempt history, there was no separate, queryable, durable event log. The event bus (`internal/events.Publisher`) was an in-memory fan-out channel whose sequence numbers (`Revision`) reset to 0 upon daemon or owner restart.

V2 introduces long-running session supervision via `agentd` (ADR 0004), workspace aggregations, and process observability (Stage 12). For Stage 13, users and automated workflows require:
1. **Durable Session Attempt Histories:** Tracking each restart attempt with its command provenance, exact start/running/ended timestamps, duration, exit code, and terminal exit reason.
2. **Append-Only Event Journaling:** Persistent event stream with monotonically increasing revisions surviving restarts, supporting bounded cursor-based queries and subscriber resync.
3. **Retention & Pruning:** Configurable retention caps (event count, attempt count, age TTLs) that strictly protect active sessions from eviction.
4. **Zero Terminal Secrets Guarantee:** Strict metadata-only persistence: never collecting raw terminal output (`stdout`/`stderr`), prompts, or credentials.

The implementation checklist for Stage 13 specifically requires:
> "Đo nhu cầu history/events và chọn versioned JSON/event journal hoặc SQLite; ADR nêu atomicity, indexing, migration, dependency/CGO/cross-build tradeoff. Không bắt buộc SQLite chỉ vì V2."

This ADR evaluates the storage architecture choices and records the decision.

---

## 2. Considered Alternatives

### Alternative A: SQLite (`sessions.db`)
Using an embedded relational database (SQLite) for storing sessions, attempts, and event records.

* **Pros:**
  * Rich SQL query capabilities (arbitrary filtering, joins, indexes, aggregate queries).
  * ACID transactions across multiple tables natively managed by the SQLite engine.
* **Cons:**
  * **CGO & Cross-Compilation Friction:** The standard Go driver (`github.com/mattn/go-sqlite3`) requires CGO (`CGO_ENABLED=1`) and a platform C compiler. This breaks ASD's zero-CGO build policy, complicates Linux ARM64 cross-compilation in CI, and increases binary distribution complexity.
  * **Pure-Go SQLite Overhead:** Pure Go transpiled alternatives (such as `modernc.org/sqlite`) avoid CGO but add ~10MB of transpiled C code to the binary, introduce measurable startup latency, and increase vendor tree footprint significantly.
  * **Complexity Overhead:** For ASD's append-only event stream and session snapshot read model, full SQL relational machinery is disproportionate to the actual access patterns.

### Alternative B: Append-Only JSONL Event Journal (`events.jsonl`) + Versioned Snapshot Index (`sessions.json`) [CHOSEN]
Retaining the proven atomic snapshot model for session state (`sessions.json`, ADR 0003) while introducing a dedicated append-only JSON Lines journal (`events.jsonl`) for persistent events.

* **Pros:**
  * **Zero CGO & Seamless Cross-Compilation:** Pure Go standard library (`os`, `bufio`, `encoding/json`), maintaining 100% offline vendoring and instant cross-compilation for `linux/amd64` and `linux/arm64`.
  * **Optimal Append Performance:** Events are written sequentially (`O_APPEND | O_SYNC` or buffered writes under the held state lock), achieving sub-millisecond persistence.
  * **Bounded Memory & Cursor Streaming:** Bounded cursor queries scan backwards from the file tail or forwards from a given revision without loading the entire event history into RAM.
  * **Natural Resilience & Safe Pruning:** Appending does not risk corrupting prior records. Compaction/pruning is performed via copy-on-write to a temporary file followed by an atomic rename.
  * **Schema Evolution & Backward Compatibility:** V1 `sessions.json` requires no disruptive migration. Each event in `events.jsonl` is self-describing JSON with a versioned schema.
* **Cons:**
  * Complex multi-dimensional ad-hoc queries require streaming through filtered records rather than using B-tree SQL indexes (mitigated by bounded query limits and indexed in-memory session summaries).

---

## 3. Decision

We choose **Alternative B: Append-Only JSONL Event Journal (`events.jsonl`) combined with Atomic Snapshot Index (`sessions.json`)**.

### Storage Layout
Under `$XDG_STATE_HOME/asd/`:
* `sessions.json`: Atomic snapshot containing active sessions, logical metadata, and recent attempt histories.
* `events.jsonl`: Append-only, newline-delimited JSON stream of published lifecycle events.
* `.lock`: File-locking primitive (`flock`) ensuring single-writer mutual exclusion between daemon and CLI clients.

### Monotonic Event Revisions
1. The event journal assigns a strictly monotonic `uint64` revision starting at 1.
2. When `agentd` initializes, it scans the tail of `events.jsonl` to discover `LastRevision`. The next published event continues from `LastRevision + 1`, guaranteeing unbroken revision ordering across daemon restarts.

### Query and Cursor Semantics
* `app.History()` queries session snapshots and full attempt arrays, supporting filters (`AgentID`, `WorkspaceID`, `Status`, `Since`, `Until`) with `Limit` and `Offset` pagination.
* `app.Events()` queries recent events from `events.jsonl`, supporting cursor filtering (`AfterRevision`, `SessionID`, `Type`, `Limit`).
* Result sizes are capped (default 50, maximum 1000) to ensure predictable memory usage even with 100,000+ stored events.

### Retention and Safe Pruning Rules
1. **Active Record Protection:** Pruning **never** evicts a session whose lifecycle is not terminal (`Running`, `Created`, `Stopping`), and never deletes the active attempt of any session.
2. **Atomic Pruning:** Pruning `events.jsonl` streams retained records into `.events.jsonl.tmp` and replaces the file via `atomic rename` while holding the store write lock.

### Zero Secrets Policy
1. No raw terminal bytes, PTY stream chunks, or emulator buffer dumps are ever written to `events.jsonl` or `sessions.json`.
2. Process command lines are stored as immutable `argv` arrays without shell expansion or environment dumps.
3. Event payloads contain strictly typed metadata (session ID, attempt generation, timestamp, duration, exit code, exit reason).

---

## 4. Consequences

### Positive
* 100% Zero-CGO retained; cross-compiles cleanly on Linux amd64 and arm64.
* Fast, crash-resilient append-only storage.
* Unbroken event revision sequence across restarts, enabling reliable reconnect resync.
* Active sessions and ongoing workflows are guaranteed immune to retention eviction.

### Negative / Tradeoffs
* Filtered queries over very large histories require linear scanning of `events.jsonl` (bounded by query limits and retention caps).
