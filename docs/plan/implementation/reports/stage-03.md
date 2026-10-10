# Stage 03 — Config and persistent state

Trạng thái: DONE · Milestone: M1 · Phụ thuộc: 02 · Coordinator review corrections closed (gate accepted).

## Kết quả

- `internal/config`: strict XDG path resolution (`config.yaml`, `state.json`, `sessions.json`, `.lock`), strict YAML schema (unknown keys, duplicate keys, multiple documents, invalid duration, duplicate agent ID, empty executable đều bị từ chối với file/key), argv literal, `~`-only expansion, redacted display, absent-file defaults.
- `internal/store`: versioned `sessions.json` (full Stage 02 snapshot envelope, per-file revision, strict decode, corrupt/future-schema fail safe, T20 delete guard, expected-revision conflict), independent `state.json` (recent workspaces, own revision), per-file atomic replace (temp-same-dir, flush/fsync, rename, dir sync best-effort), owner-lifetime flock lock, offline-mutation refusal while owner holds the lock, legacy `~/.config/asd/sessions.json` import only when XDG destination absent, backup + source preservation, no merge over valid destination.
- Durability: fault-injection tests prove write/sync/rename failure keeps the prior image and never half-JSON; concurrent writers are refused by the lock; temp files never leak.
- Config/state defaults documented in `docs/configuration.md` and `config.example.yaml` (example parses under the strict schema via a throwaway in-module run).

## Verification

Chạy trên Linux, offline (vendored), không ghi vào real `$HOME`:

- Targeted: `go test ./internal/config/ ./internal/store/` — pass, gồm round-trip, conflict, corrupt/future-schema, fault injection, lock contention, legacy import round-trip (giữ ID/name/exit metadata), permission tests trong `t.TempDir()`.
- `go test -race -count=1 -timeout 5m ./internal/store/ ./internal/config/` — pass.
- `make check` — `fmt-check`, `go vet` (unit+integration), unit suite, integration suite, `bin/asd --help/--version` đều pass (commit `931e09a`, build version `931e09a`).
- Migration acceptance: round-trip giữ ID/name/exit metadata; import lặp lại an toàn (destination authoritative); corrupt legacy fail an toàn, không ghi destination.
- Model thực tế của run này: `opencode/fledge-alpha-free` (OpenCode configured default; Orca receipt `model: null`; không override).

## Handoff

- **04/06:** config DTO là `config.Config`/`config.Paths` (ConfigFile/SessionsFile/StateFile/LockPath/RuntimeDir); store API là `store.SessionsStore` (Load/Commit/Delete, expected-revision conflict), `store.StateStore` (Load/Commit/AddRecent), `store.NewSessionsStore/NewStateStore` (offline) và `store.NewOwnerSessionsStore/NewOwnerStateStore` (owner-bound lifetime lock), lock `store.AcquireLock`/`Lock.Release`/`Lock.Owned` (`ErrLocked` khi bị chiếm, `ErrLockReleased` cho owner store sau Release); `TryLock` đã bị xóa, offline Load/Commit/Delete/AddRecent tự acquire lock ngắn và từ chối khi owner live, failure modes: `CorruptStateError`, `FutureSchemaError`, `RevisionConflictError`; legacy migration chỉ import khi XDG destination absent.
- **Commits (ledger order):** `afdacbc` W1-03.01 XDG paths; `f6124dd` W1-03.02 strict config; `9a32d69` W1-03.03 argv/tilde/redaction; `0f4d386` W1-03.04 permissions+lock; `bf83d32` W1-03.05 sessions store; `7f861bf` W1-03.06 state store; `38929bb` W1-03.07 atomic faults; `a3512d6` gofmt fault struct; `c868c3b` W1-03.08 legacy import; `204bf09` W1-03.09 config.example.yaml; `931e09a` W1-03.10 docs/configuration.md. Dependency commit (coordinator): `17aaa4c`.
- **Remaining limits:** `XDG_RUNTIME_DIR` fallback path chưa quyết định (Stage 06); SQLite/event DB chưa có (Stage 13); no two-file transactions by design; recent-workspaces không gắn vào config file; argv có thể chứa secret nên display mặc định redact.

## Gate evidence (final throwaway run, isolated temp home)

- Throwaway in-module Go program (added, run, then deleted; not a test named smoke; no script kept): isolated temp home via XDG overrides; `config.ResolvePaths` OK; absent config loads defaults (`refresh_interval=2s`, `session_backend=pty`); written config reloads via `config.Load`; owner `AcquireLock` OK; owner `SessionsStore.Commit` rev 0->1, `StateStore.Commit` rev 0->1; owner `Load` reloads rev 1; second sessions commit rev 2; `StateStore.AddRecent` rev 2; offline `SessionsStore.Load/Commit` and `StateStore.Commit` refused with `ErrLocked` while owner live; second `AcquireLock` refused; after `Lock.Release`: owner ops fail `ErrLockReleased`, `Owned()` false, offline `Load` rev 2, offline state `Commit` rev 3, lock re-acquirable; `sessions.json` (60B) and `state.json` (162B) envelopes on disk. All 21 assertions PASS; nothing written outside the temp home.
- `make check` — PASS at HEAD `cf23ef3` (fmt-check, go vet unit+integration, unit suite, integration suite, `bin/asd --help/--version`).
- `make test-race` (`go test -tags=integration -race -timeout 15m ./...`) — PASS, all packages ok.
- Model thực tế: `opencode/fledge-alpha-free` (OpenCode configured default; Orca receipt `model: null`; không override).

## Review

Coordinator review after the worker handoff found blockers before Stage 04/05 handoff:

- Reproduced: a first sessions.json Commit under a previously absent XDG state directory fails because AcquireLock tries to create .lock before creating its parent.
- Store code reacquires the flock for each mutation, so an owner holding the lifetime lock cannot write through the store; offline Load currently does not take the short lock.
- Envelope decoders read one JSON value without requiring EOF; trailing JSON can be accepted. Config keys also diverge from PRODUCT.md section 29 refresh/session-backend nesting.
- Legacy backup permissions and an existing/dangling destination path need safe handling tests.

Follow-up fixes are closed: correction commits `73e25d7`, `a756496`, `20d3a4b`, `b291ce8`, `2c3e808`, `dda747c`, `e56d933`, `cf6fd52`, `5e700bc`, `cf23ef3` all push to origin/dev; gate evidence above recorded 2026-10-07. Stage 03 status moved to DONE in the plan checklist commit.
