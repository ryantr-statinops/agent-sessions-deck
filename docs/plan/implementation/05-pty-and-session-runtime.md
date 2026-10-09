# Stage 05 — PTY và session runtime

Trạng thái: `DONE` (coordinator acceptance; Linux checks and runtime behavior tests passed) · Milestone: M1 · Phụ thuộc: 02, 03 và spike/ADR 00 · Cỡ việc: L.

## Mục tiêu

Một runtime độc lập UI sở hữu child process, PTY, lifecycle và terminal state, đủ để chạy nhiều agent mà không rò resource hoặc tác động process bên ngoài.

## Phạm vi và đầu ra

`internal/process/`, `internal/pty/`, `internal/terminal/`, runtime implementation trong `internal/session/`, `tests/integration/runtime/`; [terminal stream/process ownership contract](../../architecture/terminal-stream.md) và [acceptance report](reports/stage-05.md).

## Checklist thực thi

- [x] Start command bằng absolute resolved executable + argv, cwd đã validate; env kế thừa runtime, không persist. Launch context không gắn với child lifetime.
- [x] Dùng controlling TTY và new OS session; capture PID/PGID thực tế, kiểm tra PID là process-group leader.
- [x] App launch transaction ghi Starting, spawn, capture identity, rồi persist Running; persistence failure cleanup dùng identity đã xác minh và force-kill có reap confirmation.
- [x] Mỗi PTY có một background read loop; terminal state tiếp tục drain khi detached.
- [x] Emulator xử lý UTF-8/chunking, alt-screen/cursor/modes và trả lời terminal queries khi không có subscriber.
- [x] Attach trả screen snapshot cùng sequence boundary; frame tiếp theo là delta có thứ tự hoặc snapshot resync.
- [x] Screen, scrollback, input/raw-output buffers và frame queue được giới hạn; parser không drop byte đầu vào.
- [x] Input writes được tuần tự hóa, paste giữ literal; resize validate/debounce và cập nhật PTY, SIGWINCH có handshake test.
- [x] Mỗi session có một interactive lease; detach/release không signal process; reattach cùng attempt giữ screen/input.
- [x] Runtime có một reaper/Wait mỗi child; terminal drain có deadline, exit evidence generation-fenced ở app/domain layer.
- [x] Stop chỉ gửi SIGTERM, chờ grace và trả timeout khi còn sống; không tự nâng lên SIGKILL.
- [x] ForceKill là hành động explicit; app restart yêu cầu force khi còn chạy, runtime từ chối overlap và chỉ mở generation mới sau khi attempt cũ/reaper hoàn tất.
- [x] Signal cần runtime-owned PID/PGID/boot/starttime/owner identity match; stale/mismatched identity bị từ chối.
- [x] Tests phân biệt descendants cùng group với setsid-detached; runtime ghi residual PGID members sau leader reap và không signal group sau khi leader gone.
- [x] Leader exit có terminal drain deadline 500 ms và group-member observation grace 500 ms; residual PIDs/scan uncertainty được ghi trong exit evidence, không giữ reader vô hạn.

## Ngoài phạm vi

Không tự nhận process từ ngoài ASD, không pid scanner để adopt, không đòi cgroup/root làm prerequisite V1, không persist transcripts mặc định.

## Acceptance và verification

- [x] Fake process chạy controlling TTY, cwd/env, raw/line input, Ctrl+C và resize/SIGWINCH handshake.
- [x] Detach không kill; reattach cùng generation nhận screen và tiếp tục input.
- [x] Cooperative stop, ignored TERM + explicit force kill, restart serialization/no-overlap đều có behavior tests.
- [x] Launch/persistence failure handling, EOF/EIO, broken input và output flood được kiểm tra tại process/app/terminal suites; buffers/drains có giới hạn.
- [x] Identity mismatch/boot mismatch/stale callback không được phép signal/ghi đè generation; external sentinel sống sau owned-group kill.
- [x] `make test-race` pass trên toàn bộ module Linux, gồm process/runtime, terminal, app và integration tests.

## Skills tham khảo

Skill là hướng dẫn thao tác; PRODUCT, quyết định đã duyệt và acceptance của stage vẫn là nguồn chuẩn. Chỉ đọc skill cần cho task và kiểm tra trạng thái trong `../../../.agents/SKILLS/data/promoted.json`.

- [`common/foundation/task-planning`](../../../.agents/SKILLS/common/foundation/task-planning/SKILL.md)
- [`common/engineering/testing`](../../../.agents/SKILLS/common/engineering/testing/SKILL.md)
- [`common/engineering/debugging`](../../../.agents/SKILLS/common/engineering/debugging/SKILL.md)
- [`common/engineering/code-review`](../../../.agents/SKILLS/common/engineering/code-review/SKILL.md)

Điều phối worker Orca chỉ khi cần phối hợp Orca thật: [skill orchestration](../../../.agents/skills/orchestration/SKILL.md). Đây là discovery stub; trước lệnh Orca tải guide đúng phiên bản theo file.

## Phân công và handoff

Một runtime owner giữ process/lifecycle; terminal engine worker có thể làm riêng parser/snapshot sau contract freeze. Coordinator duyệt transaction semantics và dependency. Handoff cho 06/08 gồm API, stream ordering, input lease, size constraints và số đo memory/backpressure.

Prompt thực thi: “Thực thi Stage 05 với fake-agent integration. Xây runtime một-reader/một-waiter, process identity, terminal state và lifecycle theo ADR; chứng minh cleanup và concurrency bằng behavior tests.”
