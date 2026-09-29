# `tests/testagent` — Stage 01 fake agent

Deterministic stand-in for a coding-agent CLI, plus the harness that drives it.
No installed agent, no network, no account, no credentials.

Linux only, matching the project's Linux-first scope.

## Layout

| Path | Role |
|---|---|
| `control/` | wire types and the newline-delimited JSON codec |
| `fakeagent/` | the fixture binary (`package main`) |
| `harness_test.go` | pty spawn, bounded event waits, identity-based cleanup |
| `modes_test.go` | mode matrix and per-mode behaviour |
| `lifecycle_test.go` | exit/signal, child and grandchild identity, leak checks |

## Run

```bash
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=vendor go test -tags=integration ./tests/testagent -count=1
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=vendor go test -tags=integration -race ./tests/testagent -count=1
```

The `linux && integration` suite is opt-in; it does not run in the default unit-test command. `TestMain` compiles the fixture once with network access disabled and vendored dependencies. The suite cannot mutate `go.mod` or `go.sum`.

Each fixture gets a temporary working directory and private `HOME`/`XDG_*` roots; cleanup removes them after the process is reaped.

## Control channel

The test passes a full-duplex `AF_UNIX` `SOCK_STREAM` socket as child **fd 3**.
The fixture writes newline-delimited JSON **events** to fd 3 and reads
newline-delimited JSON **commands** from it. `stdin`, `stdout` and `stderr`
stay bound to the PTY, so terminal bytes and control bytes never share a
descriptor.

The channel is close-on-exec in the fixture, so a spawned helper never inherits
it. A helper gets its own inherited descriptors instead: fd 3 is the identity
pipe it reports on, fd 4 is the go-away pipe whose EOF makes it exit.

### Events (fixture → test)

| Event | Payload | Meaning |
|---|---|---|
| `ready` | `pid`, `pgid`, `sid`, `mode`, `label`, `arg` | first event of every run; identity resolved from `/proc` |
| `started` | `mode` | the mode passed its start-up gate |
| `ident` | `role`, `pid`, `ppid`, `pgid` | child or grandchild identity |
| `line` | `n`, `data` | one newline-stripped line of terminal input |
| `size` | `rows`, `cols`, `reason` | `start`, `winch` or `getsize` |
| `query` | `kind`, `raw` | terminal reply bytes observed on stdin |
| `signal` | `name`, `count` | a caught signal |
| `frame` | `n`, `rows`, `cols` | one full-screen repaint |
| `sample` | `name`, `hex`, `size` | one fixed Unicode payload |
| `flood` | `bytes`, `lines`, `recordlen` | total of a bounded output burst |
| `exit` | `code`, `signal`, `reason` | always the last event |

### Commands (test → fixture)

| Command | Effect |
|---|---|
| `go` | release a start-up gate (`slow-start`) |
| `exit` | exit with `code` |
| `stop` | exit cleanly, default status 0 |
| `getsize` | emit a `size` event with `reason=getsize` |
| `write` | write `data` to the terminal |
| `query` | write `data` to the terminal as a terminal query |

## Modes

| Mode | Behaviour |
|---|---|
| `echo` | echoes each input line to the terminal and reports it on the control channel |
| `line` | reports input lines on the control channel only, writing nothing to the terminal |
| `ansi` | enters the alternate screen, repaints every row on each window change, leaves cleanly |
| `exit-code` | exits immediately with `--exit-code` |
| `slow-start` | withholds `started` until an explicit `go` command |
| `ignored-term` | traps TERM/INT/HUP/QUIT, reports each, stays alive until asked to exit |
| `child` | spawns a direct child and reports its kernel-resolved identity |
| `grandchild` | same, plus a grandchild under that child |
| `flood` | writes a bounded deterministic burst; blocks on a full PTY buffer if nobody drains |
| `unicode` | writes a fixed multi-byte payload, then echoes input byte for byte |
| `query` | writes DA/DSR/DECRQM queries and reports each reply observed |
| `size` | reports size at start-up, on every `SIGWINCH`, and on demand |
| `helper` | internal identity leaf; not part of the public matrix |

Flags: `--mode` (required), `--label`, `--exit-code`, `--flood-bytes`,
`--flood-lines`, plus `--role` and `--spawn` for helpers.

## Guarantees and boundaries

- **No sleeps for ordering.** Every wait is on a named event or a control
  command. The only bounded polls are the terminal-drain reader, which exists
  because draining runs in its own goroutine, and the reap fallback, which runs
  only after a bounded wait has already failed.
- **Deadlines everywhere.** Event reads, process waits and child reaps are all
  deadline bounded.
- **Identity-based cleanup.** Process start times are recorded with fixture PIDs. Signals go through the original `exec.Cmd` process handles; there is no `pkill`, executable-name matching or `/tmp` sweep.
- **No fixture survives.** After the suite, `TestMain` re-checks every recorded process identity and PTY file identity. The leak tests also assert that the fixture's exact `/proc/<pid>/stat` identity and PTY are gone after teardown.
- **Reaping is asserted, not assumed.** Helpers ignore `SIGHUP`, `SIGTERM` and `SIGINT`, and exit only on an explicit `release` message or a `SIGKILL` through the original process handle. They cannot end incidentally when the PTY or fixture goes away, so a surviving descendant means the fixture genuinely failed to reap it.
- **Kernel-verified identity.** Child and grandchild claims are re-read from `/proc/<pid>/stat` by the test, including process start time, so a wrong or recycled PID cannot pass.

## Known limits

- Linux only; no macOS or Windows evidence.
- `ansi` uses `CUP` + `EL` per row rather than a full clear, so the echo and
  window-change regions remain visible; it does not exercise `SCO` save and
  restore, which Stage 00 already recorded as a gap in the candidate emulator.
- The PTY slave is put in raw mode by the harness, so byte-for-byte assertions
  are meaningful. A runtime that hands over a cooked-mode slave would see
  `ONLCR` translation instead.
- `flood` defaults to 1 MiB but the tests use a smaller bound; the drain
  buffer has a hard cap and fails the test rather than truncating silently.
