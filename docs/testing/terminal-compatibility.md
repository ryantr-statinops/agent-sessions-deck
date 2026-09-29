# Terminal compatibility — Stage 00 spike evidence

Scope: isolated spike only. Nothing here claims production compatibility for
any agent, emulator, or library. All statements below are backed by the
rerunnable spike in `experiments/terminal/`; where a behavior could not be
tested safely, the gap is recorded instead of asserted.

- Spike: `experiments/terminal/` (README + `run.sh` + `cleanup.sh`)
- Evidence (regenerated per run): `experiments/terminal/evidence/*.json`, `versions.txt`
- Corpus: `experiments/terminal/fixtures/corpus/*.ansi` (10 bounded fixtures)

## 1. Rerun and cleanup (exact commands, from repository root)

```bash
timeout 300 bash experiments/terminal/run.sh            # full spike
timeout 300 bash experiments/terminal/run.sh --skip-go  # PTY probes only, no `go run`
bash experiments/terminal/cleanup.sh                    # reap stale .run/run-*/ dirs, keep evidence
bash experiments/terminal/cleanup.sh --wipe-evidence    # also drop evidence/*.json*
bash experiments/terminal/cleanup.sh                    # verify: prints `cleanup: OK` when no owner survives
```

Safety: no `pkill -f`, no /tmp wildcards. Each run owns one private
`.run/run-<pid>-*/` dir (PID + starttime recorded); `run.sh` signals only
its current direct child, probes use unique private scratch and reap direct
`pty.fork` children by exact PID, and `cleanup.sh` removes only stale dirs
with verified-dead owners — refusing live/uncertain entries instead of
killing by name, never sweeping `/tmp`. Orca version probe uses
`$ORCA_CLI_COMMAND` else `orca-ide`, never bare `orca`.

Last full pass: all probes green (see §4). Per-probe results are printed as
`PASS`/`FAIL` lines and stored as JSON (`{"probe":…, "results":[…]}`).

## 2. Host and versions (measured 2026-09-29, `evidence/versions.txt`)

| Item | Version / value |
|---|---|
| Host | Linux 7.1.5-76070105-generic x86_64 |
| Go toolchain | go1.26.7 linux/amd64 |
| Python | 3.12.3 (stdlib `pty`/`termios` only, no packages installed) |
| tmux (reference only) | 3.4 |
| Terminfo | xterm-256color (`cup=\E[%i%p1%d;%p2%dH`, smcup/rmcup `?1049h/l` present) |
| `creack/pty` (spike pin) | v1.1.24 |
| `charmbracelet/x/vt` (spike pin) | v0.0.0-20260927004216-9c77d672503d — **no tagged release exists** (pseudo-version) |
| `charmbracelet/x/ansi` (spike pin) | v0.11.8 |
| opencode CLI | 1.18.33 (`--version` only, never launched interactively) |
| omp CLI | omp/18.4.2 (`--version` only) |
| orca CLI | 1.4.215 (`--version` only, via linux-orca-cli-shim) |

Spike pins live in `experiments/terminal/go/go.mod` and pin **nothing**
project-wide (the repository root has no `go.mod` involvement).

## 3. What the corpus is

10 bounded fixtures (each under 120 bytes, total under 1 KiB), replayed
through a bare `cat` behind a PTY with a `raw -echo` slave:

`sgr_colors`, `cursor_addressing`, `erase`, `alt_screen`, `unicode_mixed`
(CJK + emoji + combining marks), `osc` (title + hyperlink), `queries`
(DA/DSR/DECRQM), `bracketed_paste`, `scroll_region`,
`split_multibyte` (same bytes as `unicode_mixed`, written 1 byte at a time).
Plus a cooked-mode `a\nb\n` case documenting ONLCR (`\n` → `\r\n`).

## 4. Results matrix (all PASS on this host)

### 4.1 PTY fundamentals (`evidence/pty_basics.json`, 8/8)

| Behavior | Result | Note |
|---|---|---|
| Controlling TTY | PASS | child `tty` → `/dev/pts/N`; `stat` tty_nr nonzero |
| Initial winsize | PASS | `stty size` → `24 80` after TIOCSWINSZ |
| Resize + SIGWINCH | PASS | 2 resizes → 2 trapped SIGWINCH deliveries |
| Foreground process group | PASS | child `tpgid == pgid == pid` via `ps` from inside |
| I/O round-trip | PASS | byte-exact under `raw -echo` slave |
| Ctrl-C / ISIG | PASS | `0x03` fired a bash `INT` trap in the fg group |
| Unicode | PASS | 72-byte UTF-8 (CJK/emoji/combining) byte-exact |
| Output flood bound | PASS | 1 MiB drained in 0.03 s (~36,683 KiB/s) with no hang; single-run measurement, not a threshold |

### 4.2 Query/response (`evidence/query_response.json`, 4/4)

DA primary/secondary, DSR cursor report, DECRQM: **zero reply bytes** beyond
our own echo. The PTY is a transparent pipe; ASD's own emulator layer must
answer these (and the `x/vt` candidate does — §5).

### 4.3 Alt-screen transport (`evidence/altscreen.json`, 6/6)

smcup/rmcup/cup/ED/EL byte-identical through the PTY. The transport never
interprets screen semantics — an emulator layer must.

### 4.4 Two sessions (`evidence/two_sessions.json`, 4/4)

| Check | Result | Evidence |
|---|---|---|
| Detached A keeps producing | PASS | A tick 6 → 28 in its log while undrained; viewer buffer untouched |
| Screen restore after viewing B | PASS | re-attach shows tick 38; source sequence contiguous (39 ticks) |
| Session isolation | PASS | B advanced 12 → 28 independently throughout |
| Flood-while-detached stalls writer | PASS | attached n=39, max gap 31 ms; 2 s undrained flood n=120, max gap 1867 ms (child blocked on full ~64 KiB PTY buffer) |

Architectural consequence: the runtime must keep **draining every session's
PTY in the background**, even while the user views another session.
Raw handoff (read only the viewed session) stalls agents under output flood
and loses the detached screen at the UI layer.

### 4.5 Corpus transport (`evidence/corpus.json`, 10/10)

All 10 fixtures byte-identical through the raw PTY, including 1-byte-chunked
multibyte writes. Cooked mode confirmed to translate `\n` → `\r\n`
(production slaves must use raw mode).

### 4.6 Go fidelity layer (`evidence/go_spike.json`, 32/32 + 1 documented gap)

- `creack/pty`: spawn with winsize, `Setsize`/`Getsize` resize verify, Unicode
  round-trip — all PASS (parity with the Python probes).
- `x/vt` emulator: cursor addressing cells (X/Z/U/D at exact positions),
  SGR styling, ED/EL erase, alt-screen enter/exit (`IsAltScreen`),
  CJK in `Render()`, OSC/paste/scroll/split consumption — all PASS.
- `x/vt` **answers queries**: DA/DSR/DECRQM drew reply
  `\x1b[?62;1;6;22c\x1b[1;1R\x1b[?2004;2$y` (VT220-class DA, cursor report,
  DECRQM). This is the behavior ASD needs and bare PTY lacks.
- `x/ansi` parser baseline: tokenizes 0–10 dispatches per fixture correctly
  but keeps **no** screen/cursor/alt-screen state — parser ≠ emulator.
- Documented gap `gap_vt_sco_save_restore` (non-blocking): SCO save/restore
  (`ESC s` / `ESC u`) is **not implemented** in this `x/vt` revision;
  DECSC/DECRC (`ESC 7` / `ESC 8`) works and is the portable path agents
  should rely on.
- Two-session fullscreen integration (`twin_*`, 7/7 blocking) — see §4.7.

### 4.7 Two-session fullscreen integration (`evidence/go_spike.json`, `twin_*` 7/7)

Deterministic fake-fullscreen children (Python, `python3 -u -c` inline in
`go/main.go` — no agent CLIs, no auth, no cost): each redraws a
cursor-addressed header (`CUP home` + `EL` per line, no full clear so
echo/WINCH areas persist), echoes stdin as `ECHO-<label>:<line>`, and traps
`SIGWINCH` to print `WINCH-<label>`. Two independent PTYs
(`creack/pty`, same layer as §4.1/§4.6) each drain continuously into their
own `vt.SafeEmulator`; only each session's drain goroutine calls `Write`
(single-writer), `Render`/`Resize` go through the safe wrapper. No screen
payloads are printed to runner stdout; JSON details carry only bounded
metadata (frame numbers, byte counts, dimensions).

| Check | Result | Evidence (this host) |
|---|---|---|
| `twin_pty_spawn` | PASS | two PTYs 24×80, distinct masters + emulators |
| `twin_bg_feed` | PASS | detached A fed 2260 bytes, frames 0004→0054, while B viewed ×25 (B 0004→0054) |
| `twin_input_routing` | PASS | tokens reached only selected PTY; echo observed in 1.390 ms (B) / 1.577 ms (A) |
| `twin_resize_isolation` | PASS | PTY A=24×80 vs B=30×100; emu A=80×24 vs B=100×30; `WINCH-B` only in B, observed in 2.259 ms |
| `twin_reattach_latest` | PASS | A render frame 0055 == drain-max 0055, contiguous 56 frames from base 0004; B contiguous |
| `twin_no_contamination` | PASS | A has `SESSION-A`, no B markers; converse for B; emulator reply bytes 0/0; no scratch files |
| `twin_perf_baseline` | PASS | 100× 80×24 `Render()` max 153.445 µs; timings are one run, 1 ms polling, no thresholds |

What this proves beyond §4.4 (which covers PTY byte-stream
progress/restoration with one emulator at a time in §4.6): two independent
full-screen session states coexist — detached output is continuously
emulated per session, reattach renders the latest frame with no cross-talk
and no lost sequence, input is routed only to the selected session, and
resize dimensions/signals apply per session. Limits: fake children at
~20 Hz with small headers (not real agents, no alt-screen in the twin
loop, single volume point); real-agent compatibility remains unverified
(§7).

## 5. Emulator candidate comparison (bounded, evidence-backed)

| Candidate | Verdict from this spike | Evidence |
|---|---|---|
| Raw handoff (no emulator) | Rejected as multi-session solution | §4.4 stall/screen-loss; §4.2 no query answers |
| `charmbracelet/x/vt` @ pseudo-version | Viable, with reservations | §4.6: full corpus green + answers queries; §4.7: two-session fullscreen green; BUT untagged API, SCO gap, reply identity (`?62…`) must be validated against real agents |
| `charmbracelet/x/ansi` parser | Insufficient alone | tokenizes, keeps no screen state (§4.6) |
| Bubble Tea viewport over ANSI strings | Not evaluated as emulator | architecture decision stands on product grounds (viewport ≠ terminal); no executable claim made here |
| Minimal hand-rolled screen model | Not built | would need its own corpus + fuzzing to match `x/vt` coverage; no cost paid yet |

## 6. Recommendation for Stage 05/08 (spike-scoped, not a production claim)

1. Prototype the session runtime on `creack/pty` (stable, tagged v1.1.24)
   with per-session background drain threads and bounded scrollback caps.
2. Prototype the screen layer on `charmbracelet/x/vt`, pinned to the tested
   pseudo-version or newer, with a regression test for the SCO gap and for
   the exact DA/DSR reply bytes real agents expect. Keep one emulator per
   session with a single writer (only the session drain writes; renders and
   resizes serialize through the same guard, as the `twin_*` probe does
   with `vt.SafeEmulator`).
3. Run slaves in `raw -echo` mode; let the emulator (not the line discipline)
   own echo, ISIG handling scope, and query answers.
4. Never rely on outer-terminal `tcsetpgrp` from a detached process; manage
   foreground membership from inside owned sessions.
5. Route input only to the selected session and apply resize (winsize +
   emulator dimensions + `SIGWINCH`) per session, as demonstrated by
   `twin_input_routing` / `twin_resize_isolation`. The key-routing contract
   for this (prefix, literal forwarding, `Ctrl+C` scope, terminal restore)
   is Proposed in §8 — not measured by this spike.

## 7. Coverage gaps (recorded, not faked)

- **No interactive agent CLI launched**: opencode/omp/orca probed with
  `--version` only (auth/cost rule). The `twin_*` two-session fullscreen
  proof uses deterministic fake children, not real agents. A smoke matrix
  against real agent CLIs is required before Stage 10; absence of
  `claude`/`codex`/`aider` binaries on this host is already a coverage hole
  for that matrix. Nothing in §4.7 claims production agent compatibility.
- **Shortcut UX unmeasured**: the §8 key-routing contract (prefix, literal
  forwarding, `Ctrl+C` scope, terminal restore) is Proposed only — the
  spike never exercised shortcut handling, and no shortcut acceptance is
  claimed here. Stage 08 acceptance hooks are listed in §8.
- **Background-read SIGTTIN stop** not demonstrated: a detached runner
  without its own controlling terminal cannot `tcsetpgrp` a foreign PTY
  (ENOTTY); fg membership proven from inside instead (§4.1).
- **Paste throughput** bounded to the 39-byte corpus case; large-paste
  performance (bracketed-paste wrap, MB-scale) unmeasured.
- **Output-flood bound** measured at 1 MiB single-session; 20-session flood
  RAM behavior and scrollback-cap tuning left to Stage 05 spike follow-up.
- **tmux 3.4 present but unused** as oracle; cross-checking emulator
  rendering against tmux capture is deferred.
- **`x/vt` has no tagged release**: API drift risk until upstream tags;
  re-run this spike on any version bump.
- **Linux only**: no macOS/Windows PTY evidence; matches Linux-first strategy.

## 8. Proposed key-routing contract (not measured — Stage 08 acceptance hooks)

Status: **Proposed**. The spike never exercised shortcut handling, prefix
keys, or host-terminal mode changes (it uses only private PTYs and never
touches the outer terminal), so nothing below is spike evidence. It is
recorded here so Stage 08 has explicit acceptance hooks; do not claim the
spike measured shortcut UX.

- **Shortcut prefix**: `Ctrl+]` (plan default), then an action key
  (`s` = list, `n` = new, `?` = help, per the Stage 08 plan). In an
  attached agent view, every other key goes to the selected session's PTY.
- **Literal prefix forwarding**: pressing the prefix twice (`Ctrl+]`
  `Ctrl+]`) forwards a single `Ctrl+]` byte (`0x1D`) to the agent PTY, so
  agents that bind `Ctrl+]` themselves remain reachable. No other prefix
  escape is defined at this stage.
- **`Ctrl+C` scope**: `Ctrl+C` (`0x03`) in an attached session view is PTY
  input to the agent (ISIG scope belongs to the session's foreground
  process group — consistent with the measured §4.1 `Ctrl-C / ISIG` trap
  and ADR 0001), never an ASD shutdown command. Client-view `q` detaches;
  it never signals agent processes.
- **Terminal restore**: the host terminal must be restored on normal,
  error, and catchable-signal exits. The spike satisfies this vacuously
  (private PTYs only; `cleanup.sh` prints `cleanup: OK` and the rerun
  leaves no test child). The Stage 08 TUI must additionally save/restore
  termios, leave alt-screen if entered, and prove restoration with hooks:
  normal exit, error exit, and `SIGTERM`/`SIGHUP` catchable paths each
  return the host terminal to its pre-launch state.
