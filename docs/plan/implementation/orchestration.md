# Kế hoạch orchestration triển khai

## Mục đích và trạng thái

Tài liệu này gộp các stage tương lai thành execution wave có dependency rõ và cách giao việc cho Orca. Stage 00–02 đã hoàn tất; Stage 03–05 có report/status riêng và chỉ được giữ `DONE` khi gate/evidence tương ứng pass. Wave chỉ `DONE` khi mọi gate của wave và các stage con tương ứng có evidence; việc merge code hoặc worker báo xong chưa đủ.

**Ưu tiên:** hoàn thành M1–M2 để giao V1 qua Stage 10; chỉ mở M3/V2 và M4/V3 sau khi xem lại nhu cầu thực tế, như implementation README đã yêu cầu.

## Bốn execution wave

| Wave | Stage được gộp | Mục tiêu/gate | DAG điều phối |
|---|---|---|---|
| **W1 — Core V1 runtime (M1)** | 03–06 | Config/store an toàn; discovery/workspace và runtime chạy được; CLI/IPC điều khiển một owner. Gate: fake agent chạy qua CLI, đọc trạng thái từ terminal khác; toàn bộ contract/retry/ownership acceptance của 06 pass. | `03 → (04 ∥ 05) → 06`. Đóng băng config DTO, store/lock API và path/failure policy trước khi 04/05 cùng chạy. 06 chỉ bắt đầu sau khi 04 và 05 hoàn tất. |
| **W2 — V1 UX và release (M2)** | 07–10 | Hoàn thành launcher/dashboard, terminal tương tác, recovery; qua V1 acceptance trên Linux. Gate: Stage 10 có end-to-end evidence, concrete provider matrix và release limitations chính xác. | `07 → 08 → 09 → 10`. 09 cần 06+08; 10 cần 04+07+08+09. Có thể chạy docs/QA bounded song song nhưng không được bỏ các dependency này. |
| **W3 — V2 daemon và history** | 11–13 | Chuyển owner sang daemon, sau đó workspace/metrics và event/history persistence. Gate V2 chỉ đóng sau acceptance của cả 12 và 13. | `11 → (12 ∥ 13-core) → 13/V2-gate`. Stage 13 có thể phát triển core sau 11; không báo stage hoặc wave hoàn tất trước khi dependency Stage 12 và V2 acceptance đều pass. |
| **W4 — V3 MCP và profiles** | 14–15 | Thêm MCP có policy và profiles/automation opt-in. Gate V3 gồm security/policy acceptance của 14, profile/automation acceptance của 15 và hồi quy V1/V2. | Sau 12+13: `(14 ∥ 15-profile-core) → 15/MCP-integration-and-V3-gate`. Stage 15 không bật MCP profile tools hoặc đóng V3 trước khi Stage 14 hoàn tất. |

Stage IDs và từng file stage vẫn là nguồn acceptance chi tiết. Wave là đơn vị lập kế hoạch/điều phối, không nới scope, không thay dependency ghi trong stage, không gộp acceptance thành một kiểm tra mơ hồ. Stage 03–15 không được đổi `PLANNED` sang `DONE` trước khi implementation và evidence thực sự tồn tại.
Ký hiệu `∥` biểu thị node độc lập theo DAG, không tự chứng minh slot chạy đồng thời. Chỉ chạy song song khi có ít nhất hai worker slot `ready` đã xác minh, contract/file ownership đã freeze và mỗi worker có worktree riêng; merge vào `dev` sau khi các task settle. Kilo hiện đang `unverifiable` nên không được tính vào capacity.

## Worker capability và model gate

| Worker | Bằng chứng hiện có | Cách dùng trong kế hoạch |
|---|---|---|
| **OpenCode** | `worker-start --agent opencode` review và one-line ping đã thành công; worker báo model thực tế `opencode/fledge-alpha-free`. Receipt vẫn ghi `model: null`; Orca `--model` không áp dụng cho OpenCode. `worker-start --terminal` bị từ chối `agent_unconfigured`. | OpenCode có thể dùng model Fledge theo config hiện tại; chưa có cách pin per-dispatch model. Space Bunny Alpha vẫn không có trong direct catalog. |
| **Kilo Code** | `worker-start --agent kilo` trước đó khởi chạy terminal; task hiện tại được gọi với model mặc định và TUI hiển thị StepFun 3.7 Flash Free, nhưng dispatch `ctx_d9db5c0a1a51` là `failed/terminal_missing`, fleet liveness `unverifiable/missing_status`, không có `worker_done`. | User đã chọn Kilo default làm target W1, không override model; attempt này không chứng minh slot sẵn sàng. Không retry/replace cho tới khi có bằng chứng exit dương tính. |
| **Cline** | CLI và `/model` picker chạy trong Plan mode, auto-approve tắt; DeepSeek V4.1 Flash Free hiển thị. Chưa có `--agent` hay terminal-path Orca dispatch proof. | Chưa tính vào DAG Orca cho tới khi một trong hai route được kiểm chứng end-to-end. |
| **Freebuff** | CLI và `/model` picker chạy; Space Bunny Alpha hiển thị. Chưa có `--agent` hay terminal-path Orca dispatch proof. | Không đưa vào source-bearing task mặc định: provider ẩn danh có thể giữ prompt. Chỉ dùng cho source sau khi user chấp nhận chính sách dữ liệu và Orca route được xác minh. |
Các model mong muốn cho OpenCode cần giải quyết trước khi pin vào worker policy: catalog OpenCode trực tiếp hiện có `fledge-alpha-free`, `space-bunny-free`, `muse-spark-1.3-contributor-free`; không thấy `space-bunny-alpha` hoặc Muse Spark 1.4. Không tự thay model yêu cầu bằng model gần tên. Không ghi credentials/tokens vào plan hoặc task prompt.
Ma trận trên là evidence theo lần kiểm tra hiện tại, không phải cam kết hỗ trợ vĩnh viễn. Muốn thêm worker vào Orca DAG phải chứng minh một route thực tế (`--agent` hoặc `--terminal`) → launch/readiness pass → worker hoàn tất task bounded → output/diff đúng worktree → model/provider thực tế được xác định. Nếu thiếu bất kỳ bước nào, worker là `unverified` và không được tính vào capacity.
## Quy tắc tạo và điều phối task

Mỗi Orca task phải có đủ:

1. **Outcome quan sát được** và acceptance lấy từ stage; ghi rõ non-goals.
2. **Prerequisite**: task/stage nào phải `DONE` trước; không dispatch node đang blocked.
3. **Một owner** cùng allowlist file/package được sửa; không giao hai writer lên cùng shared contract/file.
4. **Contract** đã freeze hoặc quyết định cần coordinator chốt trước khi worker bắt đầu.
5. **Verification** cụ thể: behavior test, integration smoke hoặc artifact/evidence; reviewer không thay implementation acceptance.
6. **Handoff**: file/diff, lệnh đã chạy, kết quả quan sát, known limits, unresolved decision và trạng thái worker/task.

Coordinator giữ PRODUCT, shared DTO/API, dependency changes, task DAG, stage status và integration. Worker không tự mở rộng scope, không tự đổi contract chung, không tự đánh dấu stage `DONE`. Song song chỉ dùng cho các node độc lập sau khi interface/file ownership đã freeze. Mỗi failure giữ evidence; dependent tasks tiếp tục `blocked` cho tới khi coordinator xử lý blocker. Trước retry/replacement phải kiểm tra worker/task/terminal state để tránh chạy trùng side effect. Dùng Orca thật theo version-matched guide tại [skill orchestration](../../../.agents/skills/orchestration/SKILL.md); không mô phỏng worker handoff bằng CLI TUI hoặc subagent ngoài Orca.

## Wave 1 — execution plan (Stage 03–06)

### Dependency and worker assignment

Dependency remains `03 → (04 ∥ 05) → 06`. Stage 04 and Stage 05 acceptance pass and both branches are integrated into `dev`; Stage 06 is now actionable. W1 still requires Stage 06 contract/retry/ownership acceptance and the end-to-end gate.

- Worker target: `kilo`; bỏ `--model` để dùng default đã được user chọn. Mỗi dispatch phải ghi effective model và kết quả `ready`/completion; không coi model hiển thị ở terminal cũ là bằng chứng capacity hiện tại.
- Coordinator owns PRODUCT/ADR decisions, shared interfaces, `go.mod`/`go.sum`, acceptance, docs/status and integration. Parallel task branches use isolated worktrees with explicit allowlists; merge to `dev` only after their tasks settle, then verify `dev` before it is ready for a PR to `main`.
- When capacity is proven, Stage 05 may split only after contract freeze: `internal/terminal` parser/query core (W1-05.04) versus one process/PTY/session runtime owner; stream sequencing/queues, lifecycle and E2E remain under one integrator. Stage 06 IPC (`internal/ipc`) and CLI (`internal/cli`) may run in parallel after wire/API freeze; coordinator owns bootstrap, assembly and multi-terminal E2E. Without verified worker slots, run serially.

### Commit ledger — baseline 55 commits

This ledger decomposes the stage checklists into independently testable behavior slices. **Baseline: 55 future commits** — Stage 03: 12; Stage 04: 13; Stage 05: 15; Stage 06: 14; W1 close: 1. It excludes this planning-doc commit, previously pushed planning commits, bug-fix commits, and any coordinator-approved dependency change. One commit is not one file for code: implementation and its behavior tests stay together. Every documentation artifact below is a separate one-file commit; stage report and stage-checklist status are separate files and therefore separate commits.

#### Stage 03 — Config and persistent state (11 stage commits + status)

| ID | Commit-sized change and owned scope | Evidence before push |
|---|---|---|
| W1-03.01 | XDG config/state/runtime path resolution and defaults in internal/config; isolated temp-home path tests. | XDG overrides, absent dirs/defaults; prove no write to real home. |
| W1-03.02 | Config schema plus strict YAML validation: unknown keys, duplicate IDs, invalid durations and empty executable; actionable file/key errors. | Config parser behavior tests, valid and invalid fixtures. |
| W1-03.03 | Executable + argv model, explicit tilde path expansion and safe display/redaction; never shell-split/eval. | Literal shell metacharacters remain argv; injection fixture cannot execute a shell payload. |
| W1-03.04 | New directory/file permissions and state-home lock lifecycle. | 0700/0600 tests, preserve existing-file modes, owner lock and offline lock contention tests. |
| W1-03.05 | sessions.json envelope, strict codec, schema/revision and full snapshot commit API. | Round-trip, future-schema/corrupt-image and stale-revision behavior tests. |
| W1-03.06 | Independent state.json envelope/revision and recent-workspace API. | Recent update increments only state revision; reload and invalid-image tests. |
| W1-03.07 | Per-file atomic replace/fault handling for both stores. | Inject write/flush/rename failures; prior or complete new image survives, never half JSON; concurrent writers are refused. |
| W1-03.08 | Legacy import, destination precedence, backup/source preservation and migration. | Round-trip IDs/names/exit metadata; import only when XDG destination is absent; repeated start is safe. |
| W1-03.09 | config.example.yaml only. | Example parses under strict schema and matches defaults. |
| W1-03.10 | docs/configuration.md only. | Link check; documents XDG, schema, argv and permissions. |
| W1-03.11 | docs/plan/implementation/reports/stage-03.md only. | Record commands/evidence, migration/fault results and remaining limitations. |
| W1-03.12 | docs/plan/implementation/03-config-and-persistent-state.md only: mark checklist items/status from observed acceptance. | All Stage 03 acceptance passes in isolated home; make check passes. |

#### Stage 04 — Discovery, providers and workspaces (12 stage commits + status)

| ID | Commit-sized change and owned scope | Evidence before push |
|---|---|---|
| W1-04.01 | PATH/extra_paths discovery, canonical executable checks and symlink/binary dedupe in internal/discovery. | Missing, non-executable, spaced path, symlink and precedence fixtures. |
| W1-04.02 | Bounded identity/version probes, provenance, timeout, output cap, concurrency and cache. | Timeout/output-cap/duplicate-probe fixtures; no interactive probe. |
| W1-04.03 | Configured provider overrides and stable-ID/name collision handling. | Duplicate IDs/names fail deterministically; no unintended binary launch. |
| W1-04.04 | Generic provider BuildCommand and launch spec in internal/providers/generic. | Literal argv/workspace cwd and shell-injection fixtures. |
| W1-04.05 | Workspace source selection and canonical identity in internal/workspace. | Current/explicit/config/recent path, invalid directory, nested cwd and symlink fixtures. |
| W1-04.06 | Git root/branch/detached/worktree/bare/no-Git/permission metadata in internal/git. | Normal, nested, linked-worktree, detached and non-repo fixtures. |
| W1-04.07 | NUL-delimited Git changed-file parser and record counts. | Rename, untracked, unmerged and newline filenames; count records, not lines. |
| W1-04.08 | Claude provider adapter/launch spec only. | Identity and argv fixtures; unverified vendor capabilities remain unclaimed. |
| W1-04.09 | Codex provider adapter/launch spec only. | Identity and argv fixtures; unverified vendor capabilities remain unclaimed. |
| W1-04.10 | OpenCode provider adapter/launch spec only. | Identity and argv fixtures; unverified vendor capabilities remain unclaimed. |
| W1-04.11 | docs/providers.md only: compatibility matrix and verification timestamps. | Matrix distinguishes available/uncertain and excludes unsupported claims. |
| W1-04.12 | docs/plan/implementation/reports/stage-04.md only. | Record fixture results and concrete-provider smoke still deferred to Stage 10. |
| W1-04.13 | docs/plan/implementation/04-discovery-providers-and-workspaces.md only: update checklist/status. | Stage 04 fixtures and make check pass; matrix reviewed. |

#### Stage 05 — PTY and session runtime (14 stage commits + status)

| ID | Commit-sized change and owned scope | Evidence before push |
|---|---|---|
| W1-05.01 | Resolved executable/argv, validated cwd, inherited non-persisted env and process identity in internal/process. | Fake child observes cwd/env/argv; identity records owner/attempt and Linux process facts. |
| W1-05.02 | Controlling-PTY/new-session launch and Starting-to-Running transaction in internal/pty and internal/session. | TTY/PGID handshake; save-after-spawn failure cleans only the verified child. |
| W1-05.03 | One PTY reader per session, detached-output drain and descriptor shutdown. | EOF/EIO/broken-input behavior; detach does not stop draining or leak descriptors. |
| W1-05.04 | Terminal state parser for UTF-8 chunk boundaries, alt-screen, cursor/modes and runtime query replies. | Split-sequence and controlling-TTY integration fixtures. |
| W1-05.05 | Snapshot sequence, incremental update and attach/resubscribe gap handling. | Snapshot + subscribe cannot lose intervening bytes; stale sequence requests resync. |
| W1-05.06 | Bounded screen/scrollback/render queues and overflow policy. | Output flood remains bounded; parser input is not silently dropped; observable overflow behavior. |
| W1-05.07 | Serialized input adapter, literal paste and key/input encoding. | Concurrent writers serialize; paste remains literal; raw/line/Ctrl+C fake-agent tests. |
| W1-05.08 | Resize validation/debounce, PTY window-size update and interactive input lease. | Resize produces SIGWINCH; second writer is refused; lease loss detaches without killing process. |
| W1-05.09 | Single Wait, exit classification, trailing-output drain and attempt-generation callback fencing. | Exit/stop race tests; stale callback cannot overwrite a newer attempt. |
| W1-05.10 | Graceful stop, explicit kill, restart serialization and descendant/process-group identity checks. | Cooperative and TERM-ignoring children; explicit kill; restart only after verified exit; external sentinel survives. |
| W1-05.11 | Runtime fault/race/leak acceptance cases in tests/integration/runtime. | Launch/save failure, residual descendant, boot/starttime mismatch, concurrent lifecycle, FD/goroutine cleanup and race suite. |
| W1-05.12 | New docs/architecture/process-ownership.md only; explain as-built identity/lifecycle and link ADR 0001. | Documentation matches tested behavior; no duplicate or changed ADR decision. |
| W1-05.13 | New docs/architecture/terminal-stream-protocol.md only; sequence/snapshot/input/resize/lease contract and link ADR 0002. | Documentation matches Stage 05 stream tests. |
| W1-05.14 | docs/plan/implementation/reports/stage-05.md only. | Record fake-agent, race, leak, descendant and Linux limitations evidence. |
| W1-05.15 | docs/plan/implementation/05-pty-and-session-runtime.md only: update checklist/status. | Every Stage 05 acceptance passes; make check and race suite pass. |

#### Stage 06 — Foreground IPC and CLI (13 stage commits + status)

| ID | Commit-sized change and owned scope | Evidence before push |
|---|---|---|
| W1-06.01 | Foreground owner lock/bootstrap, instance identity and socket creation in internal/app and internal/ipc. | Two simultaneous bootstraps produce exactly one owner; loser connects or gets typed error, never spawns another owner. |
| W1-06.02 | Private runtime socket permissions, same-user access, fallback path and stale/unresponsive/replacement-owner classification. | Permission, missing XDG_RUNTIME_DIR, stale socket and live-owner protection fixtures. |
| W1-06.03 | Versioned IPC handshake/framing, request/operation/payload/error/revision fields, size limits and timeouts. | Malformed, oversize, timeout and protocol-version mismatch tests. |
| W1-06.04 | Serialized mutation, revision/generation fencing and duplicate request-ID idempotency. | Lost-response retry does not create/stop/restart twice; stale generation is rejected. |
| W1-06.05 | Offline/online scan, list, inspect service and CLI semantics. | Live vs stored authority, observed_at, filters, ambiguous ID and JSON fixtures. |
| W1-06.06 | new selection/create/launch and no-owner foreground-owner behavior. | TTY creates/attaches; non-TTY with no owner fails before spawn; client cancel does not kill session. |
| W1-06.07 | open attach/detach and terminal-stream client disconnect behavior. | Detach preserves attempt; offline PTY error is honest; second input lease rejected. |
| W1-06.08 | rename, restart, stop, kill lifecycle commands and destructive confirmations. | Idempotency, force/yes rules, historical/offline reconciliation and ownership proof tests. |
| W1-06.09 | Stable JSON/stdout/stderr, typed errors/exit codes, no ANSI for pipes and actionable help. | CLI contract matrix and existing docs/architecture/cli-contract.md assertions pass. |
| W1-06.10 | Owner/client multi-terminal E2E and protocol fault acceptance. | A/B/C terminals, no lost revision, duplicate request, attach disconnect, malformed/oversize IPC and second-owner cases. |
| W1-06.11 | docs/cli.md only. | Examples/owner lifetime/TTY semantics match observed CLI. |
| W1-06.12 | docs/architecture/ipc-protocol.md only. | Framing, ownership, retry, limits and error semantics match tests. |
| W1-06.13 | docs/plan/implementation/reports/stage-06.md only. | Record multi-terminal, offline, dedupe and protocol evidence. |
| W1-06.14 | docs/plan/implementation/06-foreground-ipc-and-cli.md only: update checklist/status. | Stage 06 E2E and make check pass. |

#### Wave close (1 commit)

| ID | Commit-sized change and owned scope | Evidence before push |
|---|---|---|
| W1-00.55 | docs/plan/implementation/orchestration.md only: mark W1 closed and record Stage 03–06 evidence links. | Full W1 E2E gate and make check pass; all four stage reports/status commits are present. |

Each code row is one small task and one commit with its behavior tests. Parallel tasks use isolated worktrees/branches, explicit disjoint file allowlists and frozen shared contracts; keep commits on those branches and merge into `dev` only after sibling tasks settle. Run `make check` and race acceptance on `dev` after integration; do not merge to `main` until `dev` passes. Each docs row changes exactly one file. Coordinator owns shared APIs, dependencies, acceptance, merge order and final integration. Never advance past failed tests or an unresolved worker.

W1.0 contract freeze is complete in ADR 0003. Stage 03 is DONE. Stage 04 and Stage 05 acceptance/status are DONE and integrated into `dev` (Stage 05 fast-forward commit `31c10f4`); `make check` and `make test-race` pass after integration. Stage 06 is unblocked. W1 remains open until Stage 06 acceptance and full multi-terminal E2E verification pass on `dev`.

The coordinator closes W1 only after Stage 03–06 gates and the full end-to-end make check pass. A worker completion message alone is not a gate.
