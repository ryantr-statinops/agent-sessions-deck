# Kế hoạch orchestration triển khai

## Mục đích và trạng thái

Tài liệu này gộp các stage tương lai thành execution wave có dependency rõ và cách giao việc cho Orca. Các stage 00–02 đã hoàn tất vẫn giữ nguyên ID, report và evidence; không gộp lại hoặc đổi lịch sử. Stage 03–15 vẫn là các checklist/acceptance con. Một wave chỉ `DONE` khi mọi gate của wave và các stage con tương ứng có evidence; việc merge code hoặc worker báo xong chưa đủ.

**Ưu tiên:** hoàn thành M1–M2 để giao V1 qua Stage 10; chỉ mở M3/V2 và M4/V3 sau khi xem lại nhu cầu thực tế, như implementation README đã yêu cầu.

## Bốn execution wave

| Wave | Stage được gộp | Mục tiêu/gate | DAG điều phối |
|---|---|---|---|
| **W1 — Core V1 runtime (M1)** | 03–06 | Config/store an toàn; discovery/workspace và runtime chạy được; CLI/IPC điều khiển một owner. Gate: fake agent chạy qua CLI, đọc trạng thái từ terminal khác; toàn bộ contract/retry/ownership acceptance của 06 pass. | `03 → (04 ∥ 05) → 06`. Đóng băng config DTO, store/lock API và path/failure policy trước khi 04/05 cùng chạy. 06 chỉ bắt đầu sau khi 04 và 05 hoàn tất. |
| **W2 — V1 UX và release (M2)** | 07–10 | Hoàn thành launcher/dashboard, terminal tương tác, recovery; qua V1 acceptance trên Linux. Gate: Stage 10 có end-to-end evidence, concrete provider matrix và release limitations chính xác. | `07 → 08 → 09 → 10`. 09 cần 06+08; 10 cần 04+07+08+09. Có thể chạy docs/QA bounded song song nhưng không được bỏ các dependency này. |
| **W3 — V2 daemon và history** | 11–13 | Chuyển owner sang daemon, sau đó workspace/metrics và event/history persistence. Gate V2 chỉ đóng sau acceptance của cả 12 và 13. | `11 → (12 ∥ 13-core) → 13/V2-gate`. Stage 13 có thể phát triển core sau 11; không báo stage hoặc wave hoàn tất trước khi dependency Stage 12 và V2 acceptance đều pass. |
| **W4 — V3 MCP và profiles** | 14–15 | Thêm MCP có policy và profiles/automation opt-in. Gate V3 gồm security/policy acceptance của 14, profile/automation acceptance của 15 và hồi quy V1/V2. | Sau 12+13: `(14 ∥ 15-profile-core) → 15/MCP-integration-and-V3-gate`. Stage 15 không bật MCP profile tools hoặc đóng V3 trước khi Stage 14 hoàn tất. |

Stage IDs và từng file stage vẫn là nguồn acceptance chi tiết. Wave là đơn vị lập kế hoạch/điều phối, không nới scope, không thay dependency ghi trong stage, không gộp acceptance thành một kiểm tra mơ hồ. Stage 03–15 không được đổi `PLANNED` sang `DONE` trước khi implementation và evidence thực sự tồn tại.
Ký hiệu `∥` biểu thị hai node không phụ thuộc theo DAG, không bảo đảm được chạy cùng lúc. Chỉ chạy đồng thời khi Orca báo worker/readiness và runtime capacity thực tế đủ; hiện chỉ OpenCode đã được xác nhận dispatch, nên nếu không có slot đã kiểm chứng thì chạy các node độc lập tuần tự, không tính Kilo/Cline/Freebuff vào capacity.

## Worker capability và model gate

| Worker | Bằng chứng hiện có | Cách dùng trong kế hoạch |
|---|---|---|
| **OpenCode** | `worker-start --agent opencode` review và one-line ping đã thành công; worker báo model thực tế `opencode/fledge-alpha-free`. Receipt vẫn ghi `model: null`; Orca `--model` không áp dụng cho OpenCode. `worker-start --terminal` bị từ chối `agent_unconfigured`. | OpenCode có thể dùng model Fledge theo config hiện tại; chưa có cách pin per-dispatch model. Space Bunny Alpha vẫn không có trong direct catalog. |
| **Kilo Code** | `worker-start --agent kilo` khởi chạy terminal và nhận task; ping vẫn chưa settled. TUI cho thấy model hiện tại là Qwen3.8 27B Free · Kilo Gateway, không phải Space Bunny Alpha, cùng cảnh báo OpenCode config. `worker-start --terminal` bị từ chối `agent_unconfigured`. | Orca `--model` không pin được Kilo; chưa sẵn sàng cho Stage 05 đến khi model khớp và một task ping kết thúc có `worker_done`. Không đổi config nếu chưa được user cho phép. |
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

Dependency remains `03 → (04 ∥ 05) → 06`. For this run, use one verified Orca worker type — OpenCode — with the configured default model and execute the independent Stage 04/05 work serially; do not assume extra concurrent OpenCode slots. Stage 04/05 stay blocked until Stage 03 passes its gate.

- Worker: `opencode`, `--worktree current`, branch `dev`; omit `--model` so Orca/OpenCode uses the user-configured default. The latest one-line ping reported `opencode/fledge-alpha-free`, while the Orca receipt left `model: null`; each new dispatch must report its effective model. Do not silently substitute a different model.
- Coordinator owns PRODUCT/ADR decisions, shared interfaces, `go.mod`/`go.sum`, acceptance review and Stage status. One active task at a time on the current `dev` worktree; no overlap in writable files.
- Stage 04/05 dependencies allow parallelism in principle, but the current plan uses one OpenCode worker sequentially for safer commits and verified capacity.

### Ordered implementation tasks

1. **W1.0 — Contract freeze: complete.** ADR 0003 and the Stage 03 plan freeze XDG paths, per-file version/revision envelopes, atomic `sessions.json` aggregate, independent `state.json` recents, and legacy import precedence.
2. **Stage 03 — Config and persistent state.** Follow the slices in [`Stage 03`](03-config-and-persistent-state.md): XDG config/schema; strict YAML/argv/security; versioned stores/revisions/locking; per-file atomic persistence; legacy migration/backup. Run each slice's behavior/fault tests before its commit and push. Gate: all Stage 03 acceptance passes in an isolated temp home; write/read revisions, migration, permissions and lock behavior are evidenced; report `stage-03.md` is written.
3. **Stage 04 — Discovery, providers and workspaces.** Own `internal/discovery/`, provider implementations, `internal/workspace/`, `internal/git/`, and provider/config docs. First commit PATH precedence, identity/probe limits and generic `BuildCommand` behavior with fixtures; next commit workspace/Git resolution and changed-path parsing fixtures; then add only providers supported by evidence and update the compatibility matrix in a separate docs-file commit. Acceptance is the Stage 04 fixture matrix; do not launch unverified real providers.
4. **Stage 05 — PTY and session runtime.** Own `internal/process/`, `internal/pty/`, `internal/terminal/`, Stage 05 runtime files and integration tests. Commit in small slices: process identity/spawn/reap; PTY single-reader/drain and terminal snapshot; serialized input/resize/lease; stop/restart/cleanup/concurrency. Each slice carries its behavior tests. No new dependency or `go.mod` edit without coordinator approval. Gate: fake-agent controlling-TTY, identity/descendant, detach/reattach, stop/restart, flood, cleanup and race acceptance passes; update runtime/terminal docs separately.
5. **Stage 06 — Foreground IPC and CLI.** After 04+05, coordinator freezes the IPC protocol and owner/bootstrap contract. OpenCode implements bounded IPC and CLI slices serially: owner lock/socket/handshake; request IDs/revision/error framing; CLI commands/JSON semantics; multi-client E2E. Keep bootstrap/assembly and shared protocol changes coordinator-owned. Gate: Stage 06 multi-terminal owner/client, dedupe, offline and protocol tests pass.

### Commit, push and wave gate

For every code slice: run its targeted tests, make one small behavior-complete commit containing implementation plus relevant tests, and immediately push that commit to `origin/dev` before the next slice. For docs, commit one file per commit. Do not accumulate several slices into a stage-end commit; if a test fails, fix it before pushing or starting dependent work. Confirm each push succeeded before proceeding.

At each stage gate, coordinator reviews the diff and evidence, updates the stage report in its own one-file docs commit, then starts the next task. Keep child stage statuses `PLANNED` until their acceptance and report are complete. Close W1 only after Stage 03, 04, 05 and 06 gates pass and the W1 end-to-end `make check` passes; then update W1 status/report. No implementation dispatch is authorized by this plan alone.
