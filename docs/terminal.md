# Interactive Terminal View (`ViewTerminal`)

`agent-sessions-deck` (`asd`) embeds an in-process terminal view within its Bubbletea TUI, allowing users to interact directly with running coding-agent processes (Claude Code, OpenAI Codex, OpenCode, Aider, shells, etc.) without leaving the application or spawning external multiplexers.

## Architecture

The terminal view is implemented as `ViewTerminal` within `internal/tui` and `internal/tui/terminal`:

```
+-------------------------------------------------------------+
| Bubbletea Host TUI                                          |
|                                                             |
| +---------------------------------------------------------+ |
| | Virtual Terminal Viewport (Width x Height - 1)          | |
| | - ANSI SGR style translation                            | |
| | - Cell run-length batching                              | |
| | - Virtual cursor (block, underline, bar)               | |
| | - Bounded scrollback ring buffer (up to 10,000 lines)   | |
| +---------------------------------------------------------+ |
| | Status Footer (1 line): session title, status, prefix   | |
+-------------------------------------------------------------+
               | PTY stream & input       ^ Frame stream
               v                          |
+-------------------------------------------------------------+
| Session Process / PTY (under Owner Runtime)                |
+-------------------------------------------------------------+
```

### Key Components

1. **Input Encoder (`terminal/input.go`)**:
   Translates Bubbletea `tea.KeyMsg` into standard VT100/Xterm escape sequences.
   - ASCII, Control codes (`Ctrl+A` through `Ctrl+Z`), Backspace, Enter (`\r`), Tab, Esc.
   - Navigation & editing keys: Arrows, Home, End, Insert, Delete, PgUp, PgDown (supporting both normal `\x1b[...` and application cursor `\x1bO...` modes).
   - Function keys: F1 through F12 (`\x1bOP`..`\x1bOS`, `\x1b[15~`..`\x1b[24~`).
   - Alt-modifier combinations: `\x1b<char>` or `\x1b[1;3<X>`.
   - Multibyte UTF-8 characters and bracketed paste wrapping (`\x1b[200~` ... `\x1b[201~`).

2. **Prefix Machine (`terminal/prefix.go`)**:
   Implements a modal prefix interceptor using `Ctrl+]` (ASCII 0x1D):
   - First `Ctrl+]` transitions to pending state.
   - Next keystroke dispatches action or cancels pending state.

3. **Screen Renderer & SGR Converter (`terminal/renderer.go`, `terminal/style.go`)**:
   - Converts `app.TerminalStyle` (bold, dim, italic, underline, reverse, RGB foreground/background) into minimal ANSI SGR sequences.
   - Renders row cells with run-length batching to optimize TUI redraw throughput.
   - Renders virtual cursor with configurable shape (block, underline, bar) and blink state.

4. **Scrollback Buffer (`terminal/scrollback.go`)**:
   - Ring buffer bounded up to 10,000 lines.
   - Preserves lines pushed off the top of the non-alternate screen buffer.
   - Automatically bypassed when child application uses alternate screen buffer (e.g. Vim, Less, Claude Code full-screen).

5. **PTY Viewport Handshake (`terminal/layout.go`)**:
   - Accurately splits window height: allocates `Height - 1` rows to child PTY and reserves 1 bottom row for status chrome.
   - Dynamically resizes child PTY upon terminal entry and on terminal window resize events (`SIGWINCH`).

6. **Host Cleanliness & Signal Traps (`cleanup_linux.go`)**:
   - Signal handling for `SIGINT`, `SIGTERM`, `SIGHUP` guarantees restoring host termios and exiting alternate screen buffer (`\x1b[?1049l\x1b[?25h`).
   - Panic recovery handler prevents terminal corruption in event of unexpected panics.

---

## Keybindings & Shortcuts

| Keybinding | Action | Description |
| :--- | :--- | :--- |
| `Ctrl+]` `s` | **Detach** | Release interactive lease and return to dashboard |
| `Ctrl+]` `n` | **New Session** | Release lease and switch directly to launcher view |
| `Ctrl+]` `?` | **Help Overlay** | Toggle interactive help modal over terminal |
| `Ctrl+]` `[` | **Scroll Mode** | Enter scrollback history navigation |
| `Ctrl+]` `Ctrl+]` | **Literal Byte** | Send raw `0x1D` (`Ctrl+]`) byte to child process |
| `Ctrl+C` | **Pass-through** | Forwarded to child PTY (`0x03`); does NOT quit `asd` |

### Scrollback Navigation (when scrolled)

| Keybinding | Action |
| :--- | :--- |
| `k` / `Up` | Scroll up 1 line |
| `j` / `Down` | Scroll down 1 line |
| `PgUp` | Scroll up 10 lines |
| `PgDn` | Scroll down 10 lines |
| `Esc` / `Enter` | Snap back to bottom and resume live tracking |

---

## Footer Status Chrome

The bottom line provides contextual status:
- **Normal Mode**: Displays session name, agent ID, and quick detach reminder (`Ctrl+] s`).
- **Prefix Pending Mode**: Displays golden banner indicating prefix is active: `^] prefix: [s] detach | [n] new | [?] help | [[] scroll | [^]] literal`.
- **History Scrolled Mode**: Displays scrollback offset indicator: `[SCROLLBACK: -N lines] (Esc to return)`.
- **Exited Child**: Displays notification indicating the child process terminated, while preserving the final screen output.
