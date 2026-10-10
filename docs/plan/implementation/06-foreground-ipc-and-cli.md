# Stage 06 — Foreground IPC và CLI V1

Trạng thái: `COMPLETE` · Milestone: M1 · Phụ thuộc: 03, 04, 05 · Cỡ việc: L.

## Mục tiêu

Các command V1 thao tác cùng runtime từ nhiều terminal mà vẫn chỉ có một process owner và một store writer.

## Phạm vi và đầu ra

`internal/ipc/`, `internal/cli/`, owner/bootstrap tại `internal/app/`, assembly `cmd/asd/`; `docs/cli.md`, `docs/architecture/ipc-protocol.md`, owner/client integration tests.

## Checklist thực thi

- [x] Bootstrap atomically: acquire owner lock trước create socket; instance ID/handshake chứng minh owner. Second launch kết nối owner hoặc trả lỗi rõ, không spawn owner trùng.
- [x] Socket trong private runtime directory, same-user access (mode/peer credentials), protocol version handshake; fallback khi XDG_RUNTIME_DIR thiếu phải private, owned, có policy cleanup rõ.
- [x] Không unlink socket chỉ vì timeout. Dùng owner lock và liveness/handshake để phân biệt stale owner, unresponsive owner và replacement owner.
- [x] Control framing có version/request ID/operation/payload/error/revision, size limit và timeout. Terminal stream framing riêng hoặc explicit frame type; cancellation không kill session.
- [x] Expose service ops, serialize mutation, generation fencing; request retry không launch/stop/restart lặp do response lost.
- [x] `asd scan`: offline được, human table và `--json`, available/missing/uncertain rõ.
- [x] `asd list [--agent --status --workspace] [--json]`: online live snapshot; offline metadata kèm observed_at/authority, không claim stored Running là đang chạy.
- [x] `asd inspect <id> [--json]`: identity, workspace/Git, attempt, I/O availability và exit reason; ambiguous ID prefix báo conflict.
- [x] `asd new [agent] [workspace] [--name ...] [-- ...]`: chọn bằng prompts nếu thiếu; launch rồi attach; không có owner trở thành foreground owner. Non-TTY không có owner fail `NOT_INTERACTIVE`; với owner có thể scriptable create, trả ID khi không có TTY.
- [x] `asd open <id>`: attach live terminal, exit chỉ detach với client; offline PTY unavailable trả lỗi honest. Không gọi native vendor resume ngầm.
- [x] `asd rename`, `restart`, `stop`, `kill`: validate args, typed error, idempotency, explicit destructive semantics. `kill` prompt khi TTY hoặc yêu cầu `--yes` khi non-TTY; `restart` đang chạy cần explicit `--force`/confirmation như contract.
- [x] `asd`/`asd new` trở thành owner: khi user rời interactive view vẫn giữ foreground control surface; không return shell trong khi giữ child lẻ mà không owner.
- [x] Với owner không còn sống: rename/restart historical record có lock và reconcile trước; record orphan/unknown không được launch duplicate. Restart historical khi không có owner phải bootstrap foreground owner và cần TTY như `new`; non-TTY fail trước spawn. Offline stop/kill chỉ hỗ trợ nếu chứng minh ownership an toàn theo ADR, nếu chưa đủ bằng chứng trả lỗi hướng dẫn.
- [x] Define stable JSON schema, stdout dành kết quả, stderr diagnostics/prompts, no ANSI cho pipe; exit codes thống nhất (ví dụ 0 success, 1 operation, 2 usage, 3 unavailable, 4 conflict; chốt ở contract).
- [x] `--help` nêu examples, shortcut attach và V1 owner lifetime. Empty list/agent missing/workspace invalid có thông điệp có hành động sửa.

## Acceptance và verification

1. Terminal A làm owner, B scan/list/new, C inspect/rename/stop cùng runtime; state revision không lost-update.
2. Owner chưa có: scan/list/inspect offline hoạt động; `new` với TTY sở hữu runtime; non-TTY create không owner fail mà không spawn child.
3. Hai owner bootstrap đồng thời chỉ một thắng lock; stale socket recovery không phá live owner.
4. Duplicate request ID không tạo hai sessions; attach disconnect không terminate agent; second input lease bị từ chối.
5. CLI integration assert JSON/error/exit code, malformed/oversize IPC và mismatched protocol; fixtures không yêu cầu real vendor.

## Skills tham khảo

Skill là hướng dẫn thao tác; PRODUCT, quyết định đã duyệt và acceptance của stage vẫn là nguồn chuẩn. Chỉ đọc skill cần cho task và kiểm tra trạng thái trong `../../../.agents/SKILLS/data/promoted.json`.

- [`common/workflow/feature-delivery`](../../../.agents/SKILLS/common/workflow/feature-delivery/SKILL.md)
- [`common/engineering/testing`](../../../.agents/SKILLS/common/engineering/testing/SKILL.md)
- [`common/engineering/debugging`](../../../.agents/SKILLS/common/engineering/debugging/SKILL.md)
- [`common/engineering/code-review`](../../../.agents/SKILLS/common/engineering/code-review/SKILL.md)

Điều phối worker Orca chỉ khi cần phối hợp Orca thật: [skill orchestration](../../../.agents/skills/orchestration/SKILL.md). Đây là discovery stub; trước lệnh Orca tải guide đúng phiên bản theo file.

## Phân công và handoff

IPC worker và CLI worker có thể làm song song sau protocol freeze; coordinator giữ bootstrap/assembly. Stage chỉ hoàn thành khi E2E owner/client pass, không phải khi commands trả mock data. Handoff cho 07–08 là application client API và CLI examples đã chạy.

Prompt thực thi: “Thực thi Stage 06, tích hợp foreground owner Unix socket và CLI V1 thật. Giữ một writer, không background daemon; verify nhiều clients, offline semantics và request retry.”
