# Stage 04 — Discovery, providers and workspaces

Trạng thái: DONE (acceptance verified and integrated into `dev`; Wave 1 close recorded in orchestration ledger) · Milestone: M1 · Phụ thuộc: 02, 03.

## Kết quả

- `internal/discovery`: PATH và `extra_paths` scan nhất quán precedence (PATH trước extra_paths), canonical executable checks, dedupe theo tên và theo canonical symlink target, không scan toàn filesystem; bounded probe (`--version` 2s timeout, output cap 64KB→4096B mặc định, concurrency 4, TTL cache 30s, stdin null, không TTY); configured overrides được validate grammar/id unicity và báo collision name/ID thay vì launch binary bất ngờ; detection trả `available/not-found/invalid/uncertain` + reason + provenance output, `RequireMarker` yêu cầu marker positive — không ép available từ tên binary (OMP/Orca screen-reader collision được ghi rõ trong matrix).
- `internal/providers/generic`: `Resolve`/`LaunchSpec` với literal argv, extra args passthrough, workspace dir là cwd, `ValidateForLaunch` (absolute executable) bắt buộc tại boundary; fixture chứng minh `;`, `$(id)`, backticks không bị shell eval.
- `internal/providers/{claude,codex,opencode}`: adapter riêng, Capabilities chỉ core launch/interactive, `Verify` qua `RequireMarker` trên output `--version` (không suy từ tên binary); `Resolve` literal argv + `ValidateForLaunch`.
- `internal/workspace`: `PathResolver{Home, GitProbe}` — tilde expansion (`~`, `~/`, `~user` rejected), relative→cwd, `EvalSymlinks` canonical identity, validate tồn tại/dir/readability, `ResolveFirst` theo Source priority (explicit → current → configured → recent); fixture: nested cwd, symlink, missing, file-as-dir, unreadable, priority, git probe attach.
- `internal/git`: `Client{Timeout 2s, TTL 5s, MaxInFlight 4}` với cache TTL, `Invalidate`, cancel theo caller context (không poison cache khi cancel), `Probe` ghi `workspace.Git{Root,Branch,Detached,Dirty,ChangedFiles}`, `DecodeWorktree` phân biệt linked worktree/bare, `CountPorcelain` đếm record NUL-delimited (`porcelain=v1 -z`): rename/copy consume field phụ, newline/space filename, untracked, unmerged; fixture normal repo, nested cwd, detached HEAD, linked worktree, bare, non-repo, slow-binary cancel.
- `docs/providers.md`: compatibility matrix (version/executable identity/launch args/terminal coverage/native capabilities/verification timestamps), `omp`/`orca`/`aider` nằm ngoài supported list đến khi có evidence.


## Coordinator review

- `discovery.Scan` now uses process `PATH` when `Options.Path` is empty. Configured `ExtraPaths` expand only `~` and `~/...`; `~user` is not treated as a home-relative path, and `Home` does not rewrite process `PATH` entries.
- Probe output now uses one mutex-protected capped writer for stdout/stderr; race reproduced with the concurrent child fixture before the fix. A successful probe with empty output is `uncertain` with a reason.
- Configured validation rejects duplicate configured IDs/names, IDs that collide with a registered provider, and display names that reuse a built-in stable ID. `agent.Registry` exposes stable IDs, not a separate built-in display-name field.
- Git status parsing counts only complete NUL-terminated records. `DecodeWorktree` now propagates symlink-resolution and bare-probe errors instead of returning a false classification.
- `PathResolver` keeps a valid workspace usable when optional Git metadata probing fails; metadata is attached only on success. Direct `git.Client` probes still return their errors.

## Verification

Chạy trên Linux, offline (vendored), không ghi state thật:

- Targeted: `go test ./internal/discovery ./internal/providers/... ./internal/workspace ./internal/git` — PASS at `fd5c98b`.
- Red/green regressions: process-PATH fallback, empty probe output, concurrent stdout/stderr race, safe tilde expansion, configured built-in ID/name collisions, truncated Git records and worktree metadata errors all failed before their fix and pass after.
- Throwaway behavioral smoke (added-run-deleted): `SMOKE PASS path=path extra=extra_paths probe=available empty=uncertain git-records=1/0`.
- Provider-collision smoke: `SMOKE PASS configured agent collision: configured agent name "claude" collides with built-in provider id "claude"`.
- `make check && make test-race` — PASS at `69152bc` (fmt-check, vet, unit, integration, build/help/version smoke, and integration race suite); the later `fd5c98b` commit only corrects a source comment.
- Original Stage 04 worker model: `opencode/fledge-alpha-free` (configured default; no model override).

## Handoff

- **06–08:** `agent.Provider` implementations sẵn sàng đăng ký qua `agent.MapRegistry`; workspace resolution qua `workspace.PathResolver`; git metadata qua `git.Client.Probe` (`workspace.Git`); detection status contract trong `internal/discovery`. Provider real-smoke (vendor binary thật) deferred đến Stage 10 gate.
- **Implementation commits:** `ded3dc7` discovery scan; `52e73ad` probes; `86efbb4` configured validation; `60efade` generic provider; `367bb34` gofmt; `e6bed20` workspace resolver; `c26d634` Git client/parser; `df2dcc8` Claude/Codex/OpenCode adapters; `98cc637` provider matrix. Stage 03 gate: `5e9c888` report, `3409ec5` status.
- **Coordinator review commits:** `947b538` process PATH fallback; `38eda70` synchronized probe capture; `2fe4354` empty-output uncertainty; `e800659` registry collision coverage; `73a7a37` safe tilde expansion; `7e511d2` complete porcelain records; `806d1be` worktree error propagation; `c49b048` test spacing; `69152bc` built-in name collision; `fd5c98b` correct Git output-capture comment.
- **Remaining limits:** `omp`/`orca`/`aider` lack verified adapters and remain deferred to Stage 10; vendor real-binary smoke is deferred to Stage 10; marker matching remains case-insensitive substring matching without version-range parsing; Git cache TTL is 5s; `git.Client.run` buffers full stdout/stderr without an output cap. `PathResolver` omits Git metadata when its optional probe fails; it does not carry probe errors in `Workspace`. Stage 04 is accepted and integrated into `dev`; its isolated branch tip is reachable from the integrated history.

## Review

Coordinator acceptance completed on `work/w1-stage04-acceptance`; its tip commit is integrated into `dev`. Stage 06 completed its acceptance and Wave 1 is closed after Stage 03–06 gates passed.
