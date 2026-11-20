# Agent Session Deck — TUI User Guide

`asd` is a terminal user interface (TUI) and CLI tool for managing local AI coding agent sessions.

---

## 1. Launching the TUI

Simply run `asd` without any subcommands in an interactive terminal:

```bash
asd
```

- If no background owner is currently running, `asd` will automatically bootstrap as the **Foreground Owner**.
- If an owner is already active, `asd` connects as an interactive **Client**.

---

## 2. Views and Navigation

The TUI features four primary views:

### 2.1 Session Dashboard (Default)

The dashboard provides a live overview of managed sessions:
- **Header**: Shows title, client/owner mode, and active search filter count.
- **Search Bar**: Press `/` to enter multi-field fuzzy search.
- **Table**:
  - `STATUS`: Visual indicators (`●` Running, `○` Starting/Stopping, `✕` Failed, `⬝` Exited).
  - `AGENT`: Coding agent identifier (`claude`, `codex`, `opencode`, `generic`).
  - `WORKSPACE`: Working directory path (compacted with `~` and `...` when narrow).
  - `NAME`: User-assigned session name.
  - `ATTEMPT`: Process PID or attempt generation number.
- **Footer**: Key shortcut hints.

### 2.2 Session Inspector (Detail Pane)

Press `d` or `Tab` to toggle the inspector pane:
- Displays full session ID, full unclipped workspace directory, Git branch and repository information.
- Shows live process PID, PGID, start ticks, and reap exit codes.
- On wide terminals (>= 100 columns), the inspector splits side-by-side with the session table. On narrow terminals, it opens full-screen.

### 2.3 Launcher (New Session Form)

Press `n` from the dashboard to open the launcher form:
1. **Agent**: Choose an available coding agent using `↑` and `↓`.
2. **Workspace**: Enter a target workspace path (pre-filled with recent workspaces).
3. **Session Name**: Optionally assign a descriptive session name.
- Press `Tab` or `Shift+Tab` to move between form fields.
- Press `Enter` to validate and launch the session.
- Press `Esc` to cancel and return to the dashboard.

### 2.4 Interactive Virtual Terminal (`ViewTerminal`)

Press `Enter` on any live session to enter the embedded interactive terminal:
- Full VT terminal emulator supporting colors, cursor navigation, and full-screen programs (Vim, Less, Claude Code).
- `Ctrl+C` passes directly through to the child PTY (does not exit the TUI).
- **Prefix Escape Sequences (`Ctrl+]` or `0x1D`)**:
  - `Ctrl+] s`: Detach from interactive session, release lease, and return to the dashboard.
  - `Ctrl+] n`: Switch immediately to the new session launcher.
  - `Ctrl+] [`: Enter scrollback history mode.
  - `Ctrl+] ?`: Display terminal contextual help overlay.
  - `Ctrl+] Ctrl+]`: Send a literal `0x1D` byte to the child process.

### 2.5 Scrollback History Navigation

When in terminal scrollback mode (`Ctrl+] [`):
- `↑` / `k`: Scroll up one line.
- `↓` / `j`: Scroll down one line.
- `PgUp` / `b`: Scroll up one page.
- `PgDn` / `f`: Scroll down one page.
- `Esc` or `Enter`: Return to live terminal streaming at current position.

### 2.6 Keyboard Shortcuts Cheatsheet

Press `?` from the dashboard or `Ctrl+] ?` from a terminal to open the shortcut cheatsheet.

---

## 3. Keyboard Shortcuts Reference

| Key | Context | Action |
|---|---|---|
| `↑` / `k`, `↓` / `j` | Dashboard | Move cursor up / down |
| `PgUp` / `b`, `PgDn` / `f` | Dashboard | Scroll one page up / down |
| `Home` / `g`, `End` / `G` | Dashboard | Jump to first / last session |
| `Enter` | Dashboard | Attach to the selected live session (embedded VT stream) |
| `n` | Dashboard | Open New Session launcher form |
| `/` | Dashboard | Activate multi-field fuzzy search bar |
| `d` | Dashboard | Toggle session inspector detail pane |
| `r` | Dashboard | Restart current attempt (prompts if running) |
| `x` / `s` | Dashboard | Graceful stop (sends SIGTERM with grace period) |
| `K` | Dashboard | Force kill (sends SIGKILL to process group) |
| `Ctrl+R` | Dashboard | Force refresh session list from server |
| `?` | Global | Toggle keyboard shortcuts cheatsheet overlay |
| `q` / `Ctrl+C` | Global | Quit TUI (detaches client, confirms with owner) |
| `Ctrl+] s` | Terminal | Detach from active session and return to dashboard |
| `Ctrl+] n` | Terminal | Detach and open launcher form |
| `Ctrl+] [` | Terminal | Enter scrollback buffer navigation |
| `Ctrl+] ?` | Terminal | Toggle terminal help overlay |
| `Ctrl+] Ctrl+]` | Terminal | Send literal `0x1D` (Ctrl+]) to child process |
