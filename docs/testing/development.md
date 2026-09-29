# Development and testing guide

Operational guide for building, checking and testing Agent Session Deck
(`asd`) on a clean checkout. For contribution rules and file ownership see
[CONTRIBUTING.md](../../CONTRIBUTING.md); for the product contract see
[PRODUCT.md](../../PRODUCT.md) (Linux strategy in §34, testing strategy in
§35).

Everything here is a Makefile target run from the repository root. The CI
workflow runs the same targets in the same order, so anything in this document
can be reproduced locally with one command.

## 1. Host requirements

| Requirement | Detail |
|---|---|
| OS | Linux. `asd` targets `/proc`, PTYs, process groups, signals, filesystem events and Unix sockets. |
| Go | The toolchain pinned in `go.mod` (currently `toolchain go1.26.7`); `GOTOOLCHAIN=local` in CI so a mismatched toolchain fails instead of downloading another one. |
| Make | GNU Make. |
| Git | Used only for version/commit metadata; builds work without it, the metadata degrades to `dev`/`unknown`. |
| Network | Not required once the Stage 01 `vendor/` tree is included in the checkout; `make vendor` regenerates it. |
| Agent CLI | Not required and not permitted. No installed coding-agent CLI, provider account, token or secret may be needed by any target. |

## 2. Clean-checkout setup

```bash
go version            # must equal the `toolchain` line in go.mod
make help
make check            # fmt-check, vet, unit, integration, build + help/version
```

Vendor mode: `go.mod` declares `go 1.26.0` (≥ 1.14), so Go selects `-mod=vendor` by default when `vendor/modules.txt` exists. The Stage 01 deliverable includes that tree; a checkout containing it builds and tests offline.

CI forces `GOFLAGS=-mod=vendor`, `GOPROXY=off` and `GOSUMDB=off` so attempted downloads fail loudly. It caches only `~/.cache/go-build`. No step installs or launches a coding-agent CLI, and no step reads a secret.

## 3. Target reference

| Target | Underlying command | Notes |
|---|---|---|
| `make build` | `go build -trimpath -ldflags ... -o bin/asd ./cmd/asd` | Host platform. |
| `make smoke` | `make build` + `bin/asd --help` + `bin/asd --version` | Stage 01 acceptance check. |
| `make fmt` | `gofmt -w` over the same package dirs as `fmt-check` | Rewrites sources, including integration-tagged files. |
| `make fmt-check` | `gofmt -l` over `go list -tags=integration -f '{{.Dir}}' ./...` | CI gate; never descends into `vendor/`, and covers integration-tagged files. |
| `make lint` | `go vet -tags=integration ./...` | Covers unit and integration sources in one pass. |
| `make test` | `go test -count=1 -timeout 5m ./...` | Unit only. |
| `make test-integration` | `go test -count=1 -tags=integration -timeout 10m ./...` | Linux-only; skips with a message elsewhere. |
| `make test-race` | `go test -count=1 -tags=integration -race -timeout 15m ./...` | Unit and Linux integration tests under the race detector. |
| `make build-arm64` | `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build ./...` | Compile check only — see §7. |
| `make check` | fmt-check, lint, test, test-integration, smoke | The local mirror of CI. |
| `make vendor` | `go mod vendor` | Coordinator-only: refreshes `vendor/`; may fetch modules missing from the local module cache. |
| `make clean` | `rm -rf bin` + `go clean -testcache` | Keeps the shared build cache; use `go clean -cache` to drop it. |

Overridable variables: `GO`, `PACKAGES`, `BIN_DIR`, `BINARY`, `CMD`,
`TEST_FLAGS`, `EXTRA_TEST_FLAGS`, `UNIT_TIMEOUT`, `INTEGRATION_TIMEOUT`,
`RACE_TIMEOUT`, `INTEGRATION_TAG`, `VERSION`, `COMMIT`, `DATE`,
`LDFLAGS`, `SOURCE_DATE_EPOCH`.

```bash
make test PACKAGES=./internal/...
make test-integration EXTRA_TEST_FLAGS='-run TestPTYHandshake -v'
make test-race                                       # unit and integration with -race
SOURCE_DATE_EPOCH=1700000000 make build           # deterministic build date
```

### Build metadata

`make build` stamps the binary with three linker variables declared in
`internal/app`:

```text
-X <module>/internal/app.buildVersion=$(VERSION)   # git describe --tags --always --dirty
-X <module>/internal/app.buildCommit=$(COMMIT)     # git rev-parse --short HEAD
-X <module>/internal/app.buildDate=$(DATE)         # UTC, honours SOURCE_DATE_EPOCH
```

`<module>` is read from `go list -m`, so the module path is never hard-coded in
the Makefile. `make build` fails fast if that lookup returns nothing, because
otherwise the symbols would silently miss their targets. The CLI exposes them
through `asd --version`.

## 4. Test layers

### 4.1 Unit tests — `make test`

Unit tests have no build tag. The current `internal/app` suite pins the minimal CLI help/version and unknown-command behavior; product-domain coverage is added by the stages that introduce those packages. Keep unit tests hermetic, fast and sleep-free.

### 4.2 Integration tests — `make test-integration`

Files guarded by `//go:build integration`. They exercise the real PTY,
process-group, signal and resize behaviour against the fake agent.

**Platform.** Linux only. The target prints
`test-integration: SKIP, Linux only (host is ...)` and exits 0 on other
platforms, because the product is Linux-first and those platforms are not
supported yet — the skip is a statement of support, not a pass.

**TTY.** Integration tests allocate their own PTY for each child (the pinned
`github.com/creack/pty v1.1.24` dependency) and must drive it through the
`creack/pty` API. Nothing may read from the developer's or the runner's
controlling terminal, so no interactive TTY is required — locally, in a
terminal-less shell, or on a CI runner. If a future test genuinely needs a
controlling terminal, run it under `script -qec` and document that; the current
suite and CI do not do this, and CI does not emulate a TTY.

**Isolation.** Each test creates its own temporary workspace and its own
temporary state/config home, and points the child process at them. Tests must
not read or write the developer's real `$HOME`, must not depend on ambient
`XDG_*` values, and must not share state through fixed paths under `/tmp`.

**Timeouts.** Every run has an explicit `-timeout` (10m for integration, 15m
for race); a hung test fails with a goroutine dump rather than stalling. The CI
job additionally has a 20-minute `timeout-minutes` ceiling. Integration tests
must reap their children on the success path, on assertion failure and on
timeout, so a killed run leaves no live fixture.

**Cleanup by identity.** Record the PID (plus start time, to avoid PID reuse)
of every process and PTY a test starts, and signal exactly those. Never use
`pkill`/`killall` by executable name, and never sweep `/tmp` with a wildcard —
a real coding-agent session started by a developer on the same machine must
survive the test run untouched.

### 4.3 Race detector — `make test-race`

`make test-race` requires cgo and a C toolchain (available on supported Linux
runners) and runs both unit and `integration`-tagged tests under `-race`. It is the
standard race gate; `make test-integration EXTRA_TEST_FLAGS=-race` is unnecessary.

## 5. Fake agent

`tests/testagent/` is the deterministic stand-in for a real coding-agent CLI. `tests/testagent/README.md` documents the wire protocol, mode matrix and test commands. The test harness compiles the fixture once in `TestMain` with `GOPROXY=off` and `GOFLAGS=-mod=vendor`.

The integration suite is Linux-only and carries `//go:build linux && integration`. Run it with `make test-integration` or:

```bash 
GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=vendor go test -tags=integration ./tests/testagent -count=1
```

Each fixture starts in a fresh temporary workspace with private `HOME`, `XDG_CONFIG_HOME`, `XDG_STATE_HOME` and `XDG_CACHE_HOME`; `t.Cleanup` removes the temporary tree. The control channel is a full-duplex `AF_UNIX` stream on fd 3 using newline-delimited JSON. `stdin/stdout/stderr` remain on the PTY; line mode and echo-mode tests assert terminal/control separation. Waits are deadline-bound, child cleanup uses `exec.Cmd` process handles, and the suite audits process start times plus PTY identity. No CLI, account, credential or user configuration is read.

## 6. CI mapping

[`.github/workflows/ci.yml`](../../.github/workflows/ci.yml) runs on
`ubuntu-latest` with Go 1.26.7, `permissions: contents: read`, a 20-minute
ceiling, and one job in this order:

| CI step | Local command |
|---|---|
| Require committed vendored dependencies | `test -f vendor/modules.txt` |
| Report toolchain | `go version` |
| Check formatting | `make fmt-check` |
| Vet | `make lint` |
| Unit tests | `make test` |
| Integration tests | `make test-integration` |
| Race detector | `make test-race` |
| Build and run help/version | `make smoke` |
| linux/arm64 compile check | `make build-arm64` |

Full local parity: `make check && make test-race && make build-arm64`.

The only thing cached is `~/.cache/go-build`, keyed on the runner, the Go
version and the hash of `go.mod`/`go.sum`. No agent CLI, agent configuration,
credential or token is stored, and nothing is downloaded during a run.

## 7. Cross-compilation: what is and is not claimed

`make build-arm64` sets `GOOS=linux GOARCH=arm64 CGO_ENABLED=0` and compiles
every package. It does **not** execute anything arm64, and no arm64 runtime
behaviour — terminal handling, PTY sizing, signal delivery — has been verified.
Any claim that arm64 is "tested" requires an arm64 host or emulator run and
recorded evidence; until then the honest statement is "linux/arm64 compiles".

## 8. Current state of the baseline

- `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=vendor make check` passes on Linux with Go 1.26.7, including formatting, vet, unit tests, fake-agent integration tests, help/version smoke.
- `make test-race` passes for unit and tagged integration tests.
- `make build-arm64` passes as a compile-only `linux/arm64` check; no arm64 runtime claim.
- `vendor/` is generated as part of Stage 01. Include it with the change so clean checkouts can use the offline path.
- `bin/` is the build output directory and is ignored by `.gitignore`.

## 9. Troubleshooting

| Symptom | Cause and fix |
|---|---|
| `go: inconsistent vendoring ... not marked as explicit in vendor/modules.txt` | `vendor/` is missing or stale. Run `make vendor` and include the refreshed tree; do not hand-edit it. |
| `go: ... requires go >= X (running go Y)` | Local toolchain differs from the `toolchain` line in `go.mod`. Install the pinned toolchain, or drop `GOTOOLCHAIN=local` locally to let Go fetch it. |
| `test-integration: SKIP, Linux only` | Running on a non-Linux host. Integration coverage only exists on Linux. |
| `WARNING no integration-tagged files found` | The `integration` tag was not applied or tagged test files are missing; check the build tags and `make test-integration`. |
| Integration test hangs | The test waited on a fixed sleep instead of the fake agent's handshake (§5), or the fake agent is missing the mode the test needs. |
| Leftover processes after a test run | A test is not reaping by fixture identity; check `t.Cleanup`/`defer` and never `pkill` by name (§4.2). |
| `make build` prints `cannot read the module path from go.mod` | `go list -m` failed; `go.mod` is broken or the Go toolchain is missing. Fix `go.mod` (coordinator-owned) — the Makefile derives the symbol path from it. |

## 10. Outstanding decisions

Current decisions and remaining verification limits:

1. **License — MIT selected by the maintainer after Stage 01.** The root `LICENSE` is canonical; include it in source distributions and release artifacts.
2. **Module path verified for current origin.** `go.mod` declares `github.com/ryantr-statinops/agent-sessions-deck`, matching the inspected `origin` URL.
   If the repository is renamed or transferred, the coordinator regenerates
   `go.mod`, `go.sum` and `vendor/` together.
3. **Fake-agent baseline — implemented.** Stage 01 has Linux integration coverage for the 12 deterministic modes, PTY control/data isolation, process identity and teardown. Later app/runtime stages add their own integration tests.
4. **arm64 runtime — unverified.** Compile-only (§7).
