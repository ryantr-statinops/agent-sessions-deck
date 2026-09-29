# CLI contract (Stage 00, V1)

Status: **Proposed**. Freezes the V1 command surface, flags, error taxonomy,
exit codes, and I/O rules that Stage 06 implements and tests. Session IDs are
stable strings (Stage 02); existing PRODUCT examples using bare numbers
(`open 42`) are read as shorthand — ambiguous prefixes must be rejected, never
guessed.

## Command surface (V1)

```text
asd                             # open TUI (becomes foreground owner if none live)
asd scan                        # discover agent executables (offline-capable)
asd list                        # list ASD-managed sessions
asd new [agent] [workspace]     # create + attach (becomes owner if none live; needs TTY)
asd open <id>                   # attach to a live session (detach on exit)
asd inspect <id>                # show identity/workspace/attempt/I-O/exit detail
asd rename <id> <name>          # rename (historical records editable offline)
asd restart <id>                # new Attempt, same Session
asd stop <id>                   # graceful stop
asd kill <id>                   # forced termination (explicit)
```

`[PRODUCT-DELTA]` PRODUCT.md §40 also lists `history`, `workspace list`, and
`workspace start`. Those are **not** V1 commands: basic past-session metadata
is visible through `list`/`inspect` in V1; dedicated history ships in V2
(Stage 13) and workspace profiles in V3 (Stage 15). The final-shape list in
PRODUCT must carry that scope note (patched in this slice).

## Global flags and conventions

| Flag / convention | Meaning |
|---|---|
| `--json` | Machine-readable output on stdout with a stable schema (see below). Without it, human tables. |
| `--name <name>` (`new`) | Session name; otherwise prompt (TTY) or generated default. |
| `--agent`, `--status`, `--workspace` (`list`) | Filters; combinable. `--status` accepts lifecycle values (`running`, `exited`, `failed`, `unknown`) — never silence-inferred `idle`. |
| `--yes` | Non-interactive confirmation for destructive actions (`kill`, `stop-all-on-quit`, `restart` of a live Attempt). Required when stdin is not a TTY; without it, fail instead of acting. |
| `--force` | Explicit override for `restart` while the Attempt is live (serialize stop → verify exit → new Attempt). Does not skip identity verification. |
| `-- <argv...>` (`new`) | Extra argv appended to the provider's resolved command. Passed as an array; never re-split, never run through a shell. |
| `--help` | Per-command examples, attach/detach keys, and a one-line V1 owner-lifetime note. |

## Per-command contract (owner × TTY matrix)

Ownership and offline rules are normative in ADR 0001; this table binds them
to flags and output.

| Command | Owner live | No owner, TTY | No owner, non-TTY |
|---|---|---|---|
| `scan` | served via owner | offline binary/PATH probe | same offline probe |
| `list` (+filters, `--json`) | live snapshot, authority `live` | stored metadata, authority `stored` + `observed_at`; no liveness claims | same stored rules |
| `inspect <id>` (+`--json`) | live + stored detail | stored detail, authority `stored` | same |
| `new` | client request → owner launches → requester attaches | requester becomes owner, then launches | fail `NOT_INTERACTIVE` before any spawn |
| `open <id>` | attach; exit detaches only | fail `SESSION_IO_FAILED` (no PTY without owner) | same |
| `rename` | via owner; immediate | historical records: lock + reconcile, then apply | same as TTY offline |
| `restart` | via owner (`--force`/`--yes` if live) | bootstrap owner first (needs TTY), then as online | fail `NOT_INTERACTIVE` before spawn |
| `stop` | graceful stop via owner | only with proven-safe ownership evidence; else fail with guidance | same; `--yes` never bypasses identity proof |
| `kill` | explicit: TTY prompt or `--yes` | same ownership proviso + `--yes` governs the prompt, not the proof | requires `--yes`, still requires proof |

Ambiguous ID prefixes fail with `CONFLICT` (list the candidates); the CLI
never picks one. `open` of an orphaned/unavailable session fails
`SESSION_IO_FAILED` with re-attach guidance and the process left untouched.

## Error taxonomy (typed, user-visible)

Core: `NOT_FOUND` · `NOT_RUNNING` · `NOT_INTERACTIVE` · `UNSUPPORTED` ·
`PERMISSION_DENIED` · `INVALID_CONFIGURATION` · `SESSION_IO_FAILED` ·
`LAUNCH_FAILED` · `OWNER_UNAVAILABLE` · `CONFLICT` · `STALE_ATTEMPT` ·
`CORRUPT_STATE` · `UNKNOWN`.

Rules: every error names the session/attempt, the reason, and the next action
(`kill` suggests `--yes` under non-TTY; `open` on orphan explains the process
may still run; corrupt state prints recovery instructions, never resets).
`stop`-timeout reports still-`running` + kill guidance — it is not reported
as `killed`. Generic `Error: failed` is a contract violation.

## Exit codes (normative for Stage 06 tests)

| Code | Meaning | Examples |
|---|---|---|
| `0` | success | any command that did what was asked |
| `1` | operational failure | `NOT_FOUND`, `LAUNCH_FAILED`, `SESSION_IO_FAILED`, stop-timeout, failed Attempt outcomes |
| `2` | usage error | bad flags, missing args, invalid config reference, ambiguous-ID `CONFLICT` caused by user input |
| `3` | unavailable | `OWNER_UNAVAILABLE`, `NOT_INTERACTIVE`, offline-unsupported mutation, `CORRUPT_STATE` blocking the read |
| `4` | conflict/state | `CONFLICT` on delete-while-active or duplicate create, `STALE_ATTEMPT` on generation mismatch |

`PERMISSION_DENIED` maps to `1` (operation attempted and refused) when the
command ran, or `3` when the environment makes the operation impossible; the
JSON `code` field disambiguates either way.

## Output rules

- `stdout` carries the result (human table or `--json` document); `stderr`
  carries diagnostics, prompts, and progress. Piped stdout never contains ANSI
  escapes.
- `--json` documents include: command, `authority` (`live`|`stored`),
  `observed_at` (UTC), store `revision`, session/attempt identity, lifecycle +
  attachment (+ activity only if provider-evidenced), workspace/Git detail for
  `inspect`, and typed `code` + `reason` + `hint` on errors.
- Human `list` shows the authority downgrade explicitly when offline
  (e.g. a `stored … observed <time>` header), so a stored `running` row is
  never misread as a live process.
- Empty list, missing agent binary, and invalid workspace fail with an
  actionable message (what is missing, where ASD looked, how to fix), not an
  empty table or a stack trace.

## Testability (acceptance hooks for Stage 06)

JSON/error/exit-code assertions per cell of the matrix above; malformed and
oversize IPC frames; protocol-version mismatch; duplicate request IDs produce
one session; attach-disconnect never terminates the agent; second input lease
refused; offline `list` output carries `stored` authority with no liveness
claim; non-TTY `new` with no owner exits `3`/`NOT_INTERACTIVE` having spawned
nothing.
