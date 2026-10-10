# Linux process ownership and terminal stream contract

Stage 05 implementation contract for `internal/process`, `internal/pty`, and `internal/terminal`. This documents the current Linux implementation; it does not promise V2 daemon lifetime.

## Process ownership

- `process.Runtime` implements `session.ChildRuntime`. The caller supplies a non-empty owner instance ID and a validated `LaunchRequest`; the request carries an absolute executable plus literal argv and a validated workspace.
- Launch uses the process owner's inherited environment and starts one controlling PTY in a new OS session. The initial terminal size is 80 columns by 24 rows. `ProcessIdentity` records PID, PGID, boot ID, `/proc` start ticks, and owner instance ID. Launch refuses an identity whose PGID is not the child PID.
- Runtime contexts bound an operation; canceling the launch caller after launch does not become the child lifetime context. A direct `os.Process` handle is used only to roll back a child before identity capture. Subsequent signals require the recorded identity to match the runtime-owned entry and the current procfs identity.
- One runtime reaper calls `Wait` once. `WaitExit` grants one caller a result and returns terminal exit-code/signal evidence. A non-zero exit code is a valid exit, not a wait failure.
- `Stop` sends SIGTERM to the verified process group and never escalates. A live child at the grace deadline returns a live observation; `ForceKill` is the explicit SIGKILL path and confirms the leader reap separately from signal delivery.
- The process leader's reap does not prove that every member of its numeric PGID is gone. The runtime closes the PTY master, gives terminal draining up to 500 ms, then scans procfs for up to a further 500 ms. Remaining non-zombie group members or an incomplete scan are included in exit evidence. Once the leader is reaped, runtime code does not signal that PGID again: a reused process-group ID is not a safe capability.
- A descendant that calls `setsid` leaves the owned process group. V1 does not adopt or signal it. The runtime/test evidence establishes this boundary; process supervision across detached groups is not claimed.

## Terminal stream

- `terminal.Manager.Register` starts one PTY reader and one emulator per attempt before `Launch` returns. Each reader continues draining while detached; parser input is not dropped. The emulator answers supported terminal queries independently of subscribers.
- `TerminalSubscription.InitialSnapshot` is captured atomically with subscription creation and includes its sequence. Following frames are ordered deltas or a full snapshot when a bounded frame queue coalesces updates. Attach therefore has no snapshot/subscribe byte gap.
- Screen state and scrollback are bounded. Raw-byte subscriber buffers are bounded separately; a reader that falls behind gets `app.ErrTerminalOutputGap` and must resynchronize from the screen snapshot. Screen parsing continues despite a slow or absent byte subscriber.
- One interactive lease is allowed per session. Closing a subscription releases the lease only; it does not signal or close the child. Resize requests are dimension-validated, debounced, applied to the PTY, and reflected in screen state; the kernel delivers SIGWINCH to the foreground process group.
- A new attempt for a completed logical session removes the prior terminal only after its PTY drain has finished, then registers a fresh screen at the requested generation. A live attempt cannot be overlapped.

## Limits

- Procfs group scans are observations, not proof that a member is a descendant. Evidence says which PIDs remained in the PGID; it does not claim ownership beyond the verified leader identity.
- If terminal drain reaches its deadline, the timeout is recorded in exit evidence; the runtime does not wait indefinitely for inherited PTY descriptors.
- Raw terminal output and environment values are not persisted. V1 owner lifetime and shutdown behavior remain governed by ADR 0001; this runtime does not promise that sessions survive owner exit.
