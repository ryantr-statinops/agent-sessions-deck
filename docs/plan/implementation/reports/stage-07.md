# Stage 07 — TUI Launcher and Session Dashboard

Status: **COMPLETE** · Wave: W2 · Dependencies: Stage 06.

## Implemented

- Built complete Charm/Bubble Tea architecture package at `internal/tui/`:
  - `Model`, `Init`, `Update`, `View` adhering to The Elm Architecture.
  - Asynchronous command sequence fencing (`AsyncSeq`) guaranteeing stale background I/O never overwrites active UI state.
- Launcher component (`internal/tui/launcher.go`):
  - Agent picker dynamically populated from `client.Scan`.
  - Workspace directory input with recent workspace suggestions.
  - Inline validation and state preservation on launch failures.
- Session Dashboard and Inspector (`internal/tui/dashboard.go`, `internal/tui/detail.go`):
  - Live table rendering with truthful status symbols (`●` Running, `○` Starting/Stopping, `✕` Failed, `⬝` Exited).
  - Multi-field deterministic fuzzy search matching across name, agent, workspace, branch, lifecycle, and process PID.
  - Responsive split inspector pane (side-by-side on wide terminals, full-screen toggle on narrow terminals).
- Modal Confirmations (`internal/tui/confirm.go`):
  - Explicit modal dialogues for restart-running attempt, graceful stop (SIGTERM), and force kill (SIGKILL).
  - Contextual keymaps with full mode isolation between normal navigation and input typing.
  - Built-in keyboard shortcuts reference cheatsheet overlay (`?`).
- Distinct Ownership Quit Policy (`internal/tui/quit.go`):
  - Secondary client quits immediately with simple detach, leaving sessions alive under owner.
  - Foreground owner prompts with active session count and stop confirmation before process termination.
- CLI Integration and Handover (`internal/cli/tui_linux.go`, `internal/cli/root_linux.go`):
  - `asd` in interactive TTY bootstraps foreground owner if none is present, or connects as a client.
  - Option A interactive handover: `Enter` suspends Bubble Tea, enters real PTY raw stream via Stage 06 `attach`, and resumes TUI on `Ctrl+]`.
  - Non-TTY invocations retain headless owner mode.

## Verification

- `make check` — passed cleanly:
  - `fmt-check`: OK.
  - `lint`: `go vet -tags=integration` OK.
  - Unit tests: 100% pass across all packages, including comprehensive model, fuzzy search, styling, launcher, detail, confirmation, and quit suites in `internal/tui`.
  - Integration tests: 100% pass across all integration-tagged packages with fake agents, PTY processes, and IPC servers.
  - Built-binary smoke test: `bin/asd --help` and `bin/asd --version` OK.
- Documentation:
  - User guide and keyboard reference created in `docs/usage.md`.
  - Stage 07 checklist fully closed in `07-tui-launcher-and-dashboard.md`.

## Completion boundary

Stage 07 acceptance checks and repository gates passed. Stage 08 (Interactive virtual terminal view embedded in TUI) remains the next planned stage.
