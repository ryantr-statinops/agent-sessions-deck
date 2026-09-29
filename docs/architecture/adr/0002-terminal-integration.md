# ADR 0002 — Terminal integration (PTY + emulator selection)

Status: **Proposed** (Stage 00 contract slice; starting point from spike
evidence only, not an approved product fact). Owner of this file's scope:
terminal-integration worker. This ADR changes nothing in ADR 0001, ADR 0003,
`cli-contract.md`, `session-state-machine.md`, `PRODUCT.md`, the Stage plan,
the spike, or production code — it only records what the spike proved and
what it did not.

## Context

ADR 0001 places every PTY handle under the single foreground owner (V1), with
client attach/detach semantics (`open` attaches; client exit detaches only)
and crash recovery that never recreates a PTY. The state machine
(`docs/architecture/session-state-machine.md`) makes
Attachment (`attached | detached | unavailable`) an axis separate from
Lifecycle, with `running + unavailable` annotated **orphaned**. ADR 0003
(`docs/architecture/adr/0003-state-and-storage.md`) forbids
persisting raw terminal bytes or secrets. The CLI contract
(`docs/architecture/cli-contract.md`) binds
`open` failure to `SESSION_IO_FAILED` and offline `list` to `stored`
authority. This ADR answers the one question ADR 0001 deferred: which PTY and
screen-emulation libraries the session runtime (Stage 05) and TUI (Stage 08)
prototype against — decided **only** from the rerunnable spike in
`experiments/terminal/` (run: `experiments/terminal/run.sh`, usage:
`experiments/terminal/README.md`), whose results matrix lives in
`docs/testing/terminal-compatibility.md`.

## Selection criteria

A candidate passes only if the spike demonstrates it on this host:

1. Spawn with controlled winsize, resize + SIGWINCH delivery, foreground
   process-group membership, byte-exact I/O, Ctrl-C/ISIG, and UTF-8 safety.
2. Multi-session behavior at two levels: (a) PTY byte-stream — a detached
   session keeps producing without losing its screen, sessions stay
   isolated, and flood-while-detached behavior is characterized (stall vs.
   loss); (b) emulator state — two independent full-screen session states
   coexist with per-session background drain, reattach rendering, input
   routing, and resize isolation (the `twin_*` probe).
3. Screen semantics (cursor addressing, SGR, erase, alt-screen, CJK render,
   OSC/paste/scroll-region consumption) plus query answering (DA/DSR/DECRQM),
   because bare PTY transport is transparent and answers nothing.
4. Bounded, pinned, rerunnable evidence: exact versions, exact import paths,
   per-probe JSON, and a one-command rerun. Unpinned or unevidenced claims
   do not count.

## Candidates considered (all spike-bounded)

| Candidate | Import path / identity | Spike version |
|---|---|---|
| PTY layer: `creack/pty` | `github.com/creack/pty` (`pty.StartWithSize`, `pty.Setsize`/`pty.Getsize`) | **v1.1.24** (tagged; pinned in `experiments/terminal/go/go.mod`) |
| Emulator: `charmbracelet/x/vt` | `github.com/charmbracelet/x/vt` (`vt.NewEmulator`, `CellAt`, `Render`, `IsAltScreen`, `CursorPosition`, `Read`/`Write`/`Close`) | **v0.0.0-20260927004216-9c77d672503d** (pseudo-version; **no tagged release exists**) |
| Tokenizer baseline: `charmbracelet/x/ansi` | `github.com/charmbracelet/x/ansi` + `github.com/charmbracelet/x/ansi/parser` (`ansi.NewParser`, `Advance` → `parser.DispatchAction`) | **v0.11.8** |
| Raw handoff (no emulator) | n/a (Python stdlib `pty`/`termios` probes) | Python 3.12.3, no packages installed |
| Bubble Tea viewport over ANSI strings | not imported in the spike | not evaluated as an emulator |
| Minimal hand-rolled screen model | not built | not evaluated |

Host baseline for every claim below: Linux 7.1.5-76070105-generic x86_64, Go
go1.26.7 linux/amd64, Python 3.12.3, tmux 3.4 (reference only, unused as
oracle), terminfo xterm-256color — see `experiments/terminal/evidence/versions.txt`.
Spike pins live only in `experiments/terminal/go/go.mod`.

## Decision (proposed, evidence-bound)

1. **PTY layer: `github.com/creack/pty` @ v1.1.24.** Accepted for Stage 05
   prototyping. Evidence: 8/8 PTY fundamentals green
   (`experiments/terminal/evidence/pty_basics.json`) plus Go parity green
   (`go_pty_spawn`, `go_pty_resize` 24×80 → 30×100 via `Getsize`,
   `go_pty_unicode`) in `experiments/terminal/evidence/go_spike.json`.
   Constraints carried forward: slaves run in `raw -echo` mode (cooked mode
   translates `\n` → `\r\n`, corpus probe `cooked_nl`); winsize set at spawn
   and on resize; foreground membership managed from inside owned sessions
   (outer-terminal `tcsetpgrp` from a detached process fails with ENOTTY —
   documented in `foreground_pgrp`, never relied upon).
2. **Per-session background drain plus one emulator per session is
   mandatory.** The two-session PTY probe
   (`experiments/terminal/evidence/two_sessions.json`, 4/4) shows a detached
   session advancing ticks 6 → 29 while undrained, contiguous re-attach
   (ticks 6 → 38/39, no loss at this volume), B advancing 12/13 → 29
   independently — but a 2 s undrained flood stalls the writer (max inter-tick
   gap 31–36 ms attached vs. ~1839–1873 ms undrained against the ~64 KiB PTY
   buffer). The emulator-level twin probe (`twin_*` 7/7 in
   `experiments/terminal/evidence/go_spike.json`) closes the remaining gap:
   two independent PTYs each drain continuously into their own
   `vt.SafeEmulator` (single writer per emulator); detached A advanced
   frames 0004 → 0054 while only B was viewed, reattached A rendered frame
   0055 == drain-max 0055 with a contiguous 56-frame sequence and no B
   markers (and converse), input tokens reached only the selected session
   in both directions, and resizing B (PTY 30×100, emulator 100×30,
   `WINCH-B` only in B) left A at 24×80 / 80×24. Raw handoff that reads
   only the viewed session is therefore **rejected as the multi-session
   solution**. Stage 05 must drain every session's PTY in the background
   with bounded scrollback caps and keep one single-writer emulator per
   session.
3. **Screen layer: `github.com/charmbracelet/x/vt` @
   v0.0.0-20260927004216-9c77d672503d — viable with reservations, not final.**
   Evidence: full corpus green through the emulator (cursor cells X/Z/U/D at
   exact positions, SGR styling, ED/EL erase, alt-screen enter/exit via
   `IsAltScreen`, CJK in `Render()`, OSC/paste/scroll/split consumption),
   two-session fullscreen green (`twin_*` 7/7: per-session emulation,
   reattach-latest, no contamination, input/resize isolation) and
   query answering (`experiments/terminal/fixtures/corpus/queries.ansi` 16/16 bytes consumed; reply
   `\x1b[?62;1;6;22c\x1b[1;1R\x1b[?2004;2$y` — VT220-class DA, cursor report,
   DECRQM) in `experiments/terminal/evidence/go_spike.json` (32/32 blocking
   green). Bare PTY answers zero reply bytes beyond echo
   (`experiments/terminal/evidence/query_response.json`, 4/4), and
   alt-screen transport is byte-identical but semantically inert without an
   emulator (`experiments/terminal/evidence/altscreen.json`, 6/6; corpus
   transport 10/10 in `experiments/terminal/evidence/corpus.json`). The
   emulator layer — not the line discipline — owns echo, ISIG scope, and
   query answers.
4. **`github.com/charmbracelet/x/ansi` @ v0.11.8 is insufficient alone.**
   It tokenizes (0–10 dispatches per fixture, per-fixture `ansi_parse_*`
   checks green) but keeps no screen/cursor/alt-screen state — parser ≠
   emulator. Kept only as a tokenizer baseline, not selected as the screen
   layer.
5. **Not selected on this evidence:** Bubble Tea viewport (viewport ≠
   terminal; no executable emulator claim made in the spike) and a hand-rolled
   screen model (would need its own corpus + fuzzing to match `x/vt`
   coverage; no cost paid yet).

## Compatibility limits (what the evidence does and does not cover)

- Transport corpus is 10 bounded fixtures, each under 120 bytes (total under
  1 KiB) plus a cooked-mode `a\nb\n` case, replayed through a bare `cat`
  behind a `raw -echo` slave; 1-byte-chunked multibyte writes pass, but this
  is transport fidelity, not agent compatibility. Fixtures:
  `experiments/terminal/fixtures/corpus/*.ansi`.
- Two-session fullscreen (`twin_*`) uses deterministic Python fake-fullscreen
  children (inline `python3 -u -c`, ~20 Hz CUP+EL header, stdin echo,
  SIGWINCH trap — no agent CLIs, no auth, no cost) at a single volume point
  (~2.3 KiB per session over the detached window, 56-frame contiguous run).
  It proves two independent emulator states with per-session drain, input
  routing, and resize isolation — not real-agent compatibility, not
  alt-screen apps, not flood-scale multi-session RAM. Real-agent behavior
  remains unverified (see gaps).
- Output-flood bound measured at 1 MiB single-session (~0.01–0.03 s across runs;
  latest run: 0.03 s, ~36,683 KiB/s). 20-session flood RAM behavior and
  scrollback-cap tuning are left to a Stage 05 follow-up.
- One-run performance baseline in `go_spike.json`: max of 100 80×24 `Render()`
  calls 153.445 µs; fake-child input echo 1.390 ms / 1.577 ms; resize-to-WINCH
  2.259 ms. Latencies use 1 ms polling; no thresholds or production SLOs.
  Re-measure under Stage 05 scale/flood workloads.
- The exact `x/vt` reply bytes above (`?62…`) are what this revision emits;
  whether real agents accept that identity is **unvalidated** (see gaps).
- `x/vt` has no tagged release: API drift risk until upstream tags. Any
  version bump requires re-running the spike and re-recording the reply
  bytes; the pseudo-version pin lives only in
  `experiments/terminal/go/go.mod` and pins nothing project-wide (the
  repository root has no `go.mod`).
- Linux only. No macOS/Windows PTY evidence; matches the Linux-first strategy
  in ADR 0001/0003 assumptions.
- tmux 3.4 was present but unused as a rendering oracle; cross-checking
  emulator rendering against tmux capture is deferred.

## Failed and unverified cases (recorded, not asserted)

- `gap_vt_sco_save_restore` (non-blocking): SCO save/restore (`ESC s` /
  `ESC u`) is **not implemented** in this `x/vt` revision — the probe
  FAILs as designed and does not fail the run (32/32 blocking pass + 1
  documented gap). Portable path DECSC/DECRC (`ESC 7` / `ESC 8`) works
  (`vt_decsc_restore` PASS). Stage 05/08 must add a regression test for the
  SCO gap and use DECSC/DECRC where agents allow it.
- **No interactive agent CLI launched**: opencode 1.18.33, omp 18.4.2, orca
  1.4.215 probed with `--version` only (auth/cost rule). The `twin_*`
  two-session fullscreen proof uses fake children, not real agents, so it
  makes no production compatibility claim. A smoke matrix against real
  agent CLIs is required before Stage 10; absence of
  `claude`/`codex`/`aider` binaries on this host is already a coverage hole.
- **Key-routing UX unmeasured**: the shortcut-prefix / literal-forwarding /
  `Ctrl+C`-scope / terminal-restore contract below is Proposed only — the
  spike exercises no shortcut handling and never touches the outer
  terminal. Stage 08 acceptance hooks are listed there, not claimed here.
- Background-read SIGTTIN stop not demonstrated (detached runner without its
  own controlling terminal cannot `tcsetpgrp` a foreign PTY — ENOTTY); fg
  membership proven from inside instead.
- Paste throughput bounded to the 39-byte corpus case; MB-scale
  bracketed-paste performance unmeasured.
- `x/vt` untagged-API drift, 20-session flood RAM, tmux-oracle cross-check,
  and non-Linux platforms all unverified (see limits above).

## Evidence map and rerun

| Probe | Evidence file | Result on this host |
|---|---|---|
| PTY fundamentals | `experiments/terminal/evidence/pty_basics.json` | 8/8 PASS |
| Query/response transparency | `experiments/terminal/evidence/query_response.json` | 4/4 PASS (zero reply bytes) |
| Alt-screen transport | `experiments/terminal/evidence/altscreen.json` | 6/6 PASS |
| Two sessions | `experiments/terminal/evidence/two_sessions.json` | 4/4 PASS |
| Corpus transport | `experiments/terminal/evidence/corpus.json` | 10/10 PASS |
| Go fidelity (`creack/pty` + `x/vt` + `x/ansi` + twin two-session) | `experiments/terminal/evidence/go_spike.json` | 32/32 blocking PASS + 1 documented gap |
| Host/versions | `experiments/terminal/evidence/versions.txt` | reproduced 2026-09-29 (see below) |

Full matrix, recommendation, and gaps: `docs/testing/terminal-compatibility.md`
(§§4–8). Spike usage and safety bounds: `experiments/terminal/README.md`.
Spike pins: `experiments/terminal/go/go.mod` (spike-only module).
Exact rerun (from repository root), verified this task:

```bash
timeout 300 bash experiments/terminal/run.sh            # full spike
timeout 300 bash experiments/terminal/run.sh --skip-go  # PTY probes only, no `go run`
bash experiments/terminal/cleanup.sh                    # verify: prints `cleanup: OK`
```

Last full pass for this ADR: 2026-09-29 — `SPIKE RESULT: ALL PROBES PASSED`
(32/32 blocking + 1 documented gap), versions identical to the table above
(`experiments/terminal/evidence/versions.txt` regenerated: Linux 7.1.5-76070105-generic x86_64,
go1.26.7, Python 3.12.3, tmux 3.4, opencode 1.18.33, omp 18.4.2, orca
1.4.215).

## Proposed key-routing contract (unmeasured; Stage 08 acceptance hooks)

Status: **Proposed**. The spike measures PTY transport, emulator
semantics, and per-session routing — never shortcut handling or
host-terminal mode changes (private PTYs only) — so this contract is a
decision placeholder with explicit hooks, not evidence.

- **Shortcut prefix**: `Ctrl+]` (plan default), then an action key
  (`s` = list, `n` = new, `?` = help, per the Stage 08 plan). In an
  attached agent view every other key goes to the selected session's PTY.
- **Literal prefix forwarding**: `Ctrl+]` `Ctrl+]` (prefix pressed twice)
  forwards one `Ctrl+]` byte (`0x1D`) to the agent PTY, keeping agents
  that bind `Ctrl+]` reachable. No other prefix escape is defined.
- **`Ctrl+C` scope**: `Ctrl+C` (`0x03`) in an attached session view is PTY
  input to the agent — ISIG scope belongs to the session's foreground
  process group (consistent with the measured `Ctrl-C / ISIG` trap in
  `pty_basics.json` and ADR 0001) — never an ASD shutdown command.
- **Terminal restore**: the host terminal must be restored on normal,
  error, and catchable-signal exits. The spike holds this vacuously
  (private PTYs only; `cleanup.sh` → `cleanup: OK`, no surviving test
  child). The Stage 08 TUI must save/restore termios, leave alt-screen if
  entered, and prove it with hooks for normal exit, error exit, and
  `SIGTERM`/`SIGHUP` catchable paths.

## Consequences for Stage 05 / Stage 08

1. Prototype the session runtime on `creack/pty` v1.1.24 with per-session
   background drain threads and bounded scrollback caps; run slaves
   `raw -echo`.
2. Prototype the screen layer on the tested `x/vt` pseudo-version or newer
   **only** with regression tests for the SCO gap and for the exact
   DA/DSR/DECRQM reply bytes real agents expect; re-run this spike on any
   bump and re-record §"Compatibility limits". Keep one single-writer
   emulator per session (only its drain writes; renders/resizes serialize
   through the same guard) and route input plus resize per session, as the
   `twin_*` probe demonstrates.
3. Never rely on outer-terminal `tcsetpgrp` from a detached process; manage
   foreground membership from inside owned sessions.
4. Implement the §"Proposed key-routing contract" (`Ctrl+]` prefix,
   double-prefix literal forwarding, `Ctrl+C`-as-agent-input, terminal
   restore) in the Stage 08 TUI with its three restore hooks; shortcut UX
   is unmeasured until then.
5. The agent-CLI smoke matrix (§"Failed and unverified cases") gates any
   production compatibility claim at Stage 10 — this ADR makes none.

## Alternatives considered

- **Raw handoff (no emulator).** Rejected for multi-session use: two-session
  stall/screen-loss (§"Decision" item 2) plus zero query answers.
- **`x/ansi` parser as the screen layer.** Rejected: tokenizes, keeps no
  screen state.
- **Bubble Tea viewport as the terminal.** Not selected: architecture
  decision stands on product grounds (viewport ≠ terminal); no spike evidence
  either way.
- **Hand-rolled screen model now.** Rejected for now: unevidenced cost
  against already-green `x/vt` coverage; revisit only with its own corpus +
  fuzzing.
- **Leaving the decision fully open.** Rejected as unnecessary: the PTY
  choice is firm on tagged, reproduced evidence, and the emulator choice is
  recorded as conditional with explicit re-validation gates rather than as a
  production guarantee.

## Open items (require named decider + new evidence before Stage 10)

1. Real-agent smoke matrix (opencode/omp/orca plus absent `claude`/`codex`/
   `aider`): which agents accept the `x/vt` `?62…` identity and DECSC-only
   save/restore. The `twin_*` fake-fullscreen proof does not cover this.
2. `x/vt` version re-pin once upstream tags a release; re-run spike, confirm
   SCO behavior and reply bytes.
3. Scrollback-cap and multi-session flood tuning from Stage 05 measurements.
4. Non-Linux evidence if the Linux-first scope ever lifts.
5. Stage 08 key-routing acceptance (§"Proposed key-routing contract"):
   prefix dispatch, double-prefix literal `0x1D` delivery, `Ctrl+C` reaching
   the agent instead of the app, and host-terminal restoration on normal /
   error / catchable-signal exits.
