# Foreground IPC protocol (V1)

Status: implemented on Linux in `internal/ipc`. One foreground `asd` process owns the state-home lock, process runtime, terminal manager, event publisher, and store writer. CLI clients use the same application DTOs through a private Unix stream socket; the owner never runs as a hidden daemon.

## Endpoint ownership

`ipc.BootstrapPaths` takes the state-home `.lock` before inspecting or binding a socket. The owner instance ID is 16 cryptographically random bytes rendered as 32 hex characters. A competing process cannot claim the lock or bind a second owner.

`SocketPath` hashes the cleaned absolute state directory (first 8 SHA-256 bytes) to a short endpoint name. It prefers `<XDG_RUNTIME_DIR>/asd/<hash>.sock`; if XDG_RUNTIME_DIR is absent, it uses `/tmp/asd-<effective-uid>/<hash>.sock`. The containing directory is verified as a real, same-UID directory and set to `0700`; the socket is set to `0600`. Accepted peers must match the effective UID via Linux `SO_PEERCRED` before protocol bytes are read.

Stale cleanup runs only after the owner lock is acquired. A path must be a same-UID Unix socket, a connection must fail definitively with `ECONNREFUSED` or `ENOENT`, and an inode recheck must match before unlink. Active connections, permission errors, timeouts, and inconclusive liveness checks are never unlinked.

## Framing

Each frame is a four-byte unsigned big-endian JSON body length followed by one strict JSON `Frame`. Maximum encoded body: 1 MiB. Unknown envelope fields, invalid JSON, trailing JSON, malformed request IDs, and unsupported versions are rejected. Request IDs are printable UTF-8, nonblank, and at most 128 bytes.

```json
{"version":1,"type":"request","request_id":"…","operation":"list","payload":{},"revision":0}
```

Envelope fields: `version`, `type`, `request_id`, `operation`, `instance_id`, `payload`, `revision`, and `error`. The payload is one JSON value; terminal byte payloads use JSON `[]byte` encoding (base64). Wire errors contain a typed code, subject, optional generation, reason, and hint; arbitrary wrapped causes are replaced with a generic safe message.

Frame types: `hello`, `hello_ack`, `request`, `response`, `terminal_bytes`, `terminal_screen`, `terminal_input`, `terminal_resize`, and `terminal_close`.

## Handshake and requests

A client sends `hello` with protocol version and request ID. The owner responds with matching `hello_ack` and its `instance_id`. A client caches that instance ID and refuses to continue if a later connection reaches a replacement owner. `Ping` performs only this handshake.

Unary service requests use one connection each. The client requires the response type and request ID to match. A transport failure retries a unary request once with the same request ID. The retry is only accepted against the same owner instance; if the owner changed, the client returns `OWNER_UNAVAILABLE` instead of risking a duplicate side effect.

Owner-side mutations still pass through `app.Service`'s serialized mutation lock and optimistic store revision. `Dispatcher` binds each request ID to SHA-256(operation + payload): identical retries wait for or replay the original response; reusing an ID with another operation/payload returns `CONFLICT`. The bounded cache holds at most 2,048 entries for 10 minutes. An unexpired full cache fails closed rather than evicting an in-flight request.

Unary handshake/request/response operations use a 30-second deadline. Frames are size-checked before payload allocation. The application request context is owned by the foreground server, not the transient client connection; disconnecting after dispatch does not cancel an accepted mutation.

## Terminal stream

`open` keeps its Unix connection open. Its initial `response` contains the session snapshot, granted interactive lease, committed revision/event, and initial screen snapshot. Following frames multiplex:

- `terminal_bytes`: bounded raw child output;
- `terminal_screen`: ordered screen snapshot or delta;
- `terminal_input`: base64 JSON byte array from the client;
- `terminal_resize`: positive cell dimensions;
- `terminal_close`: detach this client only.

Screen deltas carry their base sequence. The client applies them to its last screen and coalesces pending frames to a current full snapshot, preserving resynchronization while bounding memory. A sequence gap is an error, not a guessed screen.

If the bounded raw output queue falls behind, the owner sends a typed error on `terminal_bytes`; the client returns `app.ErrTerminalOutputGap` from raw `Read` and stops exposing truncated bytes. The screen-frame stream remains usable. Stream reads have no idle timeout; writes and setup remain bounded. Client disconnect or `terminal_close` closes the subscription and causes an owner-side `Detach`; it does not stop or kill the child. Resize and raw writes are serialized on the socket.

On owner shutdown, the server closes the listener and accepted connections, waits for handlers, then releases the owner lock. Open-stream cleanup attempts `Detach` with a bounded independent context.

## Error and replacement behavior

A failed handshake, socket absence, protocol mismatch, owner restart, or uncertain mutation result is reported as `OWNER_UNAVAILABLE` with a retry/inspection hint. `NOT_INTERACTIVE` is a CLI/TTY policy error, not a transport fallback. Offline `scan` probes binaries directly; offline `list` and `inspect` use stored authority and never claim liveness. Mutations do not silently fall back to an offline writer except safe terminal-record rename.
