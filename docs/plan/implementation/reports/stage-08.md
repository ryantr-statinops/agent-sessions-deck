# Stage 08 — Interactive Terminal View

Status: **COMPLETE** · Wave: W2 · Dependencies: Stage 05, Stage 06, Stage 07.

## Implemented

- **Embedded Virtual Terminal View (`internal/tui/terminal/`, `internal/tui/`)**:
  - Implemented `ViewTerminal` mode inside Charm Bubbletea TUI, replacing Option A process suspension with true in-process terminal virtualization.
  - Multi-session concurrent operation: sessions run under owner runtime and can be switched dynamically while preserving independent screen buffers.

- **VT Input Encoder (`internal/tui/terminal/input.go`)**:
  - Full VT keyboard sequence translation: ASCII, control characters (`0x00`-`0x1F`), Backspace (`\x7f`), Tab (`\t`), Enter (`\r`), Esc (`\x1b`).
  - Navigation & editing keys: Up (`\x1b[A`), Down (`\x1b[B`), Right (`\x1b[C`), Left (`\x1b[D`), Home (`\x1b[H`), End (`\x1b[F`), Insert (`\x1b[2~`), Delete (`\x1b[3~`), PgUp (`\x1b[5~`), PgDn (`\x1b[6~`).
  - Application cursor key support (`\x1bOA`..`\x1bOD`).
  - Function keys: F1-F4 (`\x1bOP`..`\x1bOS`), F5-F12 (`\x1b[15~`..`\x1b[24~`).
  - Alt-modifier key combinations (`\x1b<char>` and extended `\x1b[1;3<X>`).
  - Multibyte UTF-8 input, CJK, Vietnamese diacritics, emojis, and bracketed paste mode (`\x1b[200~` ... `\x1b[201~`).
  - Direct pass-through for `Ctrl+C` (0x03) to child PTY, preventing accidental client shutdown during terminal interaction.

- **Prefix Interceptor State Machine (`internal/tui/terminal/prefix.go`)**:
  - Configurable prefix key: `Ctrl+]` (0x1D).
  - Actions dispatched:
    - `s` / `S`: Detach interactive session, release lease, and return cleanly to dashboard.
    - `n` / `N`: Detach and switch immediately to new session launcher.
    - `?`: Toggle contextual keyboard help overlay without interrupting child process.
    - `[`: Enter scrollback history navigation mode.
    - `Ctrl+]` (double prefix): Forward literal 0x1D byte to child PTY.

- **Screen Rendering & Run-Length Batching (`internal/tui/terminal/renderer.go`, `internal/tui/terminal/style.go`)**:
  - ANSI SGR styling converter translating `app.TerminalStyle` (bold, dim, italic, underline, reverse, RGB foreground/background) into minimal escape sequences.
  - Cell run-length batching merging adjacent cells with matching styles into contiguous spans to maximize redraw performance.
  - Virtual cursor rendering with configurable shapes (block, underline, bar) and blink states.

- **Bounded Scrollback Ring Buffer (`internal/tui/terminal/scrollback.go`)**:
  - Ring buffer holding up to 10,000 lines of scrollback history for normal screen mode.
  - Viewport navigation controls (`k`/`j`, `Up`/`Down`, `PgUp`/`PgDn`, `Esc`/`Enter` to return).
  - Automatic bypass when child process activates alternate screen buffer (Vim, Less, Claude Code full-screen).

- **PTY Handshake & Viewport Layout (`internal/tui/terminal/layout.go`)**:
  - Precise split allocating `Height - 1` rows to child PTY and reserving bottom row for contextual status chrome.
  - Responsive PTY window resizing on initial terminal entry and upon `SIGWINCH` window size change messages.
  - Asynchronous frame streaming via `TerminalFrameMsg` and non-blocking `waitForFrameCmd`.
  - Incremental delta application and complete snapshot resynchronization.
  - Frozen final screen state preservation upon child process exit or disconnect.

- **Host Terminal Cleanliness & Signal Traps (`internal/tui/cleanup_linux.go`)**:
  - Signal traps for `SIGINT`, `SIGTERM`, `SIGHUP` and panic recovery ensuring host terminal is cleanly restored (`\x1b[?1049l\x1b[?25h`) and termios restored.

- **Comprehensive Test Suite & Documentation**:
  - Unit tests for input encoder, prefix machine, styling, renderer, layout, and scrollback.
  - Integration tests: `tests/stage08_input_routing_test.go`, `tests/stage08_multi_session_test.go`, and `tests/stage08_flood_bound_test.go`.
  - Architecture and keybindings guide in `docs/terminal.md`.

## Verification

- `make check`:
  - `fmt-check`: OK.
  - `lint`: `go vet -tags=integration` OK.
  - `test`: All unit tests pass across all packages (100% pass).
  - `test-integration`: All integration tests pass including multi-session twin agents, flood output memory bounding, and PTY life cycles.
  - `smoke`: Build and CLI smoke test OK.

## Completion Boundary

Stage 08 acceptance checks and repository gates passed. Stage 09 (Recovery and lifecycle hardening) is the next planned stage.
