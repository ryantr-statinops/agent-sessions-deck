# Migrating from ASD V1 to V2

This document details the transition from **Agent Session Deck V1** (single foreground owner) to **ASD V2** (background `agentd` daemon).

---

## What Changed?

| Feature | ASD V1 (Milestone M2) | ASD V2 (Milestone M3) |
|---|---|---|
| **Primary Owner** | The first interactive `asd` process | Dedicated `agentd` background daemon |
| **Session Persistence** | Sessions close when owner process exits | Sessions persist independently in `agentd` |
| **TUI Close Action** | Prompts to Stop all and quit | Detaches cleanly; child sessions continue |
| **CLI Role (`asd`)** | Owner or transient IPC client | Pure IPC client with auto-start capability |
| **Supervision** | Manual foreground shell | Supports `agentd start` or `systemd --user` |

---

## Migration Steps

### Step 1: Quiesce Active V1 Sessions
Because ASD prioritizes stability and does not perform unverified file-descriptor transfers across process boundaries:
1. In your running V1 `asd` instance, inspect any active agent sessions.
2. Allow critical commands to complete, or stop sessions with `asd stop <id>` or through the TUI quit dialog.
3. Exit the V1 `asd` process. Verify the process has fully exited and released the lock:
   ```bash
   # Check if any legacy foreground owner is running:
   ps aux | grep "[a]sd"
   ```

### Step 2: Update ASD Binaries
Install the V2 binaries containing both `asd` and `agentd`:
```bash
# Example from release archive or make:
make build
cp bin/asd bin/agentd ~/.local/bin/
```

### Step 3: Start `agentd`
You can either run `agentd` manually or let `asd` start it for you:

- **Option A (Recommended for systemd systems)**:
  Set up the user service per [docs/daemon.md](daemon.md):
  ```bash
  systemctl --user enable --now agentd.service
  ```

- **Option B (Standalone background daemon)**:
  ```bash
  agentd start
  ```

- **Option C (On-demand via `asd`)**:
  Running `asd` interactively will automatically launch `agentd` in the background if it is not already running.

---

## Compatibility & State Migration

### Persistent State Files
- `~/.local/state/asd/sessions.json` retains its exact schema.
- Upon booting, `agentd` reads existing session records and runs startup reconciliation:
  - Active sessions that belonged to an exited V1 owner are marked as `SESSION_IO_FAILED` / `ORPHANED` because their controlling PTY closed with the V1 process.
  - History, names, workspaces, and attempts are fully preserved.
  - You can immediately restart any previously recorded session using `asd restart <id>`.

### Error Scenarios & Troubleshooting

#### Error: `a V1 foreground owner already holds this state home`
- **Cause**: An older V1 `asd` process is still running and holds `$XDG_STATE_HOME/asd/.lock`.
- **Solution**: Switch to the terminal running the V1 owner and exit it cleanly, or run:
  ```bash
  kill -TERM <pid-of-v1-asd>
  ```
  Once the V1 process releases the lock, start `agentd`.

#### Error: `graceful stop timed out; active sessions exist`
- **Cause**: Running `agentd stop` while agent sessions are still executing.
- **Solution**: Check active sessions with `agentd status` or `asd list`. If you intend to terminate them, pass `--force`:
  ```bash
  agentd stop --force
  ```
