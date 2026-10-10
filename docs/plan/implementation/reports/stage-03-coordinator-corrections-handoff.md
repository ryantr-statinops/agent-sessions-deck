# Stage 03 coordinator-review corrections — worker handoff

Model: opencode/fledge-alpha-free (worker terminal term_6be0c157-9e65-4013-ba79-7eb3770adda3, run_3f88de491e35, dispatch ctx_e9b0f64b5063).

## Commits (each stepped: focused go test, then push of that commit)

| Commit | Change | Focused tests | Push |
|---|---|---|---|
| 73e25d7 | fix(store): XDG first-write and owner lock lifecycle — AcquireLock creates the state-home parent privately, Lock carries the shared mutex, owner-bound SessionsStore/StateStore (NewOwnerSessionsStore/NewOwnerStateStore), offline Load/Commit/Delete/AddRecent take a short flock and refuse during a live owner, owner ops serialized, TryLock removed | go test ./internal/store (PASS) | ebfb8b7..73e25d7 |
| a756496 | fix(store): strict durable JSON and state invariants — both envelopes require exactly one JSON value then EOF, reject trailing JSON, reject null/missing sessions and recent_workspaces, canonical arrays from writers, empty recent path rejected, LastUsed stored UTC | go test ./internal/store (PASS) | 73e25d7..a756496 |
| 20d3a4b | fix(store): atomic file safety — os.CreateTemp in the same directory replaces tmpCounter, mode set before fsync, EnsurePrivateDir chmods only directories it created, partial-write fault injection proves temp cleanup and old-image survival, concurrent writes in separate homes pass under -race | go test ./internal/store, go test -race (PASS) | a756496..20d3a4b |
| b291ce8 | fix(store): legacy migration — Lstat destination presence (dangling symlink counts), other stat errors propagate, private 0600 backup that never overwrites a different existing backup (falls back to .backup.N), source preserved, MigrateLegacySessionsWithLock reuses a held owner lock | go test ./internal/store -run TestMigrate (PASS) | 20d3a4b..b291ce8 |
| 2c3e808 | fix(config): strict config contract — general.refresh_interval (default 2s, must be positive), general.session_backend must be pty, malformed trailing YAML documents rejected, Config.Save/Agent.CommandLine/EnsurePrivateFile/MkdirTempFile/CreatePrivateFile removed, tautological shell-display test removed, argv-literal and redaction tests kept | go test ./internal/config ./internal/store (PASS) | b291ce8..2c3e808 |
| dda747c | docs(config): strict schema in config.example.yaml | config parse test (PASS) | 2c3e808..dda747c |
| e56d933 | docs(configuration): strict schema in docs/configuration.md | — | dda747c..e56d933 |
| cf6fd52 | docs(product): strict schema in PRODUCT.md section 29 | — | e56d933..cf6fd52 |

Final gates: `go test -race ./internal/config ./internal/store` PASS, `make check` PASS (fmt, vet, unit, integration, smoke). Uncommitted coordinator regression tests in internal/store/sessions_test.go and state_test.go now pass and are committed with the matching fix (73e25d7/a756496). Stage 02 session.Store interface preserved (Load/Commit/Delete, expected-revision conflicts); Stage 03 PLANNED status and coordinator review notes untouched; go.mod/go.sum/vendor untouched.

## Residual limits

- session_backend currently only accepts the literal "pty"; adding another backend requires a deliberate Stage 04+ decision.
- A malformed trailing YAML document is now rejected, but a bare trailing `---` also fails (it opens an empty second document).
- Offline Load now takes the short state-home lock, so a very contended state home can briefly delay reads.
- Backup fallback names (.backup.N) are first-free; content equality with the existing backup is the only reuse criterion.
- Directory fsync remains best-effort by design; durability relies on the rename surviving.
