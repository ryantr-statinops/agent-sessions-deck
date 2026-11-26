# ASD Model Context Protocol (MCP) Control Plane

This document describes the Architecture, Security Model, and AI Client Configuration for the **Agent Sessions Deck (ASD) Model Context Protocol (MCP)** server.

---

## 1. Overview & Architecture

ASD exposes its local control plane via the standard [Model Context Protocol (MCP)](https://modelcontextprotocol.io), allowing AI coding assistants (such as Claude Code, Cursor, Windsurf, or custom automation scripts) to inspect, query, and safely orchestrate coding-agent sessions.

```text
               ┌────────────────────────┐
               │    AI Assistant Client │
               │ (Claude, Cursor, etc.) │
               └───────────┬────────────┘
                           │ stdio (JSON-RPC 2.0)
                           ▼
               ┌────────────────────────┐
               │     asd mcp serve      │ (MCP Adapter)
               │ (internal/mcp, policy) │
               └───────────┬────────────┘
                           │ Unix Domain Socket (IPC Client)
                           ▼
               ┌────────────────────────┐
               │     agentd (daemon)    │ (Single State Owner)
               │   PTY / Process Tree   │
               └────────────────────────┘
```

### Key Architectural Tenets
- **Single Owner Principle**: The MCP server is strictly a client of `agentd`. It does not launch agents independently and does not instantiate a second store writer.
- **Pure Stdio Transport**: Standard JSON-RPC 2.0 over `stdin` and `stdout`. No network or TCP sockets are opened.
- **Stream Discipline**: `stdout` is strictly reserved for valid JSON-RPC frames. All diagnostic logging, notices, and audit entries are written to `stderr`.
- **Zero-CGO Pure Go**: Built 100% with the Go standard library, compatible with offline vendoring (`-mod=vendor`) and `CGO_ENABLED=0`.
- **Protocol Version**: Pinned to specification version `"2024-11-05"`.

---

## 2. Security & Policy Model

ASD implements defense-in-depth to prevent AI assistants from turning ASD into an unrestricted remote shell endpoint:

1. **Default Read-Only**: By default, the MCP server operates in read-only mode (`--read-only=true`). Destructive mutations (`start_session`, `stop_session`, `restart_session`, `interact_session` write) are rejected immediately.
2. **Explicit Mutation Grants**: Mutations require explicit opt-in via configuration (`mcp.read_only: false`) or CLI flags (`--allow-mutations`, `--allow-tool=...`).
3. **Approval-Required Typed Error**: When a mutation is attempted without permission, the server immediately returns a typed `approval-required` JSON-RPC error. It never hangs or blocks waiting for interactive TUI prompts.
4. **Canonical Workspace Containment**: All session directories must resolve inside allowed workspaces (`allowed_workspaces`). Path traversal (`../`) and symlink escapes are rigorously evaluated and rejected.
5. **No Arbitrary Executable Injection**: AI clients can only launch declared agent IDs (`opencode`, `claude`, `codex`, etc.). Supplying raw executable binaries, shell scripts, or arbitrary environment variables is forbidden.
6. **Lease Arbitration & Human Priority**: When sending input to a session (`interact_session`), if an interactive lease is already held by a human user on the TUI or another client, the request is rejected with a lease conflict error (`CodeConflict`). AI cannot hijack active human terminal sessions.
7. **Idempotency & Audit Logging**: Mutation requests support `request_id` tokens and attempt generations. Retries do not cause duplicate session launches. All mutations are audited to `stderr` with metadata only (no raw prompts, secrets, or tokens are logged).

---

## 3. Tools Specification

### 3.1 Read-Only Tools

| Tool | Description | Key Arguments |
| :--- | :--- | :--- |
| `list_agents` | List declared and built-in coding agents and availability | `agent_id` (optional) |
| `list_workspaces` | List discovered workspaces, git metadata, and active counts | None |
| `get_workspace` | Get detailed summary and git info for a specific workspace path | `path` (required) |
| `get_process` | Inspect child process PID, state, attempt identity, and group | `session_id` (required) |
| `list_sessions` | List active and historical sessions with optional filters | `status`, `agent` |
| `get_session` | Get full metadata snapshot for a specific session | `session_id` (required) |
| `find_session` | Query sessions matching agent ID or lifecycle status | `agent`, `status` |
| `get_recent_events` | Query recent event journal with bounded pagination | `session_id`, `type`, `limit`, `after_revision` |

### 3.2 Controlled Mutation Tools

| Tool | Description | Key Arguments |
| :--- | :--- | :--- |
| `start_session` | Launch a new session for a configured agent in an allowed workspace | `agent`, `workspace`, `name`, `request_id` |
| `stop_session` | Gracefully stop an active session with optional timeout | `session_id`, `grace`, `request_id` |
| `restart_session` | Restart an existing session with generation guard | `session_id`, `force`, `target_generation`, `request_id` |
| `interact_session` | Read screen snapshot or write input with lease arbitration | `session_id`, `action` (`read_screen`/`write_input`), `input_text`, `expected_generation` |

---

## 4. AI Client Configuration Examples

Ensure `agentd` is running in the background before starting the MCP server:
```bash
agentd start
```

### 4.1 Claude Code / Claude Desktop

In your Claude MCP configuration file (e.g. `~/.claude.json` or `claude_desktop_config.json`):

#### Read-Only Setup (Recommended Default)
```json
{
  "mcpServers": {
    "asd": {
      "command": "asd",
      "args": ["mcp", "serve"]
    }
  }
}
```

#### Mutation-Enabled Setup with Workspace Whitelist
```json
{
  "mcpServers": {
    "asd": {
      "command": "asd",
      "args": [
        "mcp",
        "serve",
        "--allow-mutations",
        "--workspace", "/home/user/Projects"
      ]
    }
  }
}
```

### 4.2 Cursor

In `~/.cursor/mcp.json`:
```json
{
  "mcpServers": {
    "agent-sessions-deck": {
      "command": "asd",
      "args": ["mcp", "serve", "--read-only"]
    }
  }
}
```

### 4.3 Windsurf

In `~/.codeium/windsurf/mcp_config.json`:
```json
{
  "mcpServers": {
    "asd": {
      "command": "asd",
      "args": ["mcp", "serve"]
    }
  }
}
```

---

## 5. Verification & Testing

Verify that the MCP server initializes cleanly over stdio:
```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"test","version":"1.0"}}}' | asd mcp serve
```

Expected output on stdout:
```json
{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"serverInfo":{"name":"agent-sessions-deck","version":"dev"},"instructions":"ASD Control Plane MCP server. Query and manage coding-agent sessions with bounded, scoped tools."}}
```
