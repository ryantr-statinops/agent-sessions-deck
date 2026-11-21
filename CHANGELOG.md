# Changelog

All notable changes to the Agent Sessions Deck (`asd`) project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [1.0.0] - 2026-11-22

### Added
- **Interactive Terminal View (`ViewTerminal`)**:
  - Full ANSI terminal emulation embedded directly into Bubble Tea TUI.
  - Interactive PTY streaming with zero external process suspension loops.
  - Prefix command state machine via `Ctrl+]`: `s` (detach to deck), `n` (launcher), `[` (scrollback browse), `?` (help), double `Ctrl+]` (literal byte `0x1D`).
  - Bounded 10,000-line scrollback ring buffer with alternate screen bypass.
  - Comprehensive key encoding: F1-F12, Alt/Meta modifiers, arrow keys, Home/End, multibyte UTF-8, and bracketed paste.
  - Host terminal raw-mode restore and panic recovery hooks.
- **Agent Discovery & Provider Support**:
  - Out-of-the-box discovery for `claude`, `codex`, and `opencode` with strict `--version` identity marker verification.
  - Defense against lookalike system utilities (e.g., `omp` media player, `orca` screen reader).
  - Flexible `generic` provider adapter for running custom coding agents and shell scripts.
- **Robust Recovery & Lifecycle Hardening**:
  - Startup reconciliation pass that freezes untracked surviving processes as `SESSION_IO_FAILED` to prevent PID reuse vulnerabilities.
  - Owner instance fencing token and safe automated cleanup of stale Unix sockets.
  - Structured OS signal traps (`SIGTERM`, `SIGHUP`) and bounded graceful termination with 5-second drain window.
  - Per-session mutation serialization ensuring monotonically increasing event revisions.
  - Bounded subscriber event buffer with drop-oldest policy and synthetic `resync` notifications.
  - Automatic secret redaction (`[REDACTED]`) in JSON diagnostic envelopes and TUI banners.
- **V1 CLI & Scriptable Contract**:
  - 10 core commands: `asd` (root/dashboard), `scan`, `list`, `new`, `open`, `inspect`, `rename`, `restart`, `stop`, `kill`.
  - Machine-readable `--json` envelopes for all query and mutation commands.
  - Safety confirmations `--force` and `--yes` for non-interactive destructive operations.
- **Packaging & Release**:
  - Automated release packaging script for `linux/amd64` and `linux/arm64`.
  - Checksum generation (`sha256sums.txt`) and GitHub Actions release workflow.

### Boundaries and Limitations (V1)
- **Single Foreground Owner**: ASD operates as a foreground owner process; when the owner terminates, child sessions are shut down gracefully. Background daemonization is scheduled for V2.
- **Process Restart vs Native Resume**: `restart` executes a new attempt in the session workspace; vendor-specific chat history continuation is not guessed.
- **Zero Vendor Credentials**: ASD does not store, request, or manage vendor API keys or tokens; authentication remains entirely inside the agent CLI's PTY.
