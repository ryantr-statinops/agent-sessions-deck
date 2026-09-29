# Stage 01 — Repository and engineering baseline

Trạng thái: `DONE` · Phụ thuộc: Stage 00 · Handoff: Stage 02.

## Deliverables

- Go module `github.com/ryantr-statinops/agent-sessions-deck`, pinned Go 1.26.7 toolchain, minimal Cobra CLI, version/build metadata, `--help` and `--version`.
- Vendored dependency tree, Makefile targets for build/check/test/integration/race/vendor/clean, and offline GitHub Actions CI with amd64 checks and arm64 compile-only coverage.
- Linux-only fake-agent fixture with full-duplex JSONL control channel, PTY isolation, 12 deterministic modes, private temporary workspace and XDG state/config/cache homes, and process/PTY leak auditing.
- Developer and contributor instructions; module identity recorded as matching verified `origin`. License was pending when Stage 01 completed; the maintainer subsequently selected MIT and added root `LICENSE`.

## Verification evidence

All repository checks ran with `GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOFLAGS=-mod=vendor` unless shown otherwise.

| Command | Result |
|---|---|
| `make vendor` | Passed; generated `vendor/` from `go.mod`. |
| `make fmt` | Passed; formatted the unit and integration-tagged Go sources. |
| `make check` | Passed: `fmt-check`, `go vet -tags=integration`, unit tests, tagged Linux integration tests, binary build, `--help`, and `--version`. |
| `make test-race` | Passed: unit and integration suites under `-race`. |
| `make build-arm64` | Passed compile-only for `linux/arm64`; no arm64 runtime was executed. |
| `make clean` | Passed; removed `bin/` and the Go test cache after the smoke run. |

The fake-agent integration suite also passed directly with `go test -tags=integration ./tests/testagent -count=1 -timeout 480s`. Its `TestMain` leak audit passed after the suite. A control-reader regression test verifies that a partially read JSON record is preserved across a timeout.

## Review fixes incorporated

- Exactly one `exec.Cmd.Wait` per fixture; all waiters read the cached process state.
- Process start times and PTY file identity retained through teardown; descendant cleanup uses the original `exec.Cmd` process handle, not name-based signaling or recycled PIDs.
- Persistent newline reader preserves partial records after deadline errors; control writes have bounded deadlines.
- Slow-start handles closed input without spinning; helper reads reuse one decoder and EOF waits do not busy-loop.
- Echo/line tests assert control JSON does not leak onto the PTY.

## Limits and handoff

- Integration tests require Linux. The Go module and fake agent are reusable by later stages; there is no session/runtime product implementation in Stage 01.
- License selection remains a maintainer decision before public release.
- Stage 02 can build on `cmd/asd/`, `internal/app`, the module path, Makefile/CI targets, and `tests/testagent/` control protocol. The default `make test` remains offline and does not launch a real agent or require accounts.
