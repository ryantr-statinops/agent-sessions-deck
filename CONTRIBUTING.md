# Contributing to Agent Session Deck

Agent Session Deck (`asd`) is a Linux-first Go CLI for driving coding-agent
sessions from a local terminal. The product contract is [PRODUCT.md](PRODUCT.md);
the staged plan is [docs/plan/implementation/](docs/plan/implementation/README.md).
This file covers how to build, check and hand off changes.

## 1. Prerequisites

| Requirement | Value |
|---|---|
| OS | Linux (integration tests are Linux-only; see below) |
| Go | the toolchain pinned in [`go.mod`](go.mod) — currently `go1.26.7` |
| Build tool | GNU Make |
| VCS | Git (used for build metadata) |
| Network | not needed once dependencies are vendored |

No coding-agent CLI, provider account, token or secret is required to build or
test this repository. Tests must never need one.

## 2. Setup from a clean checkout

```bash
git clone <repo> && cd agent-sessions-deck
go version          # must match the `toolchain` line in go.mod
make help           # list every target
make check          # fmt-check + vet + unit + integration + build smoke test
```

`make check` mirrors the CI check sequence: formatting, vet, unit tests,
integration tests, then a build with `--help`/`--version`. Add
`make test-race` and `make build-arm64` for full CI parity.

Dependencies come from the `vendor/` tree included with the Stage 01 source change. Go selects vendor mode by
itself (the `go` directive in `go.mod` is ≥ 1.14 and `vendor/modules.txt`
exists), so a checkout containing the tree builds and tests offline. CI additionally forces
`GOFLAGS=-mod=vendor` and `GOPROXY=off` so that an accidental download fails
loudly instead of silently succeeding.

Build output goes to `bin/asd` (override with `make build BIN_DIR=...`).

## 3. Everyday commands

| Command | What it does |
|---|---|
| `make build` | Build `bin/asd` for the host with version/commit/date metadata |
| `make smoke` | Build, then run `--help` and `--version` (Stage 01 acceptance) |
| `make test` | Unit tests |
| `make test-integration` | Integration tests (Linux-only, fake agent) |
| `make test-race` | Unit and Linux integration tests under `-race` |
| `make lint` | `go vet` over unit and integration sources |
| `make fmt` | Rewrite sources with gofmt |
| `make fmt-check` | Fail if any source is not gofmt-formatted |
| `make build-arm64` | Compile-only check for `linux/arm64` |
| `make check` | fmt-check, lint, test, test-integration, smoke |
| `make clean` | Remove `bin/` and the Go test cache |
| `make vendor` | Coordinator-only: refresh `vendor/`; may fetch modules missing from the cache. |

Useful overrides:

```bash
make test PACKAGES=./internal/...        # narrow the package set
make test-integration EXTRA_TEST_FLAGS='-run TestPTY -v'
make build VERSION=1.2.3                 # stamp an explicit version
SOURCE_DATE_EPOCH=1700000000 make build  # deterministic build date
```

`docs/testing/development.md` is the detailed guide: test layers, integration
requirements, the fake agent and the CI mapping.

## 4. Test conventions

- **Unit tests** are plain `go test` files with no build tag. They must be
  fast, hermetic and free of sleeps.
- **Integration tests** carry `//go:build linux && integration` and run through `make test-integration`. They exercise the real PTY, process, signal and resize behavior against the fake agent in `tests/testagent/`.
- `make test-race` runs both unit and integration tests under `-race`.
- The fake agent replaces real coding agents. Tests must not discover, launch
  or require Claude Code, Codex, OpenCode, or any other installed CLI, and must
  not use agent accounts or credentials.
- Synchronize through a control pipe/handshake with the fake agent, not fixed
  sleeps.
- Give every test its own temporary workspace and state home; never write under
  the developer's real `$HOME`.
- Reap only processes the test itself started, by fixture identity. Never use
  `pkill` by executable name, and never sweep `/tmp` by wildcard.
- Every test must clean up its own PTYs and child processes, including on
  failure — use `t.Cleanup` and defer.

## 5. Code style

- `gofmt` is the only formatter; `make fmt-check` is a CI gate.
- `go vet` must stay clean (`make lint`).
- Do not add abstractions, packages or interfaces without code that needs
  them. Empty scaffolding is a review blocker.
- Keep the module dependency set small and pinned. Adding a dependency is a
  coordinator decision: it touches `go.mod`, `go.sum` and `vendor/`.
- Linux is the target platform. Do not add OS-abstraction layers for
  platforms the product does not support yet.
- Do not add global PATH installers, and do not modify the user's shell
  environment, from the product or from tests.

## 6. Repository ownership

`go.mod`, `go.sum`, `cmd/asd/`, `internal/app/` and the vendored tree are owned
by the coordinator. Do not edit them in a task that was not assigned them;
send a dependency or API request instead.

| Area | Owner |
|---|---|
| `go.mod`, `go.sum`, `vendor/`, `cmd/asd/main.go`, `internal/app/`, build wiring | coordinator |
| `Makefile`, `.github/workflows/ci.yml`, `CONTRIBUTING.md`, `docs/testing/` | build/CI/docs |
| `internal/process`, `internal/pty`, `internal/terminal`, `internal/session` | runtime owner |
| `internal/providers`, `internal/discovery`, `internal/workspace`, `internal/git` | discovery owner |
| `internal/cli`, `internal/tui` | UI owner |
| `tests/testagent/` | test-agent owner |

Hand off with: files changed, commands run, remaining failures, and the API
assumptions you relied on.

## 7. Commits and review

- One coherent change per commit; describe the behaviour, not the diff.
- Run `make check` (plus `make test-race` and `make build-arm64` for
  concurrency- or platform-sensitive changes) before requesting review.
- Do not merge a change whose checks you have not run. If a check cannot run
  in your environment, say so explicitly in the handoff rather than claiming a
  pass.

## 8. Open decisions

These are deliberately unresolved. Do not "fix" them in passing, and do not
add a `LICENSE` file until the maintainer chooses one.

- **License: not chosen.** The maintainer decides the license before any
  public release. There is intentionally no `LICENSE` file in the repository
  yet, and none may be added by a contributor.
- **Module path verified for the current remote.** `go.mod` uses `github.com/ryantr-statinops/agent-sessions-deck`, matching the inspected `origin` URL. If the repository is renamed or transferred, the coordinator must regenerate `go.mod`, `go.sum` and `vendor/` together. The Makefile derives linker symbol paths from `go list -m`.

## 9. Where to look next

- [docs/testing/development.md](docs/testing/development.md) — setup, targets,
  test layers, fake agent, CI mapping, troubleshooting.
- [docs/architecture/adr/](docs/architecture/adr/) — the decisions behind the
  technical direction.
- [docs/testing/terminal-compatibility.md](docs/testing/terminal-compatibility.md)
  — Stage 00 terminal spike evidence.
- [docs/plan/implementation/01-repository-and-engineering-baseline.md](docs/plan/implementation/01-repository-and-engineering-baseline.md)
  — the current stage and its acceptance criteria.
