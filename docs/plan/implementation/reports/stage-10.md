# Stage 10 — V1 Acceptance và Release

Status: **COMPLETE** · Milestone: **M2 / V1 Release (Gate PASSED)** · Dependencies: Stage 04, Stage 07, Stage 08, Stage 09.

## Implemented & Verified

- **Performance Benchmarks & Profiling Verification (`internal/discovery`, `internal/workspace`, `internal/tui/terminal`, `tests/stage10_performance_test.go`)**:
  - `BenchmarkPathResolver`: Git workspace discovery & branch probe resolves in **6.8 µs/op** (budget: < 50ms).
  - `BenchmarkScan`: Executable filesystem scanning & PATH resolution in **67.7 µs/op** (budget: < 50ms).
  - `BenchmarkEncodeKey`: Interactive keyboard routing & ANSI VT key encoding in **84.6 ns/op** (budget: < 1ms).
  - `BenchmarkRenderScreen`: In-process 80x24 VT screen rendering with run-length attribute styling in **30.7 µs/screen** (delivering > 32,000 FPS capability, budget: 60 FPS / 16ms).
  - `BenchmarkScrollbackRingPush`: High-throughput ring buffer append in **31.3 ns/op**.
  - `TestMemoryAllocationBoundsUnderWorkload`: Memory delta capped at **~62 MB** for 10,000 fully styled lines (well within the 100MB allocation budget, system allowance 612 MB).

- **Concrete Provider Verification Matrix (`tests/stage10_providers_test.go`, `tests/stage10_providers_smoke_test.go`, `docs/providers.md`)**:
  - Identity marker verification suite for `claude`, `codex`, `opencode`, and fallback `generic`.
  - Negative marker defense rejecting name-based lookalike collisions (`omp` media player, `orca` screen reader, generic unverified binaries).
  - Interactive terminal smoke test executing concurrent PTY allocation, I/O streaming, frame processing, and graceful shutdown across concrete providers.

- **Clean-State & E2E Acceptance Suite (`tests/stage10_clean_state_e2e_test.go`, `tests/stage10_multisession_lifecycle_e2e_test.go`, `tests/stage10_cli_contract_test.go`, `tests/stage10_negative_resilience_e2e_test.go`, `tests/stage10_dogfood_test.go`)**:
  - Fresh install clean-state testing: verifying empty state guide ("No sessions found. Press 'n' to launch..."), agent auto-discovery, and empty JSON responses without configuration files.
  - Multi-session lifecycle testing: end-to-end launch (Git workspace and plain folder) -> interactive PTY -> detach -> switch -> rename -> stop -> restart with generation increment -> explicit kill.
  - Complete V1 CLI contract suite: all 10 commands (`asd`, `scan`, `list`, `new`, `open`, `inspect`, `rename`, `restart`, `stop`, `kill`), `--json` formatting, `--force` and `--yes` safety protections.
  - Negative resilience testing: actionable error handling without crashes on missing agents, invalid YAML configs, permission-denied directories, immediate child exit failures, and slow/cancelling Git probes.
  - Dogfooding verification: multi-agent concurrent workflow running simultaneous code-reviewer and test-runner sessions without cross-talk.

- **Release Packaging & CI Automation (`scripts/package-release.sh`, `Makefile`, `.github/workflows/release.yml`)**:
  - Automated release packager generating distribution archives for `linux/amd64` and `linux/arm64`.
  - Tarballs bundle `asd` binary, `LICENSE`, `README.md`, and `config.example.yaml`.
  - Automated SHA-256 checksum generation (`dist/sha256sums.txt`) and verification.
  - Makefile targets: `make release-artifacts` and `make checksums`.
  - GitHub Actions release workflow triggering on `v*.*.*` git tags with automated asset uploads.

- **Documentation & User Guides (`docs/installation.md`, `docs/configuration.md`, `docs/testing/v1-acceptance.md`, `CHANGELOG.md`, `config.example.yaml`)**:
  - Comprehensive installation, system requirements, XDG directory layout, backup/reset, and uninstallation guide.
  - Full V1 acceptance matrix in `docs/testing/v1-acceptance.md`.
  - Release notes in `CHANGELOG.md`.

## Verification Evidence

1. **Local Test & Quality Gate (`make check`)**:
   - `fmt-check`: OK.
   - `lint (go vet)`: OK.
   - `test`: All unit tests PASS (100%).
   - `test-integration`: All integration tests PASS, including Stage 08 TUI terminal tests, Stage 09 recovery suite, and Stage 10 E2E/provider/dogfooding suites.
   - `smoke`: Build and CLI smoke test OK.
2. **Release Artifacts & Checksums**:
   - `make checksums`:
     - `asd_<version>_linux_amd64.tar.gz: OK`
     - `asd_<version>_linux_arm64.tar.gz: OK`

## Boundary Notes (V1)
- V1 provides a single foreground owner architecture. Background daemonization, multi-workspace dashboards, and MCP server control planes remain reserved for V2/V3 (Stages 11-15).
- All V1 acceptance criteria and M2 milestone goals are fully met.
