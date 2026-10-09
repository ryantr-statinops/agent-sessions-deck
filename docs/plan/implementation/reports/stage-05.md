# Stage 05 — PTY và session runtime

Trạng thái: `DONE` · Milestone: M1 · Phụ thuộc: 02, 03 và spike/ADR 00.

## Kết quả

- `internal/process.Runtime` implement `session.ChildRuntime`; launch literal executable/argv trong validated workspace với environment kế thừa. Runtime sở hữu PTY, terminal session, process identity và reaper; cancellation của request không làm child mất lifetime.
- PTY chạy trong controlling TTY và OS session mới. Identity giữ PID/PGID, boot ID, `/proc` start ticks và owner instance ID. Mọi signal yêu cầu process thuộc runtime và identity hiện tại khớp; runtime từ chối signal sau khi leader đã reap.
- Mỗi child có một `Wait`; `WaitExit` chỉ cấp một waiter. Stop gửi SIGTERM và không tự escalation; `ForceKill` là thao tác explicit và xác nhận reap riêng với việc signal được gửi.
- Terminal manager đăng ký một background PTY reader và một emulator cho mỗi attempt. `Runtime.Terminals()` cung cấp `app.TerminalSource`; screen snapshot/sequence, bounded frames/raw ring, raw input, resize/SIGWINCH, query replies, detach/reattach đều có behavior tests.
- Restart cùng logical session chỉ tạo generation mới khi generation trước đã hoàn tất và terminal drain đã xong; terminal generation cũ được remove trước khi đăng ký screen mới. Attempt đang sống bị từ chối, không overlap.
- Sau khi leader reap, runtime đóng PTY master, chờ terminal drain tối đa 500 ms, rồi quan sát PGID thêm tối đa 500 ms. PIDs cùng group còn sống hoặc procfs scan không đầy đủ được đưa vào `ExitStatus.Evidence`; runtime không signal PGID sau khi leader đã reap. Process đã `setsid` rời group và không bị adopt/signal.

## Verification

Chạy trên Linux, offline với vendor tree:

- `go test ./internal/process ./internal/pty ./internal/terminal` — PASS.
- `make check` — PASS: fmt-check, vet, unit tests, integration tests, build, `asd --help`/`--version` smoke.
- `make test-race` — PASS trên toàn module, gồm process/runtime, terminal, app và integration packages.
- Runtime behavior fixtures chứng minh cwd/argv và exit code; controlling-PTY identity; một waiter; graceful stop; ignored TERM không tự escalation rồi explicit SIGKILL; identity mismatch và external sentinel; terminal detach/reattach; restart generation không giữ screen cũ; không overlap attempt; same-group residual process được ghi evidence; `setsid` descendant không bị signal cùng group.
- `git diff --check dev...HEAD` sạch sau khi sửa whitespace ở hai file vendored.

## Handoff

- **Stage 06:** composition tạo `process.Runtime`, truyền runtime vào `app.Service`, và truyền `Runtime.Terminals()` làm terminal source. Owner phải gọi `WaitExit` đúng một lần cho `ExitWait` generation tương ứng; app callback vẫn generation-fenced.
- **Commits:** Stage 05 worker commits được giữ nguyên; coordinator additions trên nhánh gồm `fix(pty): kill exact child during launch rollback`, `feat(process): add verified Linux PTY child runtime`, process/runtime lifecycle tests, restart/descendant fixtures, procfs group observation và vendored whitespace cleanup.
- **Giới hạn:** Linux `/proc` và process groups only; không dùng cgroup/pidfd, không adopt process ngoài ASD. PGID scan là evidence numeric, không chứng minh descendant ancestry; `setsid` descendants ở ngoài owned group. PGID signal được identity-checked ngay trước syscall, nhưng không có pidfd cho group-wide signal.

## Coordinator review

Stage 05 acceptance được xác nhận trên `work/w1-stage05-runtime`; Stage 06 có thể bắt đầu sau khi Stage 05 đã tích hợp vào `dev`.
