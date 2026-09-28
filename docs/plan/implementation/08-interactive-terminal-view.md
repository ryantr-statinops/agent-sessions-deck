# Stage 08 — Interactive terminal view

Trạng thái: `PLANNED` · Milestone: M2 · Phụ thuộc: 05, 06, 07 · Cỡ việc: L.

## Mục tiêu

Chạy coding-agent full-screen ngay trong ASD, nhập liệu và chuyển session mà không làm sai screen, terminal modes hoặc lifecycle.

## Phạm vi và đầu ra

`internal/tui/terminal/`, input/renderer adapter trong `internal/terminal/`, attach protocol integration, terminal compatibility tests và `docs/terminal.md`.

## Checklist thực thi

- [ ] Gắn terminal state engine đã chọn từ spike; UI render cells/styles/cursor, không parse ANSI lần nữa trong viewport.
- [ ] Handshake dimensions, lease và atomic snapshot sequence; apply ordered deltas, detect gap và resync snapshot; discard attempt cũ sau restart.
- [ ] Bố cục full-screen hoặc footer rõ với available rows/cols; resize PTY theo vùng agent thực sự được render, không theo toàn terminal khi có chrome.
- [ ] Ctrl+C, Ctrl+D, arrows, modifiers, Tab, Esc, function keys được encode theo modes của session; không lấy event String() rồi gửi như text.
- [ ] Bracketed paste, multiline/Unicode/wide chars và split UTF-8; paste không được kích hoạt ASD shortcut. Mouse/extended keyboard chỉ quảng cáo những mode đã hỗ trợ/test.
- [ ] Prefix shortcut theo ADR (đề xuất Ctrl+] rồi s=list, n=new, ?=help); hỗ trợ gửi literal prefix. q/Ctrl+C thông thường trong agent view đi tới agent.
- [ ] Terminal queries (DA/DSR), application cursor/keypad, alt-screen và cursor visibility có response/restore đúng; không quảng cáo TERM capabilities lớn hơn emulator hỗ trợ.
- [ ] Host terminal effects như OSC clipboard/title/hyperlink được lọc theo allowlist; clipboard explicit user action, không pass through escape sequence tùy ý từ child.
- [ ] Detach ngừng input/lease nhưng parser vẫn cập nhật; open A → B → A giữ screen riêng. Foreground owner vẫn drain mọi PTY.
- [ ] Scrollback bounded, follow-live/scroll mode có indication rõ, alternate-screen không trộn history sai; `open` session exited có final screen nếu còn runtime hoặc báo history I/O unavailable.
- [ ] Owner/client disconnect, PTY EOF, restart generation change hiển thị status và back-to-list; không freeze raw mode.
- [ ] Restore host terminal qua normal exit, errors, panic handling và catchable signals; SIGHUP/TERM semantics theo 09. Không hứa restore được khi SIGKILL.
- [ ] Render throttling không trì hoãn input writes; snapshot/cells/render buffers có measurable caps.

## Acceptance và verification

1. Fake full-screen agent: alt screen, cursor edits, Unicode, terminal queries, paste và resize đều có assertions/fixtures.
2. Hai sessions chạy liên tục: chuyển qua lại screen đúng, input chỉ đến leased session, output detached không block process.
3. TUI frame + agent view không có hai reader tranh stdin hoặc writer tranh stdout.
4. Manual real-terminal matrix gồm local Linux terminal và SSH; thêm tmux nếu là môi trường hỗ trợ. Ghi TERM/version và keyboard limitations.
5. Smoke ít nhất ba concrete providers dự kiến hỗ trợ ở 10; nếu chưa có môi trường, carry gate rõ, không gắn supported badge.
6. Flood workload giữ RAM bounded, UI back/detach vẫn đáp ứng; cleanup trả echo/cursor/alt-screen host về đúng trạng thái.

## Phân công và handoff

Terminal worker giữ adapter; UI owner phối hợp view switching; runtime owner nhận fix protocol qua task riêng. Mọi thay đổi shared contracts phải coordinator review. Handoff cho 09/10 gồm compatibility matrix, benchmarks và unsupported terminal features được tài liệu hóa.

Prompt thực thi: “Thực thi Stage 08 trên terminal engine từ spike. Hoàn thiện input encoding, screen restore, switching, queries/paste/resize và host-terminal cleanup; kiểm tra full-screen agents thực tế trước khi claim support.”
