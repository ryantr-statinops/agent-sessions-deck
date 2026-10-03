# ASD Glossary (Stage 02 domain contract)

Status: **Frozen for Stage 02** · Scope: shared domain/application vocabulary. Later stages supply runtime, persistence and CLI implementations. Terms diverging from PRODUCT.md remain marked `[PRODUCT-DELTA]` with an ADR pointer.

## Agent

A coding-agent implementation ASD can launch (for example `claude`, `codex`,
`opencode`, `omp`, `orca`, `aider`, or a user-configured command). An Agent is
a launchable command definition, never a running instance.

## Logical Session

A single user-visible unit of work: one selected agent plus one workspace plus
a stable identity across restarts. A logical Session outlives any single OS
process: `restart` keeps the Session ID and creates a new execution Attempt.
`[PRODUCT-DELTA]` PRODUCT.md §5 defines "Session" twice (running instance vs.
started command); this glossary keeps exactly one meaning — the logical
Session — and moves run-specific facts to Attempt and ProcessIdentity.

## Execution Attempt (Attempt)

One execution of a logical Session's resolved command in its workspace. Each
Attempt has a monotonically increasing generation number scoped to its Session,
an immutable resolved command (`argv` array, never a shell string), timestamps,
an exit reason, and the observed ProcessIdentity. Concurrent Attempts of the
same Session are forbidden: a new Attempt starts only after the previous one
has a recorded terminal observation (exited/failed) or an explicit
stop/kill has been verified (see `session-state-machine.md`).

## ProcessIdentity

The evidence that a live OS process is the one ASD started for a given
Attempt. Fields: PID, process group ID (PGID), process start time (from
`/proc/PID/stat` starttime, never PID alone), boot ID, and the owner instance
ID that spawned it. A signal (stop/kill) or liveness claim is valid only when
the observed identity matches the recorded one; otherwise the runtime must
fail closed (refuse to signal, mark observation `unknown` or stale — never
signal a recycled PID). `[PRODUCT-DELTA]` PRODUCT.md §10 stores only `PID`;
PID alone is explicitly insufficient per plan README decision 5 and Stage 05/09
identity rules.

## Workspace

The directory in which an agent operates. Sources, in priority order: explicit
CLI path, current working directory, configured workspaces, recent-workspace
history. ASD never scans the whole disk for workspaces.

## Repository

Git metadata associated with a Workspace (root, branch, dirty state,
changed-file count), obtained by invoking the Git CLI. Metadata only; not a
replacement for Git.

## PTY handle

The in-memory pseudo-terminal owned by the runtime for one Attempt's
interactive input/output. A PTY handle is never serialized to disk and never
survives its owner process: after an owner crash the PTY is gone even if the
child process survived (that state is called Orphaned, see
`session-state-machine.md`). "Reconstruct state" after a crash therefore means
recovering metadata plus liveness classification only.

## Owner (foreground owner, V1)

The single live `asd` process that holds the state-home lock, the
SessionManager, all PTY handles, and the application service. Exactly one
owner exists per ASD state home. Every other `asd` invocation is a client.
See `adr/0001-v1-runtime-ownership.md`.

## Client

Any `asd` CLI/TUI invocation that is not the owner. Clients reach the owner
over the local Unix control socket; they never write the state store directly
while an owner is live. Closing a client detaches it (sessions keep running
under the owner). Closing the owner ends ownership (see ADR 0001).

## agentd (V2)

The planned background daemon that takes over ownership in V2 so sessions can
survive the TUI closing. V2-only; V1 must not promise session survival past
owner exit and must not run a background daemon.

## Lifecycle / Attachment / Activity

Three orthogonal axes that PRODUCT.md's single `Status` enum conflates
(`[PRODUCT-DELTA]`, see `session-state-machine.md`):

- **Lifecycle** (process truth): `created | starting | running | stopping |
  exited | failed | unknown`.
- **Attachment** (I/O truth): `attached | detached | unavailable`.
- **Activity** (interaction hint): `unknown | working | idle`, set only with
  provider evidence — never inferred from silence.

Legacy PRODUCT labels map as follows and must not be used as state-machine
states in new contracts: `RUNNING` ≈ lifecycle `running`; `EXITED`/`DEAD` ≈
lifecycle `exited`/`failed` (distinguished by exit reason); `ORPHANED` is an
annotation (`running` + I/O `unavailable`), not a lifecycle state; `IDLE` as a
lifecycle state is removed (idleness is Activity, provider-evidenced only).

## Testability note

Every term above has a Stage 02 table-test hook: Session/Attempt identity
across restart, ProcessIdentity mismatch refusal, orphan-vs-exited
classification, and the ban on silence-inferred idleness. The state machine
document lists the exact assertions.
