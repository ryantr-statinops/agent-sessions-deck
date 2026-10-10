# Providers — compatibility matrix

Trạng thái: IN EFFECT cho Stage 04 · Milestone: M1.

Guideline: a provider only appears in the **supported** list with an
executable-identity proof (bounded `--version` probe whose output contains
the vendor marker, see `internal/discovery` and `internal/providers/<name>`).
Everything else stays `unverified`/`uncertain` and is never presented as
available from its binary name.

## Matrix

| Provider ID | Executable identity | Version probe | Launch args (built-in default) | Terminal coverage | Native capabilities | Last verified | Status |
|---|---|---|---|---|---|---|---|
| `generic` | Explicit executable path supplied by the user; identity is the config, never the binary name. | None (no probing). | Configured `args` + literal `-- <argv...>` tail; workspace directory is the cwd. | Interactive PTY (core). | None beyond core launch/interactive. | 2026-10-07 | Supported (fallback). |
| `claude` | Marker `claude` (case-insensitive) in bounded `--version` output. Binaries named `claude` without the marker are `uncertain`. | `--version`, 2s timeout, 64KB cap, cached 30s. | Configured args + literal extra argv; no session/resume flags guessed. | Interactive PTY (core). | None claimed; session list/logs/resume/kill stay off. | 2026-10-07 | Adapter implemented, identity probe enforced; real-vendor smoke deferred to Stage 10. |
| `codex` | Marker `codex` in bounded `--version` output. | `--version`, 2s timeout, 64KB cap, cached 30s. | Configured args + literal extra argv; no vendor flags guessed. | Interactive PTY (core). | None claimed. | 2026-10-07 | Adapter implemented, identity probe enforced; real-vendor smoke deferred to Stage 10. |
| `opencode` | Marker `opencode` in bounded `--version` output. | `--version`, 2s timeout, 64KB cap, cached 30s. | Configured args + literal extra argv; no vendor flags guessed. | Interactive PTY (core). | None claimed. | 2026-10-07 | Adapter implemented, identity probe enforced; real-vendor smoke deferred to Stage 10. |

## Not in the supported list

| Provider ID | Reason |
|---|---|
| `omp` | Linux `omp` can resolve to the screen reader; identity is never inferred from the binary name and no verified coding-agent probe exists yet. Any future OMP coding-agent adapter must pass `Marker` verification and remain `uncertain` until then. |
| `orca` | Same rule: an `orca` executable is not assumed to be a coding agent without a verified marker probe. The screen-reader collision on Linux makes name-based identity explicitly forbidden. |
| `aider` | No adapter implemented in Stage 04; no identity evidence. |

## Detection result contract

Every probe returns one of `available` / `not-found` / `invalid` /
`uncertain` plus `reason` and capped `output` provenance (`internal/discovery`).
`available` requires a positive probe result; timeouts and exit-non-zero map
to `uncertain` and `invalid` respectively. Probe runs never allocate a TTY
and never read interactive stdin, so a binary that wants a terminal fails
fast instead of stalling the launcher.

## Notes on `--model` / receipts

Orca receipts may record `model: null`; the worker's effective default model
is `opencode/fledge-alpha-free` (reported 2026-10-07). Provider matrices
must not claim vendor capabilities from absence of evidence.
