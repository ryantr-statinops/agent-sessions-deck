# Agent Session Deck — Product & Engineering Specification

> A CLI app with a TUI for launching and interacting with coding-agent sessions.

**Status:** Draft  
**Product:** Agent Session Deck  
**Command:** `asd`  
**Primary language:** Go  
**Target platform:** Linux first  
**Interface:** CLI app with TUI  
**License:** MIT (see `LICENSE`), selected by the maintainer.

---

## 1. Product Summary

Agent Session Deck (`asd`) is a local CLI app with a TUI for selecting an installed coding-agent CLI, choosing a workspace, running the command, and interacting with the session it starts. ASD owns the lifecycle and terminal interaction of sessions it launches. It does not aim to discover or reconnect to arbitrary sessions started elsewhere.

The user runs the CLI command:

```bash
asd
```

and gets one control surface:

```text
┌─ AGENT SESSION DECK ──────────────────────────────────────────────────────┐
│ Choose an agent and workspace                                  │
├─────────────────────────────────────────────────────────────────┤
│ AGENT       WORKSPACE          COMMAND / SESSION                 │
│ OpenCode    ~/Kestrel          opencode                          │
│ Claude      ~/observability    claude                            │
│ Codex       ~/Cluster          codex                             │
├─────────────────────────────────────────────────────────────────┤
│ ↑↓ Navigate  Enter Choose  q Quit                              │
└─────────────────────────────────────────────────────────────────┘
```

The product treats **agents, launched sessions, processes, workspaces, and repositories as separate entities**. A session is a command started and owned by ASD; terminal input and output are part of that live session.

The long-term goal is to provide a local control plane that can later be consumed not only by humans through the TUI, but also by AI systems through MCP.

---

# 2. Problem

Modern coding workflows increasingly involve multiple agent CLIs:

- Claude Code
- Codex CLI
- OpenCode
- OMP
- Orca
- Aider
- other local or custom agents

A developer may want to run several coding agents across different projects without repeatedly remembering commands or manually setting up each interactive session.

Typical workflow:

```bash
cd ~/Kestrel
opencode
```

The problem is not that any individual tool lacks session management.

The problem is **fragmentation**.

The developer has to remember:

- which agent command to run;
- which project directory to run it in;
- how to start a session with the right context;
- how to see and interact with the session just started by ASD.

ASD provides a unified local abstraction over these resources.

---

# 3. Product Vision

> **Choose a coding agent and workspace, run it, and work in the session from one interface.**

ASD should feel like a **coding-agent launcher with an interactive session interface**, specialized for local developer workflows.

It is not intended to replace:

- shell and terminal tools
- coding agents
- Git
- IDEs

Instead, it sits above them as a coordination layer.

```text
             Developer
                  │
                  ▼
             ASD TUI
                  │
            ASD Core
                  │
      ┌───────────┼────────────┐
      ▼           ▼            ▼
   Agents      Sessions     Workspaces
      │           │            │
 Claude/Codex   PTY/process    Git
 OpenCode/etc.  lifecycle      repositories
```

---

# 4. Design Principles

## 4.1 Local-first

ASD operates on the local machine.

No account is required.

No cloud service is required.

No remote server is required for the core product.

---

## 4.2 Discover before configure

The user should not have to manually register every agent.

If an agent CLI is installed, ASD should attempt to detect it.

```text
Installed binary
       ↓
Agent discovery
       ↓
Provider identification
       ↓
Available for launch
```

---

## 4.3 One abstraction, many implementations

ASD should normalize different agent CLIs behind a provider interface.

The user interacts with:

```text
Agent
Session
Workspace
Process
```

rather than agent-specific implementation details.

---

## 4.4 Run the real underlying tools

ASD launches the real agent CLI as a child process and provides its interactive input/output. It is not a replacement coding runtime and does not emulate agent behavior.

---

## 4.5 Explicit destructive actions

Stopping a session must clearly indicate that the underlying agent process will be terminated. Closing the UI must have a defined behavior and must not silently orphan or kill a process.

ASD must not silently kill a user's coding process.

---

## 4.6 Provider independence

ASD should not be tightly coupled to one agent vendor.

Each provider implements a common interface.

---

# 5. Terminology

## Agent

A coding-agent implementation.

Examples:

```text
Claude
Codex
OpenCode
OMP
Orca
Aider
```

An Agent represents a command that ASD can launch, not a running instance.

---

## Session

A running or historical instance of an agent command launched by ASD.

Example:

```text
OpenCode session
Workspace: ~/Kestrel
Status: RUNNING
```

---

## Process

The operating-system process associated with a session.

Example:

```text
PID: 18231
Executable: opencode
CWD: /home/user/Kestrel
```

---

## Attempt

One execution of a Session's resolved command in its workspace. Each attempt has a generation number scoped to its session, an immutable resolved command, timestamps, an exit reason, and observed process identity. A restart keeps the Session ID and creates a new attempt; concurrent attempts of the same session are forbidden.

---

## Process identity

The evidence that a live OS process is the one ASD started for a given attempt: PID plus process-group ID, process start time, boot ID, and the owner instance ID. A PID alone never proves ownership.

---

The normative definitions for Session, Attempt, and Process identity — including the mapping of legacy status labels — live in `docs/architecture/glossary.md` and `docs/architecture/session-state-machine.md`.

---

## Workspace

The directory in which an agent is operating.

A workspace may be a Git repository.

---

## Repository

A Git repository associated with a workspace.

ASD may expose:

- repository root;
- branch;
- dirty state;
- changed-file count.

Git information is metadata, not a replacement for Git.

---

# 6. Primary User Experience

The primary CLI command is:

```bash
asd
```

This launches the TUI.

The first screen should let the user choose an installed agent or configured command, choose a workspace, and run it. ASD then switches directly to the interactive session it started. A session list can be opened from there to select another ASD-managed session.

Example:

```text
╭─ AGENT SESSION DECK ──────────────────────────────────────────────────────╮
│ 5 agent commands available · 2 active sessions                 │
├─────────────────────────────────────────────────────────────────┤
│ OpenCode   ~/Kestrel          RUNNING                             │
│ Claude     ~/observability    RUNNING                             │
│ Codex      ~/Cluster          EXITED                              │
├─────────────────────────────────────────────────────────────────┤
│ Enter run · / search · n new · sessions · q quit                 │
╰─────────────────────────────────────────────────────────────────╯
```

---

# 7. Discovery System

Discovery is one of the core features.

ASD discovers available commands and workspaces, then tracks only the sessions it launches.

## 7.1 Binary discovery

Determine whether known agent executables exist.

Examples:

```text
claude
codex
opencode
omp
orca
aider
```

Go implementation may initially use:

```go
exec.LookPath(...)
```

The system should also support configured additional paths.

Example:

```text
~/.local/bin
~/.npm/bin
~/.cargo/bin
/usr/local/bin
```

ASD should avoid expensive full-filesystem scans.

---

## 7.2 Session process tracking

ASD tracks the processes it starts. It does not claim ownership of arbitrary matching processes found elsewhere.

Relevant metadata:

```text
PID
PPID
command
executable
working directory
start time
TTY
PTY and process group
```

Example:

```text
PID      18231
Agent    opencode
CWD      /home/user/Kestrel
PTY      managed by ASD
Status   running
```

---

## 7.3 Interactive session handling

ASD starts the selected command in an interactive pseudo-terminal (PTY), connects user input and output to that PTY, and tracks the process it owns. V1 does not discover or reconnect to sessions started outside ASD.

---

# 8. Provider Architecture

ASD uses an adapter/provider model.

Conceptual interface:

```go
type AgentProvider interface {
    Name() string
    Detect() Detection
    Version() (string, error)
    BuildCommand(opts LaunchOptions) (CommandSpec, error)
}
```

Providers may also expose optional capabilities:

```go
type Capabilities struct {
    Launch       bool
    Interactive  bool
    SessionList  bool
    SessionLogs  bool
    Resume       bool
    Kill         bool
}
```

This allows ASD to gracefully handle agents with different APIs.

For example:

```text
OpenCode
├── Detect       ✓
├── Launch       ✓
├── Interactive ✓
└── Resume       -

Generic CLI
├── Detect       ✓
├── Launch       ✓
├── Interactive ✓
├── Sessions     -
└── Resume       -
```

The product must never assume every agent exposes the same session model. For the core flow, each provider resolves a command and arguments; the managed PTY provides the interactive session even when the agent has no session API.

---

# 9. Provider Directory

Initial provider structure:

```text
internal/
└── providers/
    ├── claude/
    ├── codex/
    ├── opencode/
    ├── omp/
    ├── orca/
    ├── aider/
    └── generic/
```

A generic provider should support arbitrary commands configured by the user. ASD starts the configured command directly and supplies the selected workspace as its working directory.

Example:

```yaml
agents:
  - name: my-asd
    command: my-asd
```

This prevents the product from being limited to a fixed list of supported tools.

---

# 10. Session Model

Example Go model:

```go
type Session struct {
    ID          string
    AgentID     string
    Name        string
    Status      Status

    PID         int
    WorkspaceID string
    PTYID       string
    Command     []string

    CreatedAt   time.Time
    LastSeenAt  time.Time
}
```

Session state is a triple of three orthogonal axes — Lifecycle (process truth: `created/starting/running/stopping/exited/failed/unknown`), Attachment (I/O truth: `attached/detached/unavailable`), and Activity (provider-evidenced hint: `unknown/working/idle`, never inferred from silence). `ORPHANED` is an annotation (`running` + I/O `unavailable`), not a lifecycle state. The normative transition tables live in `docs/architecture/session-state-machine.md`; the Go sketch below is illustrative only and must not be treated as the state enum:

```go
// Illustrative only; see docs/architecture/session-state-machine.md.
type Status string // e.g. "running", "exited", "failed", "unknown"
```

The state model (each axis) should be extensible in a backward-compatible way.

---

# 11. Workspace Model

```go
type Workspace struct {
    ID       string
    Path     string
    GitRoot  string
    Branch   string
    Dirty    bool
}
```

ASD should automatically attempt to identify a Git repository.

Example:

```text
~/Kestrel
├── Git root: ~/Kestrel
├── Branch: feature/executor
└── Dirty: 3 files
```

This allows users to identify sessions by project context rather than only by process ID.

---

# 12. Session Lifecycle

Legacy illustrative diagram — display labels only, not the normative state model:

```text
                ┌───────────┐
                │  CREATED  │
                └─────┬─────┘
                      │
                      ▼
                ┌───────────┐
                │  RUNNING  │
                └─────┬─────┘
                  ┌───┴────┐
                  ▼        ▼
               IDLE      DEAD
                  │
                  └───► RUNNING
```

> Legacy illustrative diagram: `IDLE` is not a lifecycle state (idleness is Activity, provider-evidenced only) and `DEAD` is display shorthand for lifecycle `exited`/`failed` distinguished by exit reason. Normative contract: the binding lifecycle/attachment/activity triple and transition table live in `docs/architecture/session-state-machine.md`.

The lifecycle must distinguish:

### Detach

User leaves the interactive view.

The process continues while ASD remains active.

### Stop

The underlying process is terminated gracefully.

### Kill

Underlying process is forcibly terminated when necessary.

### Delete metadata

ASD removes its own stored metadata. By default, metadata cannot be deleted while its process is still running.

These operations must not be conflated.

---

# 13. CLI

The CLI provides scriptable access to core operations. Session IDs are stable strings (numeric examples below are shorthand); ambiguous ID prefixes are rejected, never guessed. Flags, error codes, and exit codes are frozen in `docs/architecture/cli-contract.md`.

## Launch TUI

```bash
asd
```

## Scan

```bash
asd scan
```

Output:

```text
Agents detected:

✓ claude     ~/.local/bin/claude
✓ codex      ~/.local/bin/codex
✓ opencode   ~/.local/bin/opencode
✓ omp        ~/.local/bin/omp
✓ orca       ~/.local/bin/orca
✗ aider      not found
```

## List

```bash
asd list
```

Optional:

```bash
asd list --agent opencode
asd list --status running
asd list --workspace ~/Kestrel
```

## Start

```bash
asd new
```

or:

```bash
asd new opencode ~/Kestrel
```

## Open session

```bash
asd open 42
```

## Rename

```bash
asd rename 42 kestrel-executor
```

## Restart

```bash
asd restart 42
```

## Stop

```bash
asd stop 42
```

## Kill

```bash
asd kill 42
```

## Inspect

```bash
asd inspect 42
```

Example:

```text
Session:       42
Agent:         OpenCode
PID:           18231
Status:        RUNNING

Workspace:     ~/Kestrel
Git branch:    feature/executor
Git state:     3 modified files

Session I/O:   managed PTY
Created:       22:31
Last seen:     22:48
```

---

# 14. TUI

The TUI is the primary interaction layer.

Recommended Go ecosystem:

- Bubble Tea
- Lip Gloss
- Bubbles

The exact libraries may change, but the architecture should keep the UI layer independent from discovery and process management.

---

## 14.1 Main view

```text
╭─ AGENT SESSION DECK ──────────────────────────────────────────────────────╮
│ Search:                                                         │
├─────────────────────────────────────────────────────────────────┤
│ STATUS AGENT       WORKSPACE             COMMAND / SESSION      │
│ ●      OpenCode    ~/Kestrel             opencode                │
│ ●      Claude      ~/observability       claude                  │
│ ○      Codex       ~/Cluster             codex                   │
│ ✕      Orca        sandbox         ~/sandbox             2h     │
├─────────────────────────────────────────────────────────────────┤
│ Enter Run  / Search  n New  x Stop  q Quit                     │
╰─────────────────────────────────────────────────────────────────╯
```

---

# 15. Fuzzy Search

Search should operate across multiple fields:

```text
session name
agent name
workspace path
repository name
branch
status
PID
```

Example:

```text
/kestrel
```

may return:

```text
42 OpenCode  executor       ~/Kestrel
19 Claude    experiments    ~/Kestrel-tests
```

Searching:

```text
claude
```

returns all Claude sessions.

Searching:

```text
running
```

filters active sessions.

A fuzzy matcher is preferred over exact string matching.

---

# 16. Interactive Session Workflow

The most important interaction:

```text
ASD
 ↓
Select agent and workspace
 ↓
Start command in a managed PTY
 ↓
Interact with that session
```

The selected agent runs as a real process in a PTY managed by ASD. ASD forwards keystrokes and renders output. Returning to the session list and selecting a live session resumes interaction with that managed PTY. Sessions launched outside ASD are out of scope for V1.

---

# 17. Create Session Workflow

Press:

```text
n
```

Example:

```text
╭─ NEW SESSION ─────────────────────────────╮
│ Agent                                      │
│ > OpenCode                                 │
│   Claude                                   │
│   Codex                                    │
│   OMP                                      │
│   Orca                                     │
│                                            │
│ Workspace                                  │
│ > ~/Kestrel                                │
│                                            │
│ Session name                               │
│ > executor                                 │
│                                            │
│ [Enter] Run       [Esc] Cancel             │
╰────────────────────────────────────────────╯
```

ASD then:

1. validates the agent;
2. validates workspace;
3. starts the command in a managed PTY;
4. records the process and session;
5. switches directly to the interactive session.

---

# 18. Process Management

Process management should be isolated from the TUI.

Example:

```go
type ProcessManager interface {
    Find(pid int) (*Process, error)
    Start(spec ProcessSpec) (*Process, error)
    Stop(pid int) error
    Kill(pid int) error
}
```

The implementation must account for:

- process groups;
- child processes;
- graceful termination;
- forceful termination;
- orphan processes;
- stale metadata.

The initial implementation should prefer process-group management over killing a single PID when the agent launches child processes.

---

# 19. Reconciliation

ASD maintains its own state, but the OS is the source of truth for whether a process actually exists.

A reconciliation loop should periodically compare:

```text
Stored sessions
        +
OS processes
        +
Terminal sessions
        +
Provider state
```

Example:

```text
Stored:
Session 42 → PID 18231

OS:
PID 18231 exists

Provider:
OpenCode running

Result:
RUNNING
```

If:

```text
Stored:
Session 42 → PID 18231

OS:
PID 18231 missing
```

then the session becomes:

```text
DEAD
```

Legacy display labels in the examples above: `RUNNING` ≈ lifecycle `running`; `DEAD` ≈ lifecycle `exited`/`failed` distinguished by exit reason. The normative reconciliation invariants live in `docs/architecture/session-state-machine.md`.

This prevents stale session records.

---

# 20. Storage

Persistent state follows the XDG split (normative paths in `docs/architecture/adr/0003-state-and-storage.md`):

```text
$XDG_CONFIG_HOME/asd/config.yaml      # default ~/.config/asd/config.yaml
$XDG_STATE_HOME/asd/sessions.json     # default ~/.local/state/asd/
$XDG_STATE_HOME/asd/state.json
$XDG_RUNTIME_DIR/asd/control.sock     # owner control socket, not persisted state
```

A legacy `~/.config/asd/sessions.json`, if present, is imported once under documented rules with a backup kept; the source is never deleted silently.

SQLite is intentionally not required for the first product release.

The amount of state is small and local.

SQLite becomes useful when ASD needs:

- event history;
- large session history;
- structured logs;
- analytics;
- full-text search.

The storage abstraction should nevertheless be separated from the domain model.

```go
type SessionStore interface {
    List() ([]Session, error)
    Get(id string) (*Session, error)
    Save(session Session) error
    Delete(id string) error
}
```

---

# 21. Event Model

ASD should internally represent meaningful state changes as events.

Examples:

```text
agent.detected
session.created
session.started
session.opened
session.detached
session.stopped
session.killed
session.dead
session.restarted
workspace.changed
```

Example:

```go
type Event struct {
    ID        string
    Type      string
    SessionID string
    Time      time.Time
    Data      map[string]any
}
```

This provides a foundation for future observability without requiring a full event system initially.

---

# 22. Architecture

Recommended architecture:

```text
                    ┌──────────────────┐
                    │       CLI        │
                    └────────┬─────────┘
                             │
                    ┌────────▼─────────┐
                    │       TUI        │
                    └────────┬─────────┘
                             │
                    ┌────────▼─────────┐
                    │   Application    │
                    │     Service      │
                    └────────┬─────────┘
                             │
        ┌────────────────────┼─────────────────────┐
        │                    │                     │
        ▼                    ▼                     ▼
   Discovery             Session              Workspace
        │                 Manager                 │
        │                    │                     │
        ▼                    ▼                     ▼
   Processes                 PTY                  Git
   Binaries                                     Providers
        │
        ▼
    Provider APIs
```

Suggested repository:

```text
asd/
├── cmd/
│   └── asd/
│       └── main.go
│
├── internal/
│   ├── agent/
│   ├── discovery/
│   ├── process/
│   ├── session/
│   ├── pty/
│   ├── workspace/
│   ├── git/
│   ├── store/
│   ├── events/
│   ├── providers/
│   │   ├── claude/
│   │   ├── codex/
│   │   ├── opencode/
│   │   ├── omp/
│   │   ├── orca/
│   │   └── generic/
│   └── tui/
│
├── pkg/
│   └── api/
│
├── docs/
│   └── PRODUCT.md
│
├── tests/
│
├── go.mod
└── README.md
```

---

# 23. V1 — Unified Local Agent Manager

V1 is a complete usable product, not a technical prototype.

## V1 capabilities

### Agent selection and launch

- detect installed agent CLIs;
- track running processes started by ASD;
- configure additional commands;
- start agent commands in selected workspaces;
- track processes and interactive PTY sessions started by ASD;
- associate processes with workspaces;
- identify Git repositories.

### Session management

- list sessions;
- inspect sessions;
- create sessions;
- interact with active sessions;
- rename sessions;
- stop sessions;
- kill sessions;
- restart sessions.

### TUI

- session list;
- status indicators;
- fuzzy search;
- agent filtering;
- workspace filtering;
- keyboard navigation;
- interactive session view;
- create;
- restart;
- stop/kill.

### CLI

```bash
asd
asd scan
asd list
asd new
asd open
asd inspect
asd rename
asd restart
asd stop
asd kill
```

### Providers

At least:

- generic provider;
- managed PTY integration;
- several concrete agent providers where their CLI behavior can be reliably detected and launched.

Provider support should be capability-based rather than pretending all agents expose identical APIs.

---

# 24. V2 — Agent Control Plane

V2 expands ASD from session management into continuous local orchestration.

## Daemon

Introduce:

```bash
agentd
```

Responsibilities:

- continuous discovery;
- process reconciliation;
- session state;
- event collection;
- provider monitoring.

Architecture:

```text
TUI
 │
 │ IPC
 ▼
agentd
 │
 ├── process monitor
 ├── PTY session monitor
 ├── provider monitor
 ├── workspace monitor
 └── event store
```

The TUI becomes a client of the daemon rather than directly owning system state. Managed PTY sessions can survive closing the TUI while the local daemon remains active.

---

## V2 workspace view

Example:

```text
WORKSPACES

● Kestrel
  ├── OpenCode     executor       RUNNING
  └── Claude       review         IDLE

● Cluster
  └── Codex        frontend       RUNNING

● Quant
  └── OMP          research       RUNNING
```

Legacy display labels in the example above: `RUNNING` ≈ lifecycle `running`; `IDLE` here is an Activity hint (provider-evidenced only), not a lifecycle state. The normative lifecycle/attachment/activity triple lives in `docs/architecture/session-state-machine.md`.

This changes the primary mental model from:

```text
agent → session
```

to:

```text
workspace
 └── sessions
      └── agents
```

---

## V2 observability

Display:

```text
CPU
RAM
PID
uptime
process tree
last event
Git branch
Git dirty state
```

Example:

```text
OpenCode / executor

PID       18231
CPU       4.2%
Memory    612 MB
Uptime    47m

Workspace ~/Kestrel
Branch    feature/executor
Changes   3 files

Last event
22:43:18 tool execution
```

The goal is not to become a general system monitor.

Only information useful for understanding agent sessions belongs here.

---

## V2 session history

Users can inspect previous sessions:

```bash
asd history
```

Example:

```text
42 OpenCode  Kestrel       47m   EXITED
41 Claude    Observability 1h    EXITED
39 Codex     Cluster       2h    EXITED
```

History enables:

- debugging;
- workflow reconstruction;
- session auditing;
- future analytics.

---

# 25. V3 — Developer Agent Control Plane

V3 extends ASD into a programmable platform.

The central abstraction becomes:

```text
Developer
   │
   ▼
ASD
   ├── Agents
   ├── Sessions
   ├── Processes
   ├── Workspaces
   ├── Events
   └── Tools
```

---

## V3 MCP Interface

ASD exposes selected capabilities through MCP.

Potential tools:

```text
list_agents
list_sessions
get_session
find_session
list_workspaces
get_workspace
get_process
get_recent_events
start_asd
interact_session
stop_session
restart_session
```

Example AI interaction:

```text
AI:
Find the running OpenCode session working on Kestrel.

ASD:
Session 42
Agent: OpenCode
Workspace: ~/Kestrel
Branch: feature/executor
Status: RUNNING
```

The AI interacts with the control plane rather than directly manipulating arbitrary shell commands.

---

# 26. V3 Automation

ASD may support controlled automation:

```text
workspace opened
      ↓
detect repository
      ↓
load workspace profile
      ↓
suggest/start configured agents
```

Example workspace configuration:

```yaml
workspace:
  name: Kestrel

agents:
  - name: opencode
    role: implementation

  - name: claude
    role: review

  - name: codex
    role: testing
```

A workspace could then expose:

```text
Kestrel

Implementation   OpenCode   ●
Review           Claude     ○
Testing          Codex      ○
```

Automation must remain opt-in.

---

# 27. Workspace Profiles

A workspace profile defines preferred agents and commands.

Example:

```yaml
name: kestrel

path: ~/Kestrel

sessions:
  - name: implementation
    agent: opencode

  - name: review
    agent: claude

  - name: testing
    agent: codex
```

Then:

```bash
asd workspace start kestrel
```

could create the configured sessions.

This is useful for projects where a developer repeatedly uses the same agent topology.

---

# 28. Security Model

ASD is a local privileged-adjacent tool because it can:

- inspect processes;
- launch processes;
- terminate processes;
- read local paths;
- provide a general-purpose terminal emulator.

Security principles:

## No remote control by default

The core product should not expose a network listener.

## No arbitrary command execution through external interfaces by default

MCP/API capabilities must be explicitly scoped.

## Explicit destructive operations

Killing a process requires an explicit user action.

## Configuration permissions

Configuration files should not contain secrets unless explicitly supported.

## ASD should not copy environment secrets into its own persistent state

For example, environment variables should not be persisted merely for discovery.

---

# 29. Configuration

Example:

```yaml
general:
  refresh_interval: 2s
  session_backend: pty

discovery:
  extra_paths:
    - ~/.local/bin

agents:
  - id: opencode
    name: opencode
    executable: opencode
    args: []

  - id: claude
    name: claude
    executable: claude
    args: []

  - id: custom-asd
    name: custom-asd
    executable: my-asd
    args: []

workspaces:
  - path: ~/Kestrel

terminal:
  scrollback: 10000
  max_input_queue: 65536
```

Configuration should remain intentionally small.

ASD should not become another giant dotfile ecosystem.

---

# 30. Error Handling

ASD must clearly distinguish:

```text
NOT_FOUND
NOT_RUNNING
UNSUPPORTED
PERMISSION_DENIED
INVALID_CONFIGURATION
SESSION_IO_FAILED
LAUNCH_FAILED
UNKNOWN
```

Example:

```text
Cannot interact with session 42.

Reason:
the managed PTY is no longer available.

The underlying process may still be running.
```

Avoid generic:

```text
Error: failed.
```

---

# 31. Recovery

ASD should tolerate unexpected failures.

Examples:

### Agent crashes

Legacy display shorthand (lifecycle `running` → lifecycle `exited`/`failed` distinguished by exit reason):

```text
RUNNING → DEAD
```

### Managed PTY becomes unavailable

The session is annotated `ORPHANED` (lifecycle `running` + Attachment `unavailable`) if the process is still alive but interactive input/output cannot be restored. `ORPHANED` is an annotation, not a lifecycle state; see `docs/architecture/session-state-machine.md`.

### ASD itself crashes

On restart:

```text
discover
  ↓
reconcile
  ↓
reconstruct state
```

The product should not depend on its own daemon having been alive continuously.

---

# 32. Non-Goals

ASD is explicitly not:

### An IDE

No editor replacement.

### A coding asd

ASD does not generate code.

### A terminal multiplexer

It is not a general-purpose terminal multiplexer. ASD only provides interactive access to the agent sessions it launches.

### A cloud orchestration platform

The initial product is local-first.

### A general process manager

Only process information relevant to agent sessions belongs in the product.

### An agent benchmark

No model quality ranking.

### An AI router

ASD may launch different agents, but model routing is outside the core product.

---

# 33. Technical Stack

## Core

```text
Go
```

## CLI

Potential:

```text
cobra
```

or a smaller CLI framework if preferred.

## TUI

Potential:

```text
Bubble Tea
Bubbles
Lip Gloss
```

## Process management

Go standard library plus Linux-specific interfaces where necessary.

Potential packages:

```text
os/exec
syscall
os
io
context
```

## Interactive process I/O

V1 uses a PTY managed by ASD for interactive input/output and process lifecycle.

## Git

Initially invoke the Git CLI rather than embedding a complete Git implementation.

## Storage

V1:

```text
JSON
```

V2:

```text
SQLite
```

if event/session history requires it.

---

# 34. Linux Strategy

Linux is the first target.

This simplifies access to:

```text
/proc
PTYs
process groups
filesystem events
Unix sockets
signals
```

The first implementation should optimize for Linux rather than immediately abstracting every operating system.

Cross-platform support can be added later behind interfaces.

---

# 35. Testing Strategy

Testing should focus on domain behavior rather than the TUI itself.

## Unit tests

- provider detection;
- process parsing;
- session state transitions;
- workspace detection;
- Git metadata;
- storage;
- fuzzy search.

## Integration tests

- launch a fake agent;
- detect it;
- associate it with workspace;
- create session;
- interactive session view;
- stop;
- restart;
- reconcile dead process.

## Fake provider

A fake provider is important:

```text
fake-asd
```

It can simulate (legacy display labels mixing axes; normative axes in `docs/architecture/session-state-machine.md` — `idle` is an Activity hint, provider-evidenced only):

```text
running
idle
crashed
slow startup
child processes
```

This avoids making tests depend on real third-party agents.

---

# 36. Example End-to-End Workflow

User installs several agents:

```text
Claude
Codex
OpenCode
OMP
Orca
```

They run:

```bash
asd
```

ASD scans the system:

```text
5 agent commands detected
8 ASD sessions
6 running
```

The user searches:

```text
/kestrel
```

Results:

```text
42 OpenCode  executor     ~/Kestrel
37 Claude    review       ~/Kestrel
```

They select the OpenCode command and choose `~/Kestrel`. ASD starts it and immediately opens the interactive session. They can return to the session list and select a different managed session, or start a Codex session in the same workspace.

Later:

```text
r
```

restarts the session by rerunning its saved command in the same workspace.

The developer never needs to remember the underlying process details or CLI invocation.

---

# 37. Product Success Criteria

ASD is successful when a developer can answer all of these from one interface:

> What coding agents are installed?

> What agent sessions are currently running?

> Which project is each session working on?

> How do I select and interact with a session ASD started?

> How do I start another agent in this repository?

> How do I stop or restart one?

> Which ASD-managed sessions have exited or become unavailable?

The core interaction should be:

```bash
asd
```

followed by:

```text
choose agent → choose workspace → run → interact
```

with no need to remember infrastructure details.

---

# 38. Future Direction

If the control-plane abstraction proves useful, ASD can evolve into:

```text
                   ASD
                      │
        ┌─────────────┼─────────────┐
        │             │             │
     Human          CLI           MCP
        │             │             │
       TUI        scripting         AI
        │             │             │
        └─────────────┼─────────────┘
                      │
                ASD Core
                      │
       ┌──────────────┼──────────────┐
       │              │              │
    Agents         Sessions      Workspaces
       │              │              │
    Claude          PTY             Git
    Codex           PTY             FS
    OpenCode        process
    OMP
    Orca
```

The important architectural boundary is:

> **ASD manages the environment around coding agents; the agents remain responsible for coding.**

This keeps the product small enough to be useful while leaving room for a powerful developer infrastructure layer.

---

# 39. One-Sentence Definition

> **ASD lets developers choose a coding-agent CLI and workspace, run it, and interact with the managed session from one local interface.**

---

# 40. Initial Product Command Surface

The intended command surface is deliberately small:

```bash
asd                           # open TUI

asd scan                      # discover available agent commands
asd list                      # list ASD-managed sessions
asd new                       # choose agent and workspace, then run
asd open <id>                 # interact with a managed session
asd inspect <id>              # inspect
asd rename <id> <name>        # rename
asd restart <id>              # restart
asd stop <id>                 # graceful stop
asd kill <id>                 # force termination
asd history                   # session history
asd workspace list            # list workspaces
asd workspace start <name>    # start workspace profile
```

The product should prefer a small, memorable command surface over exposing every internal operation.

Scope note: `history` ships in V2 (dedicated history, Stage 13) and `workspace list` / `workspace start` ship in V3 (profiles, Stage 15). In V1, past-session metadata is visible through `list` / `inspect`; the V1 command contract is frozen in `docs/architecture/cli-contract.md`.

---

# 41. Final Product Shape

The complete product can be understood as four layers:

```text
┌─────────────────────────────────────────────────────┐
│                    USER INTERFACE                   │
│                 CLI + TUI + MCP                    │
├─────────────────────────────────────────────────────┤
│                  CONTROL PLANE                      │
│       Session / Agent / Workspace / Events          │
├─────────────────────────────────────────────────────┤
│                    DISCOVERY                        │
│  Agent commands / PTYs / Git / Filesystem / Providers│
├─────────────────────────────────────────────────────┤
│                 LOCAL ENVIRONMENT                   │
│       Agents / PTYs / Processes / OS / Git         │
└─────────────────────────────────────────────────────┘
```

The product starts as a small Go CLI app with a TUI but has a coherent path toward a local **agent operating layer** without requiring that complexity on day one.
