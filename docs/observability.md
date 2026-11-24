# ASD Observability & Workspaces Guide

ASD V2 introduces first-class **Workspaces Projection** and **Linux Process Observability**. This subsystem provides realtime insight into coding-agent execution, CPU usage, memory consumption, process trees, and repository context without sacrificing privacy or stability.

---

## Architecture Overview

```text
┌─────────────────────────────────────────────────────────────┐
│                       ASD Frontend                          │
│     TUI Dashboard ('w' toggle)   │   CLI ('asd workspace')  │
└──────────────────────────────┬──────────────────────────────┘
                               │ IPC OpWorkspaces / OpInspect
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                    Application Layer                        │
│   ┌───────────────────────────┐ ┌─────────────────────────┐ │
│   │   MetricsCache (2s TTL)   │ │  GitCache (10s Throttle)│ │
│   │  (Stale Fallback on Error)│ │ (Avoids Disk I/O Storm) │ │
│   └─────────────┬─────────────┘ └────────────┬────────────┘ │
│                 │                            │              │
│                 ▼                            ▼              │
│   ┌───────────────────────────┐ ┌─────────────────────────┐ │
│   │  Linux /procfs Sampler    │ │   Workspace Resolver    │ │
│   │   • stat (CPU ticks)      │ │   • Canonical Path      │ │
│   │   • statm (RSS pages)     │ │   • Git Root vs Subdir  │ │
│   │   • Tree Descendants      │ │   • Grouping & Sorting  │ │
│   └───────────────────────────┘ └─────────────────────────┘ │
└──────────────────────────────┬──────────────────────────────┘
                               │ Procfs / Kernel
                               ▼
┌─────────────────────────────────────────────────────────────┐
│                 Kernel Process Boundaries                   │
│   PID 4129: claude / opencode (Root Agent Process)           │
│     ├── PID 4130: node / python (Descendant Worker)         │
│     └── PID 4131: build-tool / git (Ephemeral Child)         │
└─────────────────────────────────────────────────────────────┘
```

---

## Core Principles & Security Invariants

1. **No External Process Scanning**: ASD never scans arbitrary processes across the system. It strictly probes the specific `PID` recorded during session launch.
2. **PID Reuse Defense**: When Linux reuses a PID after process termination, the kernel's process start tick counter (`starttime` in `/proc/[pid]/stat`) will differ from ASD's recorded `session.ProcessIdentity.StartTicks`. ASD detects this mismatch immediately and rejects the sample with `Unavailable: true, Error: "pid-reused"`, preventing resource usage or metadata leakage from foreign processes.
3. **Strict Descendant Attribution**: Only child processes whose parent tree directly links back to the root session PID and whose `StartTicks >= rootStartTicks` are aggregated into the process tree. Reparented or detached processes are never erroneously attributed.
4. **No Terminal Text Scraping**: Agent activity and metrics are measured purely from kernel counters and verified provider signals. ASD never parses terminal output or screen contents to guess agent state.
5. **Zero File Descriptor Leaks**: Every procfs file opened during sampling (`stat`, `statm`) is closed immediately after reading.

---

## Metrics Reference

| Metric Field | Unit | Procfs Source | Description |
| :--- | :--- | :--- | :--- |
| `uptime` | Duration (`s` / `m`) | Kernel clock & Start time | Time elapsed since session attempt was spawned. |
| `cpu_percent` | Percentage (`0.0% - N*100%`) | `/proc/[pid]/stat` (`utime + stime`) | Percentage of CPU used across interval ticks. |
| `memory_rss_bytes` | Bytes (`B`, `KB`, `MB`, `GB`) | `/proc/[pid]/statm` (`resident * pagesize`) | Resident Set Size of the root agent process. |
| `tree_rss_bytes` | Bytes (`B`, `KB`, `MB`, `GB`) | Recursive `/proc/[descendant]/statm` | Aggregate RSS of all verified descendant processes. |
| `process_count` | Integer | Recursive procfs scan | Total count of root process plus active children. |
| `stale` | Boolean | `MetricsCache` | `true` if re-sampling failed and cached metric was returned. |
| `unavailable` | Boolean | `MetricsCache` / Sampler | `true` if process exited, was recycled, or lacks permission. |

### Caching & Refresh Intervals
- **Metrics Cache TTL**: 2 seconds. Successive queries within 2 seconds return cached metrics. If a sample fails due to process exit or transient error, the last known metrics are returned with `stale: true`.
- **Git Status Throttle TTL**: 10 seconds. Probing repository status (`git status`, dirty files) is throttled to at most once per 10 seconds per workspace, protecting disk I/O under high session concurrency.

---

## Workspace Grouping & Projections

A workspace represents the canonical filesystem directory in which one or more sessions execute.

### Subdirectory vs Repository Root
- **Git Root**: The root directory containing the `.git` directory or worktree anchor.
- **Subdirectory Workspace**: If a session executes in `/repo/services/api` inside Git root `/repo`, ASD flags the workspace as `is_subdirectory: true` and displays `[subdir]` in the CLI and TUI.

### Sorting Policy
Workspaces are sorted deterministically according to the following hierarchy:
1. **Active First**: Workspaces with `ActiveCount > 0` are sorted before dormant workspaces.
2. **Recent Activity**: Workspaces with more recent `LastObservedAt` timestamps come first.
3. **Lexical Tie-Breaker**: Alphabetical sorting by workspace directory path.

---

## CLI Usage

### Listing Workspaces
```bash
# Human-readable table
asd workspace list

# Filter by lifecycle
asd workspace list --lifecycle running

# Filter by agent
asd workspace list --agent opencode

# Output stable JSON envelope
asd workspace list --json
```

**Example JSON Output**:
```json
{
  "command": "workspaces",
  "authority": "live",
  "observed_at": "2026-11-25T10:15:30Z",
  "workspaces": [
    {
      "workspace_id": "/home/dev/project-alpha",
      "path": "/home/dev/project-alpha",
      "display_name": "project-alpha",
      "git": {
        "root": "/home/dev/project-alpha",
        "branch": "main",
        "detached": false,
        "dirty": true,
        "changed_files": 3
      },
      "is_subdirectory": false,
      "active_count": 2,
      "total_count": 3,
      "session_ids": ["s-001", "s-002", "s-003"],
      "last_observed_at": "2026-11-25T10:15:00Z"
    }
  ]
}
```

### Inspecting Session Metrics
```bash
asd inspect 01JQASDWSDECK0001
```

**Sample Output with Observability**:
```text
id: 01JQASDWSDECK0001
name: refactor-compiler
agent: opencode
workspace: /home/dev/project-alpha
authority: live
observed_at: 2026-11-25T10:15:30Z
revision: 14
lifecycle: running
attachment: attached
io_availability: available
activity: active
attempt: 1
identity_verified: true
persisted: true
command: /usr/local/bin/opencode --acp
identity: pid=4129 pgid=4129 start=104523 boot=boot-1 owner=owner-1
git_root: /home/dev/project-alpha
git_branch: main
git_dirty: true
git_changed_files: 3
cpu: 14.8%
memory_rss: 83886080
tree_rss: 251658240
process_count: 4
uptime: 245.0s
metrics_sampled_at: 2026-11-25T10:15:29Z
```

---

## TUI Dashboard Controls

In the ASD interactive terminal UI:
- **`w`**: Toggle between **Flat Session View** and **Workspace-Grouped View**.
- **`d`**: Open / close the **Session Detail & Metrics Inspector** pane.
  - On standard and wide terminals (`>= 100` cols), metrics are displayed side-by-side.
  - On narrow terminals (`< 100` cols) or constrained heights, the inspector automatically adapts into **Responsive Compact Mode**.
- **`r` / `x` / `K`**: Lifecycle controls (Restart, Stop, Force Kill) respect the currently highlighted session.
