# Stage 05 — PTY và session runtime

Trạng thái: `PLANNED` · Milestone: M1 · Phụ thuộc: 02, 03 và spike/ADR 00 · Cỡ việc: L.

## Mục tiêu

Một runtime độc lập UI sở hữu child process, PTY, lifecycle và terminal state, đủ để chạy nhiều agent mà không rò resource hoặc tác động process bên ngoài.

## Phạm vi và đầu ra

`internal/process/`, `internal/pty/`, `internal/terminal/`, runtime implementation trong `internal/session/`, `tests/integration/runtime/`; tài liệu process ownership và terminal stream protocol.

## Checklist thực thi

- [ ] Start command bằng absolute resolved executable + argv, cwd đã validate; env kế thừa runtime, không persist. Tránh dùng request context của CLI làm lifetime process: cancel client không tự kill session.
- [ ] Dùng PTY controlling TTY và new OS session theo API đã spike; lưu PID/PGID thực tế. Không kết hợp tùy tiện Setsid/Setpgid gây launch failure.
- [ ] Launch transaction: record Starting, spawn, capture identity/PTY, persist Running. Nếu save sau spawn fail, cleanup child bằng ownership đã xác minh và báo launch failure; không để child invisible.
- [ ] Mỗi PTY có đúng một read loop; phân phối bytes sang emulator/state engine. Background session vẫn drain output dù không có subscriber.
- [ ] Parser handling UTF-8 chunk boundary, alt-screen/cursor/modes; response cho terminal queries do runtime thực hiện, không phụ thuộc client đang attach.
- [ ] Screen snapshot + sequence number, incremental updates/dirty cells theo contract; attach snapshot và subscribe phải không bỏ mất bytes giữa hai bước.
- [ ] Bounded screen/scrollback/render queues; parser không được drop bytes tùy tiện vì sẽ làm sai terminal state. Coalesce UI frames và cap scrollback, policy overflow ghi rõ.
- [ ] Input adapter serializes writes, giữ literal paste; resize validation + debounce + PTY window-size update; process nhận SIGWINCH theo OS semantics đã test.
- [ ] Một interactive lease mỗi session; second writer bị từ chối có lý do. Khi lease mất kết nối, detach và release nhưng process tiếp tục nếu owner còn sống.
- [ ] `Wait` chỉ gọi một lần; exit callback kiểm tra attempt generation, ghi exit code/signal/reason, drain trailing output và close descriptor/goroutines đúng thứ tự.
- [ ] Stop gửi graceful signal tới owned process group, chờ timeout. Nếu chưa exit, trả còn running/stop-timeout và gợi ý kill; không tự SIGKILL trừ hành động force đã explicit.
- [ ] Kill explicit dùng owned group sau identity checks; restart-running yêu cầu force confirmation contract. Restart serialize stop → verify exit → attempt mới, không chạy hai attempt chồng nhau.
- [ ] Identity checks dùng owner/attempt + starttime/boot ID; cân nhắc pidfd cho stable process handle. Stale/unverifiable identity từ chối signal, không dùng PID đơn lẻ hoặc kill-by-name.
- [ ] Test agent sinh descendants cùng group và tách session/group; ghi giới hạn Linux process-group cleanup. PID leader exit không chứng minh group sạch; không signal PGID tái sử dụng khi không xác minh được.
- [ ] Xác định session completion khi leader exit nhưng descendants còn giữ PTY: grace/drain deadline, residual-owned-process evidence và trạng thái rõ; không giữ reader vô hạn.

## Ngoài phạm vi

Không tự nhận process từ ngoài ASD, không pid scanner để adopt, không đòi cgroup/root làm prerequisite V1, không persist transcripts mặc định.

## Acceptance và verification

- Fake agent có controlling TTY, cwd/env đúng, raw/line input, Ctrl+C và resize có observable handshake.
- Detach không kill; reattach cùng attempt nhận screen đúng và input tiếp tục.
- Stop cooperative child group kết thúc; ignored TERM cần explicit kill; restart chỉ tạo một attempt mới sau attempt cũ đã xử lý.
- Launch/save failure, EOF/EIO, broken input và output flood có typed error, không crash runtime hoặc leak FD/goroutine.
- Tests identity mismatch/boot mismatch/old callback không signal hoặc overwrite attempt hiện tại; external sentinel process vẫn sống.
- Race suite pass cho concurrent exit/stop/restart/input/resize, với timeout và fixture cleanup.

## Phân công và handoff

Một runtime owner giữ process/lifecycle; terminal engine worker có thể làm riêng parser/snapshot sau contract freeze. Coordinator duyệt transaction semantics và dependency. Handoff cho 06/08 gồm API, stream ordering, input lease, size constraints và số đo memory/backpressure.

Prompt thực thi: “Thực thi Stage 05 với fake-agent integration. Xây runtime một-reader/một-waiter, process identity, terminal state và lifecycle theo ADR; chứng minh cleanup và concurrency bằng behavior tests.”
