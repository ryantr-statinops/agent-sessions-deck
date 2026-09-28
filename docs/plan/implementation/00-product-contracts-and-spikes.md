# Stage 00 — Product contracts và technical spikes

Trạng thái: `PLANNED` · Milestone: M0 · Phụ thuộc: không · Cỡ việc: L.

## Mục tiêu

Biến bản draft PRODUCT thành các contract có thể kiểm thử; chứng minh hai phần khó nhất là ownership đa client và terminal compatibility trước khi xây UI đầy đủ.

## Phạm vi và đầu ra

- `docs/architecture/adr/0001-v1-runtime-ownership.md`: foreground owner, socket, client/owner shutdown, V2 daemon boundary.
- `docs/architecture/adr/0002-terminal-integration.md`: chọn emulator/input adapter, compatibility và limitations.
- `docs/architecture/adr/0003-state-and-storage.md`: lifecycle/attempt identity/XDG/concurrency.
- `docs/architecture/cli-contract.md`, `docs/architecture/session-state-machine.md`.
- `docs/testing/terminal-compatibility.md` và spike tái chạy được trong `experiments/terminal/`.
- Đề xuất patch PRODUCT để hợp nhất thuật ngữ; giải thích mọi điểm thay đổi, không chỉ chỉnh diagram.

## Checklist thực thi

- [ ] Chuẩn hóa Agent, logical Session, execution Attempt, ProcessIdentity, Workspace và Repository; xác định identity ổn định giữa restart.
- [ ] Chốt foreground owner semantics cho `asd`, `new`, `open`, second TUI, offline commands và non-TTY. Chốt lock/state-home isolation.
- [ ] Định nghĩa exit owner với active sessions: cancel hoặc explicit stop-all; client exit chỉ detach. TERM/HUP shutdown; crash/SIGKILL không bảo đảm giữ PTY.
- [ ] Viết transition table: launch failure, natural exit, stopped/killed reason, restart failure, owner crash, PTY loss, stale PID, unknown permission.
- [ ] Chốt CLI flags dự kiến: `--json`, `--name`, filters, `--yes` cho confirmation cần thiết, `--force` cho restart-running, `--` truyền argv; error codes/exit codes.
- [ ] Spike Linux PTY với controlling TTY, size, foreground process group, Unicode, Ctrl+C, paste, alt-screen, cursor addressing và query/response (DA/DSR).
- [ ] Spike per-session terminal screen: hai session chạy đồng thời, detach A, output A vẫn cập nhật, open B rồi A không mất màn hình.
- [ ] So sánh emulator candidate bằng corpus thực tế. Thử raw handoff làm baseline nhưng không coi là giải pháp nhiều session nếu không giữ được screen.
- [ ] Thử ít nhất hai coding-agent CLI nếu có cài sẵn và có thể dùng mà không phát sinh đăng nhập/chi phí ngoài phạm vi; nếu không, ghi thiếu coverage và yêu cầu smoke trước Stage 10.
- [ ] Đo bounds của output flood, resize, snapshot và input roundtrip; lập performance baseline chứ không chọn lib theo tên.
- [ ] Chốt shortcut prefix (đề xuất Ctrl+] rồi phím action), cách gửi literal prefix, scope Ctrl+C và terminal restore.
- [ ] Liệt kê license/module naming pending; maintainer chọn license trước public release. Không chặn spike bởi license chưa chọn.

## Ngoài phạm vi

Không xây daemon, MCP, profiles hoặc production dashboard. Không claim native conversation resume. Không reconnect agent do công cụ khác khởi tạo.

## Acceptance và verification

1. Có bảng hành vi cho mọi CLI V1 khi owner tồn tại/không tồn tại và khi không có TTY.
2. Spike chạy bằng hướng dẫn một command; kết thúc trả terminal về trạng thái ban đầu, không để test child tồn tại.
3. Hai session full-screen có input/resize đúng, screen A được khôi phục sau khi detach, stdout UI không bị agent ghi chồng.
4. Ghi rõ emulator nào được chọn, version/import path tương thích, coverage chưa đạt và phương án giải quyết. Nếu spike fail, stage chưa DONE; cập nhật kiến trúc/scope trước khi tiếp tục 05/08.
5. ADR giải thích vì sao socket V1 không đồng nghĩa daemon V2, và vì sao crash recovery không khôi phục PTY đã mất.

## Phân công và handoff

Một owner chốt contract; reviewer xem state transitions và process identity. Có thể giao terminal spike cho worker với riêng `experiments/terminal/`; owner hợp nhất quyết định. Handoff cho 01–02 gồm dependency shortlist, state table, command matrix và unresolved decisions có gate rõ ràng.

Prompt thực thi: “Thực thi Stage 00 theo tài liệu này. Đọc PRODUCT và plan README, viết ADR/contract và chạy terminal spike. Lưu evidence thực tế, ghi mọi giới hạn; chưa xây tính năng production ngoài scope.”
