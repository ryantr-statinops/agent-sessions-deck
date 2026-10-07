# Stage 04 — Discovery, providers and workspaces

Trạng thái: PLANNED (implementation complete, awaiting coordinator acceptance) · Milestone: M1 · Phụ thuộc: 02, 03 · Coordinator review pending.

## Kết quả

- `internal/discovery`: PATH và `extra_paths` scan nhất quán precedence (PATH trước extra_paths), canonical executable checks, dedupe theo tên và theo canonical symlink target, không scan toàn filesystem; bounded probe (`--version` 2s timeout, output cap 64KB→4096B mặc định, concurrency 4, TTL cache 30s, stdin null, không TTY); configured overrides được validate grammar/id unicity và báo collision name/ID thay vì launch binary bất ngờ; detection trả `available/not-found/invalid/uncertain` + reason + provenance output, `RequireMarker` yêu cầu marker positive — không ép available từ tên binary (OMP/Orca screen-reader collision được ghi rõ trong matrix).
- `internal/providers/generic`: `Resolve`/`LaunchSpec` với literal argv, extra args passthrough, workspace dir là cwd, `ValidateForLaunch` (absolute executable) bắt buộc tại boundary; fixture chứng minh `;`, `$(id)`, backticks không bị shell eval.
- `internal/providers/{claude,codex,opencode}`: adapter riêng, Capabilities chỉ core launch/interactive, `Verify` qua `RequireMarker` trên output `--version` (không suy từ tên binary); `Resolve` literal argv + `ValidateForLaunch`.
- `internal/workspace`: `PathResolver{Home, GitProbe}` — tilde expansion (`~`, `~/`, `~user` rejected), relative→cwd, `EvalSymlinks` canonical identity, validate tồn tại/dir/readability, `ResolveFirst` theo Source priority (explicit → current → configured → recent); fixture: nested cwd, symlink, missing, file-as-dir, unreadable, priority, git probe attach.
- `internal/git`: `Client{Timeout 2s, TTL 5s, MaxInFlight 4}` với cache TTL, `Invalidate`, cancel theo caller context (không poison cache khi cancel), `Probe` ghi `workspace.Git{Root,Branch,Detached,Dirty,ChangedFiles}`, `DecodeWorktree` phân biệt linked worktree/bare, `CountPorcelain` đếm record NUL-delimited (`porcelain=v1 -z`): rename/copy consume field phụ, newline/space filename, untracked, unmerged; fixture normal repo, nested cwd, detached HEAD, linked worktree, bare, non-repo, slow-binary cancel.
- `docs/providers.md`: compatibility matrix (version/executable identity/launch args/terminal coverage/native capabilities/verification timestamps), `omp`/`orca`/`aider` nằm ngoài supported list đến khi có evidence.

## Verification

Chạy trên Linux, offline (vendored), không ghi state thật:

- Targeted: `go test ./internal/discovery/ ./internal/providers/... ./internal/workspace/ ./internal/git/` — PASS.
- Throwaway discovery/workspace/Git smoke (in-module, added-run-deleted, không có test tên `smoke`): scan tìm fake executables, impostor `claude` bị `Verify` reject (`identity marker not observed`), marker output chấp nhận, generic LaunchSpec cwd đúng workspace, literal argv passthrough, `workspace.Git` probe nhận `main`/dirty/1 changed — `SMOKE PASS`.
- `make check` — PASS tại HEAD `98cc637` (fmt-check, go vet, unit, integration, `bin/asd --help/--version`).
- `make test-race` — PASS tại HEAD `98cc637`.
- Model thực tế của run này: `opencode/fledge-alpha-free` (OpenCode configured default; Orca receipt `model: null`; không override).

## Handoff

- **06–08:** `agent.Provider` implementations sẵn sàng đăng ký qua `agent.MapRegistry`; workspace resolution qua `workspace.PathResolver`; git metadata qua `git.Client.Probe` (`workspace.Git`); detection status contract trong `internal/discovery`. Provider real-smoke (vendor binary thật) deferred đến Stage 10 gate.
- **Commits:** `ded3dc7` discovery scan; `52e73ad` discovery probes; `86efbb4` configured validation; `60efade` generic provider; `367bb34` gofmt; `e6bed20` workspace PathResolver; `c26d634` git client+parser; `df2dcc8` claude/codex/opencode adapters + case-insensitive marker; `98cc637` docs/providers.md. Stage 03 gate commits: `5e9c888` (report), `3409ec5` (plan DONE).
- **Remaining limits:** `omp`/`orca`/`aider` adapters không có (needs verified marker + Stage 10 smoke); marker match là substring case-insensitive, chưa có version-range parsing; git cache TTL 5s có thể trả stale trong cửa sổ TTL; Stage 04 giữ nguyên `PLANNED` cho tới khi coordinator acceptance đóng.

## Review

Awaiting coordinator review of fixtures, matrix and deferred items. Stage 04/05 may now be considered implemented for W1 contract review; Stage 06 remains blocked on Stage 05.
