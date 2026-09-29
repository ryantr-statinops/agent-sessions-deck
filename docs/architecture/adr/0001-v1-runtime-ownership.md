# ADR 0001 — V1 foreground owner, shutdown semantics, and the agentd (V2) boundary

Status: **Proposed** (Stage 00 contract slice; starting point from the plan
README decisions 1–2, not an approved product fact). Owner of this file's
scope: Stage 00 contracts worker. Terminal-emulator selection is explicitly
out of scope here (ADR 0002, separate spike).

## Context

PRODUCT.md describes `asd open/stop` issued "from another process/CLI" (§§13,
40) but only introduces a daemon (`agentd`) in V2 (§24). Without an ownership
rule, two `asd` processes could spawn duplicate runtimes, write the same JSON
store, or kill each other's children. The plan README proposes one foreground
owner per state home plus a local Unix control socket. This ADR turns that
proposal into a testable contract.

## Decision (proposed)

1. **Exactly one foreground owner per ASD state home in V1.** The first
   interactive `asd` / `asd new` process acquires the state-home lock, owns
   the SessionManager, all PTY handles, and the application service, and
   serves a local Unix control socket. Every other invocation is a client.
2. **No background daemon in V1.** Nothing in V1 outlives the owner's process
   tree on purpose. Session survival past owner exit is never promised.
3. **A V1 control socket is not a V2 daemon.** The socket is a foreground
   convenience (many terminals, one runtime). `agentd` (V2) is a different
   ownership regime: background lifetime, survival of TUI close, continuous
   reconciliation. Sharing a framing shape does not merge the two; Stage 11
   re-decides lifetime, supervision, and migration.
4. **Single writer.** Only the owner mutates the store. Clients mutate via IPC
   requests that the owner serializes. A client never writes store JSON
   directly while an owner is live (see ADR 0003 for the lock).

## Bootstrap and identity

- Bootstrap order is atomic: acquire the state-home lock **first**, then
  create/bind the socket. A second launcher that finds a live lock + passing
  handshake becomes a client; it never spawns a rival owner.
- The owner mints an instance ID at startup and includes it in every handshake
  and in each Attempt's ProcessIdentity. Stale-socket cleanup is
  instance-fenced: a cleanup path belonging to an old instance must never
  delete a new owner's socket (Stage 09 verifies this).
- Two simultaneous bootstraps resolve through the lock: exactly one wins; the
  loser becomes a client or reports `OWNER_UNAVAILABLE` / conflict honestly.

## Client vs. owner shutdown

| Event | Semantics |
|---|---|
| Client exits (`q` in a client view, `open` session ends, client crash) | **Detach only.** The session keeps running under the owner. Client exit never signals agent processes. |
| Owner quits with no live sessions | Exit normally after persisting final observations. |
| Owner quits with live sessions | Show an explicit choice: **Cancel** or **Stop all and quit** (with a live-session count). Never silently orphan and never silently kill. A bounded graceful stop applies; children that ignore TERM are reported as not-stopped, and force-stop-all requires a second explicit action. The UI must not claim "stopped" for children it abandoned. |
| `SIGTERM` / `SIGHUP` to the owner | Public policy: graceful shutdown — persist observations, attempt bounded graceful stop of owned groups, log residual processes that did not stop, no silent escalation to SIGKILL. |
| `SIGKILL` / owner crash | No guarantees: PTY handles are lost, recent observations may be unpersisted. Restart does metadata recovery + liveness classification only (see state machine: stale-running vs. orphan vs. unknown). Crash recovery never recreates a PTY and never auto-kills or auto-adopts survivors. |
| `Ctrl+C` in an attached session view | Agent input (goes to the PTY), not an ASD shutdown command. |

## Offline and non-TTY semantics

"Offline" = no live owner reachable. "Non-TTY" = stdin is not an interactive
terminal.

| Command | Owner live | No owner (offline) | No owner + non-TTY |
|---|---|---|---|
| `scan` | via owner (same result) | allowed offline (binary/filesystem reads only) | allowed |
| `list` | live snapshot with authority `live` | allowed offline from persisted metadata with authority `stored`, `observed_at` timestamp, and no claim that stored-`running` means running now | allowed (same offline rules) |
| `inspect` | live + stored | allowed offline, same authority downgrade | allowed |
| `new` (full TUI or prompts) | client request; owner launches, requester attaches | requester **becomes** the foreground owner, then launches | **fail** with `NOT_INTERACTIVE` before spawning any child — never create an unmanaged process |
| `new` with complete args (scriptable create) | allowed; returns session ID | same as above (becomes owner) only with a TTY; non-TTY fails `NOT_INTERACTIVE` | fail `NOT_INTERACTIVE`, no spawn |
| `open` | attach to live PTY; exit detaches | fail honestly: `SESSION_IO_FAILED` (PTY unavailable) with guidance; never synthesize a screen | same |
| `rename` (historical record, exit done) | via owner | allowed with a short lock + reconcile first | allowed |
| `restart` (historical) | via owner | must bootstrap a foreground owner first, hence needs a TTY like `new`; non-TTY fails before spawn | fail `NOT_INTERACTIVE` |
| `stop` / `kill` offline | via owner | only if ownership can be proven safe per the state machine (verified ProcessIdentity); otherwise fail with guidance instead of signalling a possibly-recycled PID | same; `kill` additionally needs `--yes` (see CLI contract) |

## V1 / V2 boundary (what changes in Stage 11, what does not)

- Unchanged: Session/Attempt model, single-writer store, ProcessIdentity
  checks, explicit destructive actions, no auto-adopt of foreign processes.
- Changes: ownership lifetime (foreground process → background `agentd`),
  survival of TUI close becomes guaranteed-by-daemon instead of impossible,
  continuous reconciliation and event retention move into the daemon.
- The V1 socket framing (version/request-ID/operation/payload/error/revision)
  is designed to be reusable by the daemon, but reuse is a Stage 11 decision
  after a migration plan exists. No V1 code or doc may assume `agentd`
  exists.

## Alternatives considered

- **Background daemon in V1.** Rejected: supervision, socket lifecycle, and
  crash semantics are a full project (Stage 11); shipping half of it as an
  accident of the V1 socket would promise survival semantics we cannot keep.
- **Lock-free multi-writer JSON.** Rejected: lost updates and split-brain
  session records under concurrent CLI/TUI writes; Stage 03 fault tests would
  fail.
- **Adopting foreign processes by PID/name scan.** Rejected: PID reuse and
  name collisions make this unsafe; PRODUCT already scopes V1 to
  ASD-launched sessions only.
- **`sh -c` command strings for launch.** Rejected: quoting/splitting bugs
  and secret leakage; commands are executable + argv arrays (ADR 0003, Stage 03
  strict config).

## Assumptions and evidence

- Assumptions: Linux-first (`/proc` starttime, process groups, Unix sockets
  with same-user access, XDG runtime dir usually present); single user per
  state home; Go standard library + syscalls suffice for lock/socket/signal
  mechanics (library pins are Stage 01, not this ADR).
- Evidence at Stage 00: none executed by this slice — ownership/liveness
  proofs arrive with Stage 05 runtime tests (controlling TTY, identity
  mismatch fixtures), Stage 06 owner/client E2E (two-terminal test,
  dual-bootstrap race, stale-socket recovery), and Stage 09 fault matrix
  (owner crash → classification, ignored-TERM honesty). Acceptance hooks are
  listed so the claims above are falsifiable.

## Unresolved maintainer choices

- Exact socket path when `XDG_RUNTIME_DIR` is absent (private owned fallback
  with a documented cleanup policy — Stage 06 decides, ADR 0003 holds the
  path table).
- Whether offline `stop` of a verified-owned survivor is ever offered in V1,
  or always deferred with guidance (needs Stage 09 identity-proof results).
- Go module identity and license: recorded as open in ADR 0003; they do not
  block this contract but must be chosen before public release (Stage 01/10
  gates).

## Testability

Stage 06 acceptance traces directly to this ADR: terminal A owns while B/C
act as clients without lost updates; offline scan/list/inspect work with
downgraded authority; TTY-less `new` with no owner fails without spawning;
dual bootstrap yields one owner; stale-socket recovery never kills a live
owner; duplicate request IDs never double-launch; attach disconnect never
terminates the agent.
