# Stage 02 — Domain và application contracts

Trạng thái: `PLANNED` · Milestone: M1 · Phụ thuộc: 01 · Cỡ việc: M.

## Mục tiêu

Một model và application service thống nhất dùng được từ CLI, TUI, foreground IPC và daemon/MCP sau này.

## Phạm vi và đầu ra

`internal/agent/`, `internal/session/`, `internal/workspace/`, `internal/events/`, `internal/app/`; typed errors và contracts tại `docs/architecture/`. Domain không import Bubble Tea, Cobra, transport hoặc vendor SDK.

## Checklist thực thi

- [ ] Định nghĩa logical Session với ID/name/agent/workspace, và Attempt với generation, immutable resolved command, timestamps, exit reason, process identity.
- [ ] ProcessIdentity gồm PID, PGID, boot ID, process start ticks và owner instance ID; PTY handle chỉ trong memory, không serialize file descriptor.
- [ ] Tách Lifecycle, Attachment và Activity theo ADR; trạng thái unknown không bị tự đổi thành exited chỉ bởi probe fail.
- [ ] Viết transition reducer cho Created → Starting → Running → Stopping → Exited/Failed; Running + I/O unavailable tạo orphan annotation; restart giữ session ID tăng attempt generation.
- [ ] DTO snapshot phân biệt persisted observation và live authoritative observation, gồm revision và observed timestamp.
- [ ] Port contracts cho ProviderRegistry, WorkspaceResolver, SessionStore, ProcessRuntime, TerminalSubscription và application client.
- [ ] Application use cases: scan/list/get/create/open/detach/rename/restart/stop/kill; delete metadata chỉ khi không active, có contract dù chưa có CLI riêng.
- [ ] Capabilities phân biệt ASD core lifecycle với native vendor list/log/resume. Unsupported capability trả typed error.
- [ ] Error taxonomy bao phủ PRODUCT và bổ sung `OWNER_UNAVAILABLE`, `CONFLICT`, `STALE_ATTEMPT`, `NOT_INTERACTIVE`, `CORRUPT_STATE` khi cần.
- [ ] Metadata events có ID, type, session/attempt, revision, timestamp; không chứa raw terminal bytes hay environment secrets.
- [ ] Chốt concurrency: per-session serialized lifecycle, owner-level store transactions, exactly-one waiter cho mỗi child, generation fencing cho callbacks.

## Acceptance và verification

- Table tests bao phủ transition hợp lệ và bị từ chối, rename/restart identity, callback từ attempt cũ, delete-running và unsupported operations.
- Mock ports chứng minh use case không phụ thuộc terminal framework hoặc transport.
- Natural nonzero exit khác launch failure; stop timeout không tự trở thành “killed”.
- Events có ordering/revision rõ; subscriber chậm không giữ lock lifecycle và policy overflow đã mô tả.

## Phân công và handoff

Một owner sửa contracts; reviewer độc lập đối chiếu PRODUCT. Sau khi freeze DTO/ports, 03/04/05 có thể làm task riêng theo dependency. Thay đổi shared contract phải gửi coordinator để tránh worker tự tạo interface khác nhau.

Prompt thực thi: “Thực thi Stage 02, ưu tiên domain state machine và service ports. Viết test theo hành vi và publish contract đủ cho store/discovery/runtime workers; chưa thêm backend production ngoài scope.”
