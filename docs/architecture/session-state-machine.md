# Session state machine (Stage 00 contract)

Status: **Frozen for Stage 02**. Stage 00 established this transition contract; Stage 02 implements and tests the reducer. Stages 05/09 execute process ownership and crash reconciliation. Terms are defined in `glossary.md`; ownership and storage rules live in `adr/0001-…` and `adr/0003-…`.

## The three axes (normative)

A Session's observable state is always a triple. No single enum is the state.

**Lifecycle** — what the process is doing (OS is the source of truth):

```text
created → starting → running → stopping → exited | failed
                                        ↘ unknown (probe inconclusive)
```

- `created`: record exists, nothing spawned yet.
- `starting`: spawn requested, child identity not yet captured.
- `running`: child identity captured, no terminal exit observation.
- `stopping`: graceful stop requested, child not yet reaped.
- `exited`: child reaped; exit reason recorded (code/signal, natural or
  stop-induced).
- `failed`: launch failed, restart precondition failed, or runtime fault
  (never a bare string — always with a reason).
- `unknown`: liveness could not be established for a captured identity (probe timeout or permission denied). `unknown` never auto-converts to `exited`; an attempt without identity remains `starting` until T2 returns a spawn result.

**Attachment (I/O)** — whether ASD can show the session now:

```text
attached | detached | unavailable
```

- `attached`: a client holds the session's interactive lease.
- `detached`: no client attached, but the PTY handle is healthy (re-attachable).
- `unavailable`: no PTY handle exists for the current Attempt (never started,
  already exited, or PTY lost).

**Activity** — interaction hint, provider evidence only:

```text
unknown | working | idle
```

Default is `unknown`. Silence never implies `idle`; only a provider signal
with documented evidence may set `working`/`idle`.

**Orphaned** is not a lifecycle state. It is the annotation
`running + unavailable` (process alive, PTY gone — typically after owner
crash). The original observations are preserved alongside the annotation.

Legacy PRODUCT labels map per `glossary.md`; `IDLE` as a lifecycle state is
removed by this contract.

## Transition table (Attempt-scoped; Session keeps its ID, Attempt bumps generation)

| # | From | Event | To | Notes / recorded reason |
|---|---|---|---|---|
| T1 | `created` | launch requested, validation fails | `failed` | `launch-failed: <typed error>`; no child exists; nothing to clean up. |
| T2 | `created` | launch requested, validation passes | `starting` | Attempt generation assigned, resolved argv frozen. |
| T3 | `starting` | spawn succeeds, identity captured, persisted | `running` + `detached` | Persist-then-run ordering per Stage 05 launch transaction. |
| T4 | `starting` | spawn fails (bad executable, cwd invalid, PTY setup error) | `failed` | `launch-failed`; no invisible child may remain. |
| T5 | `starting` / `running` | persist-after-spawn fails | `failed` + cleanup | Child is killed **only** via verified identity, then `launch-failed: store-error`. |
| T6 | `running` | child exits naturally, or runtime reports a confirmed reap after an explicit kill | `exited` | Natural exits retain `natural-exit` (any code); a late confirmed kill reap retains `killed`. T6 rejects T8 stop and T16/T17 reconciliation reasons. Drain trailing output first. |
| T7 | `running` | `stop` requested | `stopping` | Graceful signal to the **verified owned group**; start timeout. |
| T8 | `stopping` | child exits within timeout | `exited` | Reason `stopped` (distinguishes from natural exit and from kill). |
| T9 | `stopping` | timeout, child still alive | `running` | Return to `running` with a `stop-timeout` note + kill guidance. Never auto-escalate to SIGKILL. |
| T10 | `running` / `stopping` | `kill` requested (explicit) | `exited` | Force termination of the verified owned group; reason `killed`. Requires explicit action (prompt or `--yes`, see CLI contract). |
| T11 | any active | `restart` requested on a live Attempt | serialize stop → verify exit → new Attempt | Restarting a running Attempt needs explicit `--force`/confirmation; never two concurrent Attempts. New Attempt re-resolves argv in the same workspace; vendor conversation resume is **not** implied. |
| T12 | `exited` / `failed` | `restart` requested | new Attempt at `starting`, same Session ID | Generation bumps; old Attempt's exit reason is immutable history. |
| T13 | `exited` / `failed` | restart precondition fails | `failed` (new Attempt) | Reason `restart-failed`; the prior Attempt record is untouched. |
| T14 | `running` / `stopping` | PTY lost, process verified alive | `running` / `stopping` + `unavailable` | Annotate **orphaned** only for `running`; a `stopping` attempt remains stopping without the orphan note. Process is not signalled. |
| T15 | `running` | PTY lost, liveness unverifiable | `unknown` + `unavailable` | Preserve raw observations; no auto-kill, no auto-adopt. |
| T16 | `running` (stored) | owner restarts, PID missing | `exited` | Reason `dead: process gone`; reconciliation evidence recorded. |
| T17 | `running` (stored) | owner restarts, PID present but identity mismatches (starttime/boot/owner differ) | `exited` (old observation closed) | The old Attempt ends as stale; the live PID is **not** adopted and never signalled. |
| T18 | any active Attempt with a captured ProcessIdentity | liveness probe times out / permission denied | `unknown` | Record the reason; a failed probe never fabricates `exited`. A `starting` Attempt without identity is not probeable: refuse T18 and wait for the T2 spawn result. |
| T19 | any active | stale callback from an older generation arrives | ignored | Generation check drops it; current Attempt untouched (Stage 02/05 tests). |
| T20 | any | metadata delete requested while lifecycle active | rejected | `CONFLICT`: delete requires `exited`/`failed` first (CLI contract). |

## Operation semantics (must not be conflated)

- **Detach** (client leaves `open`, client exits): Attachment `attached` →
  `detached`. Lifecycle untouched; the process continues while the owner lives.
- **Stop**: graceful termination request (T7–T9). May end in `exited/stopped`
  or back in `running` on timeout.
- **Kill**: forced termination (T10). Always explicit, always verified identity.
- **Delete metadata**: store removal only. Blocked while active (T20).
- **Restart**: new Attempt, same Session (T11–T13). Never native vendor
  resume; the `Resume` provider capability stays off without adapter +
  evidence (Stage 04).

## Reconciliation invariants (Stages 05/09 implement, Stage 02 tests)

1. Owned-child `Wait` is the primary exit source; periodic reconciliation only
   classifies what `Wait` has not (yet) reported.
2. No signal without verified ProcessIdentity (PID + starttime + boot ID +
   owner instance). PID alone, or name matching, never authorizes a signal.
3. `unknown` requires a captured ProcessIdentity and is sticky until real evidence arrives — never decays into `exited` on a timer.
4. Crash recovery classifies persisted `running` into exactly one of
   `exited` (T16), stale-closed (T17), `orphaned` (T14), or `unknown`
   (T15/T18). It never recreates a PTY, never auto-restarts, never auto-kills.
5. A surviving orphan blocks auto-restart of its Session until an explicit,
   identity-verified destructive action resolves it.

## Testability (acceptance hooks for Stage 02+)

Table tests must cover every row T1–T20 including rejections (T1, T9-timeout
return, T19 drop, T20 refusal); restart keeps Session ID and bumps generation;
natural nonzero exit (T6) is distinguishable from launch failure (T4) and from
stop-timeout (T9); stop-timeout never becomes `killed`; generation-fenced
callbacks and PID-reuse fixtures with an external sentinel process prove no
foreign signalling; race suites cover concurrent exit/stop/restart/input/resize
without leaking FDs or goroutines.
