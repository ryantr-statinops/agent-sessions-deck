# CLI V1

The CLI controls one foreground owner. Run `asd` in terminal A to hold the state-home lock and serve the private Unix socket; it remains in the foreground until Ctrl-C. Stage 07 supplies the dashboard/TUI. `asd` does not daemonize or adopt processes from a previous owner.

## Commands

```text
asd
asd scan [--agent <id> ...] [--json]
asd list [--agent <id>] [--status <lifecycle>] [--workspace <id>] [--json]
asd inspect <session-id> [--json]
asd new [agent] [workspace] [--name <name>] [--json] [-- <argv...>]
asd open <session-id> [--name <client>]
asd rename <session-id> <name> [--json]
asd restart <session-id> [--force] [--yes] [--json]
asd stop <session-id> [--grace <duration>] [--json]
asd kill <session-id> [--yes] [--json]
```

Session ID prefixes resolve only when unambiguous. An ambiguous prefix fails with `CONFLICT`; ASD never chooses a candidate.

### Owner and offline behavior

| Command | Owner available | No owner |
|---|---|---|
| `scan` | Bounded PATH/`extra_paths` probes | Same probes; no owner required |
| `list`, `inspect` | Live owner authority | Stored authority and `observed_at`; stored `running` is not presented as live |
| `new` | Create through owner; attach when TTY is present; `--json` or non-TTY returns the created session without attaching | TTY requester bootstraps and owns the foreground runtime, creates and attaches, then remains owner after detach. Non-TTY fails `NOT_INTERACTIVE` before launch |
| `open` | Requires stdin/stdout TTY; Ctrl+] or input EOF detaches only the client lease | `SESSION_IO_FAILED`: no owner can provide a PTY |
| `rename` | Through owner | Stored records are reconciled against recorded process identity under the offline store lock before renaming. If an active record has no identity, offline rename refuses and directs the user to start the owner. Reconciliation observes only; it does not adopt or signal the process |
| `restart` | Through owner; a process-may-exist attempt needs `--force` and TTY confirmation or `--yes` | TTY bootstraps a foreground owner first. Non-TTY fails before launch |
| `stop`, `kill` | Through owner; stop is graceful and never escalates; kill is explicit | Refuse with owner/identity guidance. No offline process adoption or unverified signal |

`new` prompts for an omitted agent and workspace only on a TTY. A non-TTY create with a live owner requires both positional arguments. Extra argv after `--` is passed literally; ASD never runs a shell or resplits arguments.

`kill` prompts on a TTY unless `--yes` is supplied; non-TTY requires `--yes`. Graceful `stop` does not require confirmation. A stop timeout remains a running session and returns kill guidance.

### Scan statuses

`scan` probes built-in `claude`, `codex`, and `opencode` executables from PATH then configured `extra_paths`; identity markers must match. Configured generic commands are also bounded-probed. Each row reports `available`, `not-found`, or `uncertain`, path/source where observed, and a reason. A candidate name alone is not proof of provider identity.

### Output and errors

Without `--json`, results and progress are human-readable; diagnostics and prompts use stderr. Piped stdout contains no ANSI escapes. `list` prints its authority, observation time, and store revision before rows. Empty lists and unavailable agents include an actionable next step.

With `--json`, output uses lower-case stable keys. Read documents include `command`, `authority`, `observed_at`, `revision`, and session data; `inspect` includes stored/live readings, workspace path, Git metadata when available, attempt identity, command, and exit reason. Mutation documents include their committed revision. Errors use:

```json
{"command":"open","code":"SESSION_IO_FAILED","subject":"session-id","reason":"no foreground owner can provide this session's PTY","hint":"start asd in a terminal, then run open again"}
```

Exit codes: `0` success; `1` operation failure; `2` usage/input error; `3` unavailable/non-interactive/corrupt state; `4` conflict or stale generation. The JSON `code` disambiguates causes sharing an exit status.

## Owner lifetime

The state-home `.lock` is claimed before the socket is bound. A second owner cannot start. Client disconnection detaches an `open` lease; it never requests child termination. An owner replacement gets a new instance ID, and clients refuse to retry an uncertain mutation against a different owner instance.
