# Troubleshooting Guide

This guide assists operators in diagnosing and resolving common operational issues with `agent-sessions-deck` (`asd`).

## Error Codes Reference

### `SESSION_IO_FAILED`
- **Symptom**: `asd open <ref>` returns `SESSION_IO_FAILED: no PTY handle for attempt N: the process survived an owner crash but lost its controlling terminal`.
- **Cause**: The owner process crashed while a session child process was running. The child process survived (reparented to init), but its controlling terminal file descriptor was lost.
- **Resolution**:
  1. Inspect the session to view the surviving PID: `asd inspect <ref>` or `asd list`.
  2. If the process is still performing useful work, allow it to finish.
  3. To end the process explicitly: `kill -TERM <pid>` or `kill -KILL <pid>`.
  4. Once ended, restart the session with a fresh PTY: `asd restart <ref> --force`.

### `OWNER_UNAVAILABLE` (Write Latch)
- **Symptom**: Mutation commands (`create`, `restart`, `kill`, `rename`, `delete`) fail with `OWNER_UNAVAILABLE: the session metadata could not be written, so this owner will not record another mutation`.
- **Cause**: A persistent store write failed (e.g. disk full, permission denied on `~/.local/state/asd/sessions.json`, or read-only filesystem). ASD latches against further writes to prevent memory-store divergence.
- **Resolution**:
  1. Read commands (`asd list`, `asd inspect`) remain available to view system state.
  2. Check disk space: `df -h ~/.local/state/asd`.
  3. Check file permissions: `ls -la ~/.local/state/asd/sessions.json`. Ensure the owner user has write permissions (`chmod 0644`).
  4. Restart `asd` to reload state from disk once the filesystem issue is resolved.

### `CORRUPT_STATE`
- **Symptom**: `asd` fails to start with `CORRUPT_STATE: the stored session metadata is corrupt or unreadable`.
- **Cause**: The `sessions.json` file contains invalid JSON or schema violations.
- **Resolution**:
  1. ASD **never** silently resets or overwrites corrupt metadata to prevent data loss.
  2. Inspect the JSON file manually: `cat ~/.local/state/asd/sessions.json | jq .`.
  3. Repair any syntax errors or restore from backup.
  4. Alternatively, move the corrupt file aside: `mv ~/.local/state/asd/sessions.json ~/.local/state/asd/sessions.json.bak` and re-launch.

### `CONFLICT`
- **Symptom 1**: `CONFLICT: restarting a live attempt needs an explicit force confirmation`.
  - **Resolution**: Pass `--force` to confirm restarting an active session (`asd restart <ref> --force`).
- **Symptom 2**: `CONFLICT: the interactive lease is currently held by <holder>`.
  - **Resolution**: Detach the existing client (`asd detach <ref>`) or wait for the client to close.
- **Symptom 3**: `CONFLICT: another foreground owner already holds this state home`.
  - **Resolution**: Check for running `asd` daemon or owner processes with `ps aux | grep asd`. Ensure previous instances have shut down cleanly.

### `STALE_ATTEMPT`
- **Symptom**: Observation or exit report indicates stale attempt.
- **Cause**: An asynchronous probe or exit signal arrived after the session had already advanced to a newer generation.
- **Resolution**: Benign condition; ASD automatically fences out stale events and leaves the current attempt untouched.

---

## Socket and Lock Recovery

If an abnormal power failure or SIGKILL left stale sockets:
1. ASD automatically probes the Unix socket on startup. If the socket is unresponsive and the lock file is unheld, it cleans up automatically.
2. If manual cleanup is required:
   ```bash
   rm -f ~/.local/state/asd/owner.sock ~/.local/state/asd/owner.lock
   ```
