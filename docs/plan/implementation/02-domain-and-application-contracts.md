# Stage 02 — Domain và application contracts

Trạng thái: `DONE` · Milestone: M1 · Phụ thuộc: 01 · Cỡ việc: M.

## Mục tiêu

Một model và application service thống nhất dùng được từ CLI, TUI, foreground IPC và daemon/MCP sau này.

## Phạm vi và đầu ra

`internal/agent/`, `internal/session/`, `internal/workspace/`, `internal/events/`, `internal/app/`; typed errors và contracts tại `docs/architecture/`. Domain không import Bubble Tea, Cobra, transport hoặc vendor SDK.

## Checklist thực thi

- [x] Định nghĩa logical Session với ID/name/agent/workspace, và Attempt với generation, immutable resolved command, timestamps, exit reason, process identity.
- [x] ProcessIdentity gồm PID, PGID, boot ID, process start ticks và owner instance ID; PTY handle chỉ trong memory, không serialize file descriptor.
- [x] Tách Lifecycle, Attachment và Activity theo ADR; unknown không bị đổi thành exited chỉ bởi probe fail.
- [x] Reducer T1–T20; orphan là annotation trên running + unavailable; restart giữ Session ID và tăng attempt generation.
- [x] DTO phân biệt persisted và live authoritative observations, có revision và observed timestamp.
- [x] Ports cho ProviderRegistry, WorkspaceResolver, SessionStore, ProcessRuntime, TerminalSubscription và application client.
- [x] Application use cases gồm scan/list/get/create/open/detach/rename/restart/stop/kill/delete/report; delete chỉ metadata khi session không active.
- [x] Capabilities tách ASD core lifecycle khỏi native vendor list/log/resume; unsupported trả typed error.
- [x] Error taxonomy bao phủ PRODUCT và typed `OWNER_UNAVAILABLE`, `CONFLICT`, `STALE_ATTEMPT`, `NOT_INTERACTIVE`, `CORRUPT_STATE`.
- [x] Metadata events có ID/type/session/attempt/revision/timestamp, không chứa raw terminal bytes hoặc environment secrets.
- [x] Owner-wide serialized mutations, store transactions, một exit waiter/child, generation fencing cho callbacks.

## Acceptance và verification

- Table tests bao phủ transition hợp lệ và bị từ chối, rename/restart identity, callback từ attempt cũ, delete-running và unsupported operations.
- Mock ports chứng minh use case không phụ thuộc terminal framework hoặc transport.
- Natural nonzero exit khác launch failure; stop timeout không tự trở thành “killed”.
- Events có ordering/revision rõ; subscriber chậm không giữ lock lifecycle và policy overflow đã mô tả.
## Kết quả và handoff

Implementation và verification được ghi tại [báo cáo Stage 02](reports/stage-02.md). Durable JSON dùng `snake_case` cho các nested `Reason`, `ProcessIdentity` và `Evidence`; Stage 03 phải đặt schema version và migration quanh wire shape này. `unknown` yêu cầu ProcessIdentity; probe T15/T18 trước khi có identity bị từ chối, giữ `starting` để T2 quyết định. T6 nhận `natural-exit` hoặc `killed` từ reap đã xác nhận; không nhận reason T8/T16/T17.

Scope giới hạn theo Stage 02: không thêm production process/PTY/store backend. Stage 05/09 sở hữu process runtime, wait/reconciliation và crash recovery; Stage 03 sở hữu persistent store/schema migration; Stage 06 phải hoàn thiện request-ID dedup theo ADR 0003 và thêm `Report` vào client surface. Review handoff còn lại (import-boundary guard cho `events`, concurrent observer consistency, create-after-spawn invariant-failure path, và FD/goroutine leak evidence) nằm trong report.

## Skills tham khảo

Skill là hướng dẫn thao tác; PRODUCT, quyết định đã duyệt và acceptance của stage vẫn là nguồn chuẩn. Chỉ đọc skill cần cho task và kiểm tra trạng thái trong `../../../.agents/SKILLS/data/promoted.json`.

- [`common/foundation/requirements-analysis`](../../../.agents/SKILLS/common/foundation/requirements-analysis/SKILL.md)
- [`common/foundation/task-planning`](../../../.agents/SKILLS/common/foundation/task-planning/SKILL.md)
- [`common/engineering/code-review`](../../../.agents/SKILLS/common/engineering/code-review/SKILL.md)
- [`common/engineering/testing`](../../../.agents/SKILLS/common/engineering/testing/SKILL.md)

Điều phối worker Orca chỉ khi cần phối hợp Orca thật: [skill orchestration](../../../.agents/skills/orchestration/SKILL.md). Đây là discovery stub; trước lệnh Orca tải guide đúng phiên bản theo file.

## Phân công và handoff

Một owner sửa contracts; reviewer độc lập đối chiếu PRODUCT. Sau khi freeze DTO/ports, 03/04/05 có thể làm task riêng theo dependency. Thay đổi shared contract phải gửi coordinator để tránh worker tự tạo interface khác nhau.

Prompt thực thi: “Thực thi Stage 02, ưu tiên domain state machine và service ports. Viết test theo hành vi và publish contract đủ cho store/discovery/runtime workers; chưa thêm backend production ngoài scope.”
