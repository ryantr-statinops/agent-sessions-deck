# Stage 00 — Terminal spike (isolated, independently runnable)

Scope: this directory plus `docs/testing/terminal-compatibility.md` only.
Nothing here is production code and nothing is imported by production code.
Isolated `go/` module; the repository root has no `go.mod` involvement.

## Exact one-command rerun (from repository root)

```bash
timeout 300 bash experiments/terminal/run.sh
```

Offline variant (Python stdlib PTY probes only, no `go run`):

```bash
timeout 300 bash experiments/terminal/run.sh --skip-go
```

## Cleanup path

```bash
bash experiments/terminal/cleanup.sh                 # reap stale .run/run-*/ dirs, drop nothing live
bash experiments/terminal/cleanup.sh --wipe-evidence # also drop evidence/
```

Safety: no `pkill`, no /tmp wildcards. Each run owns one private
`.run/run-<pid>-*/` dir (recorded PID + starttime); `run.sh` signals only
its current direct child and removes only its own dir, probes use unique
private scratch (under `$ASD_SPIKE_RUN_DIR` when run via `run.sh`, else a
0700 `mkdtemp`) and reap direct `pty.fork` children by exact PID, and
`cleanup.sh` removes only stale per-invocation dirs with verified-dead
owners — it refuses live/uncertain entries instead of killing by name and
never sweeps `/tmp`. Orca version probe uses `$ORCA_CLI_COMMAND` else
`orca-ide`, never bare `orca`.

Verify no test child survives (PID-scoped, no name kills): check that no
`.run/run-*/runner.pid` owner is still alive, e.g.
`bash experiments/terminal/cleanup.sh` reports `cleanup: OK`. A same-named
sentinel (e.g. `exec -a asd-spike sleep 300`) must remain alive across
run + cleanup.

## What it proves (executable evidence in `evidence/`)

| Probe | File | Covers |
|---|---|---|
| PTY fundamentals | `probes/pty_basics.py` | controlling TTY, winsize, resize+SIGWINCH, fg process group (+bg read stall), I/O round-trip, Ctrl-C/SIGINT, Unicode, 1 MiB flood bound |
| Query/response | `probes/query_response.py` | DA/DSR/DECRQM get no reply without an emulator (transparency proof) |
| Alt-screen transport | `probes/altscreen.py` | smcup/rmcup/cup/ED/EL pass through byte-identical |
| Two sessions | `probes/two_sessions.py` | detached A keeps producing; B isolated; re-attach restores latest; flood-while-detached stalls writer (must drain in background) |
| Bounded corpus | `probes/corpus.py` + `fixtures/corpus/*.ansi` | 10 transport cases + cooked-mode ONLCR note |
| Go fidelity layer | `go/main.go` (own `go.mod`) | creack/pty parity; `x/vt` emulator assertions; `x/ansi` tokenizer baseline |

Results: `docs/testing/terminal-compatibility.md` (matrix, versions,
recommendation, gaps). No production compatibility is claimed.

## Bounds and safety

- No system packages installed; no agent CLI launched interactively
  (`--version` probes only, no auth, no cost).
- Every probe kills and reaps its children, closes fds, uses timeouts, and
  never touches the outer terminal (own PTYs only).
- Go dependencies are pinned inside `go/go.mod` for the spike only and do
  not pin any project-wide dependency choice.
