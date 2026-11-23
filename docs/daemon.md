# Running and Managing the ASD Daemon (`agentd`)

Starting with ASD V2 (Milestone M3), session lifecycle and PTY terminal management are handled by a dedicated background service: `agentd`.

With `agentd`, your coding-agent sessions (Claude, Codex, OpenCode, generic shell agents) continue running uninterrupted even when you close the terminal, exit the TUI dashboard, or disconnect from SSH.

---

## Architecture Overview

```text
┌─────────────────┐       ┌─────────────────┐
│     asd TUI     │       │     asd CLI     │
│ (Bubble Tea UI) │       │ (list, new, …)  │
└────────┬────────┘       └────────┬────────┘
         │                         │
         └────────────┬────────────┘
                      │ Local Unix Stream Socket (0600)
                      │ [SO_PEERCRED verified]
                      ▼
         ┌─────────────────────────┐
         │     agentd (Daemon)     │
         │                         │
         │  • SessionManager       │
         │  • PTY Master Handles   │
         │  • ANSI VT Emulation    │
         │  • 10k Scrollback Ring  │
         │  • Single-Writer Store  │
         └────────────┬────────────┘
                      │ PTY Slave / Process Groups
                      ▼
         ┌─────────────────────────┐
         │ Child Agent Processes   │
         │ (Claude, Codex, etc.)   │
         └─────────────────────────┘
```

---

## Command Reference

### 1. `agentd run` (Foreground Mode)
Runs `agentd` directly in the current terminal, emitting operational logs to standard error. This is the recommended mode when running under systemd or a process supervisor.

```bash
agentd run
# or
agentd --foreground
```

Options:
- `--config <path>`: Override path to `config.yaml`.
- `--log-level <debug|info|warn|error>`: Set logging verbosity (default: `info`).

### 2. `agentd start` (Background Mode)
Spawns `agentd` in the background as a detached process and waits for the Unix socket to become ready before exiting.

```bash
agentd start
```

If `agentd` is already running, `agentd start` prints the running PID and exits successfully. If a V1 foreground owner is holding the lock, it reports an error and refuses to spawn.

### 3. `agentd status`
Queries the daemon health and runtime statistics via the control socket without needing interactive terminal allocation.

```bash
agentd status
# Or machine-readable JSON:
agentd status --json
```

Example output:
```text
Daemon Status:   RUNNING
PID:             4129
Instance ID:     7b9a4c8e103f5d21a8b9c0d1e2f3a4b5
Socket:          /run/user/1000/asd/8f2c3d1e.sock
Uptime:          2h 14m 32s
Sessions Active: 2
```

### 4. `agentd stop`
Instructs the daemon to shut down gracefully.

```bash
agentd stop
```

Safety rules:
- If there are **active sessions running**, `agentd stop` will reject termination by default to protect in-flight coding agent work.
- To shut down the daemon and gracefully stop all running sessions, use `--force`:
  ```bash
  agentd stop --force
  ```
- You can specify a custom graceful shutdown window with `--timeout`:
  ```bash
  agentd stop --force --timeout 15s
  ```

---

## Running as a systemd User Service

To automatically start `agentd` when you log in and keep it supervised, configure a systemd user unit:

1. Create directory `~/.config/systemd/user/`:
   ```bash
   mkdir -p ~/.config/systemd/user
   ```

2. Create `~/.config/systemd/user/agentd.service`:
   ```ini
   [Unit]
   Description=Agent Session Deck Daemon (agentd)
   Documentation=https://github.com/ryantr-statinops/agent-sessions-deck
   After=network.target

   [Service]
   Type=simple
   ExecStart=%h/.local/bin/agentd run
   Restart=on-failure
   RestartSec=3s
   KillMode=mixed
   TimeoutStopSec=15s

   # Sandboxing & security
   NoNewPrivileges=true
   PrivateTmp=true
   ProtectSystem=strict
   ProtectHome=read-only
   ReadWritePaths=%h/.local/state/asd %h/.config/asd /run/user/%U/asd

   [Install]
   WantedBy=default.target
   ```

3. Reload systemd and enable the service:
   ```bash
   systemctl --user daemon-reload
   systemctl --user enable --now agentd.service
   ```

4. Check service status:
   ```bash
   systemctl --user status agentd.service
   ```

> [!NOTE]
> By default on Linux, user systemd units stop when you log out. If you want sessions to persist across SSH logouts, enable lingering for your user:
> ```bash
> loginctl enable-linger $USER
> ```

---

## Socket & Security

- **Socket Location**: Stored in `$XDG_RUNTIME_DIR/asd/<hash>.sock` (e.g. `/run/user/1000/asd/`). If `XDG_RUNTIME_DIR` is not set, falls back to `/tmp/asd-<uid>/<hash>.sock`.
- **Permissions**: Directory permissions are restricted to `0700` and socket permissions to `0600`.
- **Peer Verification**: All incoming connections are verified against the effective UID using `SO_PEERCRED`. Cross-user or unauthenticated access is rejected before reading any bytes.
- **No Network / TCP**: `agentd` does not bind any TCP or network interfaces.
