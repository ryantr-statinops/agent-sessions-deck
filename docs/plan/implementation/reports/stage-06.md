# Stage 06 — Foreground IPC and CLI

Status: **COMPLETE** · Wave: W1 · Dependencies: Stages 03, 04, 05.

## Implemented

- Linux owner bootstrap acquires the state-home lock before binding. A random owner instance ID fences clients; the endpoint is mode `0600` inside a same-UID mode-`0700` directory. The fallback is `/tmp/asd-<euid>/<state-home-hash>.sock`.
- Accepted sockets verify `SO_PEERCRED` before reading frames. Stale socket cleanup requires a held owner lock, a same-UID socket, definitive `ECONNREFUSED`/`ENOENT`, and an unchanged inode. Timeouts and active/unresponsive paths are not unlinked.
- IPC V1 uses a 4-byte big-endian length, strict JSON, a 1 MiB encoded frame cap, 128-byte request IDs, typed safe wire errors, a 30-second control timeout, versioned handshake, instance fencing, and a bounded idempotency cache (2,048 entries / 10 minutes).
- The server/client implement app operations and a duplex terminal stream. Mutations use the application service's global serializer; duplicate mutation IDs replay the original response. Terminal byte/screen/input/resize/close messages are multiplexed; a raw-byte gap is explicit while screen snapshots continue. Disconnect releases the lease without requesting process termination.
- Foreground composition wires the held-lock store, process runtime, terminal source, workspace resolver, provider registry, lease broker, and events. Owner mode probes built-ins with Stage 04 identity markers. Offline list/inspect use stored authority without probing/adopting processes.
- CLI V1 includes `scan`, `list`, `inspect`, `new`, `open`, `rename`, `restart`, `stop`, and `kill`. `scan` reports `available`/`not-found`/`uncertain`; list/inspect distinguish live and stored authority; new supports TTY owner bootstrap and scriptable create through a live owner; open uses raw TTY mode, resize events, and Ctrl+] detach. Offline rename reconciles recorded process identity under the store lock before applying metadata changes, refuses active rows without an identity, and never adopts or signals. Offline stop/kill fail closed.
- JSON output uses lower-case stable envelopes. Errors include command/code/reason/hint and map to exit codes 0–4. Help includes examples and foreground-owner lifetime.

## Verification

- `make check` — passed: format, vet, unit suite, integration suite, built-binary help/version smoke.
- `make test-race` — passed across all packages with integration tag and race detector.
- Offline-rename integration creates a real `/usr/bin/sleep` process, shuts down only the owner server, runs the no-owner CLI rename, and verifies stored authority plus reconciled `running` lifecycle and renamed metadata. The fixture process exits naturally; no orphan signal is used.
- Owner integration creates a real `/usr/bin/sleep` PTY process through one owner, serves three independent IPC clients, verifies concurrent rename revisions 2–4 without lost writes, then stops/restarts/kills the same session.
- IPC integration covers competing owner lock, handshake, unary dispatch, terminal byte output/input, resize, screen snapshot, raw queue overflow, screen continuation after raw gap, disconnect detach, and owner shutdown.
- Discovery fixtures cover verified available, missing, and identity-uncertain built-ins. CLI tests cover live/stored list authority, non-TTY create with a live owner, refusal to bootstrap/spawn without a TTY, open-without-owner error classification, and destructive confirmation behavior.
- Separate-process built-binary smoke exercised foreground owner and client commands through create/list/inspect/rename/stop/restart/kill; `go run ./cmd/asd --help`, `--version`, offline `scan --json`, and `list --json` were also exercised.

## Completion boundary

Stage 06 acceptance checks and repository gates passed. Stage 07 dashboard/TUI remains outside this stage. Offline stop/kill remain unsupported; no process is adopted or signaled from stored metadata alone. Stage 06 is implemented on `dev`; it has not been merged to `main`.

## Commit trail

The Stage 06 implementation is split across owner bootstrap, endpoint/framing/idempotency, server/client stream, composition, CLI contract, discovery, JSON/error, and integration-test commits on `dev`. The exact commits are retained individually in Git history; no merge to `main` has been made.
