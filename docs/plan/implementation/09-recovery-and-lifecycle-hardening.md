# Stage 09 — Recovery và lifecycle hardening

Trạng thái: `PLANNED` · Milestone: M2 · Phụ thuộc: 06, 08 · Cỡ việc: L.

## Mục tiêu

V1 chịu được child crash, owner crash, lỗi I/O và concurrent actions mà không giết nhầm process, orphan âm thầm hoặc bịa trạng thái recoverable.

## Phạm vi và đầu ra

Reconciliation/shutdown trong `internal/{session,process,app,ipc,store}`, bounded metadata event delivery, fault suite tại `tests/integration/recovery/`; `docs/recovery.md` và `docs/troubleshooting.md`.

## Checklist thực thi

- [ ] Owned-child Wait là nguồn chính cho exit; reconciliation định kỳ đối chiếu identity/liveness với persisted observations, không discovery/adopt agent ngoài ASD.
- [ ] OS probe timeout/permission denied trả Unknown có reason; missing PID/identity mismatch kết thúc observation cũ mà không signal PID thay thế.
- [ ] On startup phân loại history exited, stale running, verified surviving orphan và unknown. Không restore FD/PTY bằng metadata; `open` orphan trả SESSION_IO_FAILED với hướng dẫn rõ.
- [ ] Surviving orphan blocks auto-restart cùng logical session; destructive recovery cần explicit action + verified identity. Không gửi input/kill process chưa xác minh là ASD-owned.
- [ ] Owner quit active sessions: show cancel/stop-all count, graceful stop bounded, child ignored TERM báo chưa hoàn tất; force-all chỉ explicit. Không return shell và bỏ child mà UI đã tuyên bố stopped.
- [ ] Catch TERM/HUP theo policy công khai: shutdown owner gracefully, persist results, log residual processes nếu chưa stop được; không silent escalation. Ctrl+C ở agent view vẫn là agent input.
- [ ] Khi owner chết bất ngờ/PTY loss có thể HUP child tự nhiên; reconcile sự kiện thực tế, không assume mọi child chắc còn sống hoặc chắc đã chết.
- [ ] Verify startup lock/socket stale detection và cleanup instance-fenced; không delete socket owner mới từ cleanup owner cũ.
- [ ] Concurrent stop/kill/restart/exit được serialize; late event/timeout không ghi state attempt mới; restart không tạo duplicate do IPC retry.
- [ ] Event subscribers có policy drop/coalesce và resync revision khi overflow; lifecycle transactions không chờ UI chậm.
- [ ] Sanitized diagnostics: session/attempt/error code hữu ích, không dump env/token/raw output. Unexpected panic có recovery path cho host terminal khi có thể.
- [ ] Thêm disk-full, corrupt state, client disconnect giữa mutation, PTY EIO/write failure, killed-owner và slow listener fault scenarios.
- [ ] FD/goroutine/process stress chạy nhiều create/open/detach/restart/stop cycles; bounded runtime cleanup và temporary home cleanup.

## Acceptance và verification

- Fault matrix ghi expected lifecycle/I/O/store result cho từng failure; tests không chỉ assert “không panic”.
- Crash owner rồi khởi động lại: metadata còn, liveness thật được phân loại, không thể open PTY đã mất, no automatic kill/adopt.
- PID reuse fixtures và external sentinel chứng minh không tác động process ngoài ASD.
- Ignored TERM giữ truthful status, require explicit force; UI và CLI trả cùng typed error/reason.
- Race suite + stress pass; nếu group descendants tách khỏi ownership không cleanup được, report limitation cụ thể trong release docs.

## Phân công và handoff

Runtime owner làm lifecycle/reconciliation; reviewer viết/adapt fault cases; docs worker viết recovery từ evidence. Coordinator giữ shutdown policy chung. Handoff cho 10 gồm bug closure, fault matrix và material limitations còn lại.

Prompt thực thi: “Thực thi Stage 09 với fault injection thực tế. Hoàn thiện startup recovery, shutdown, orphan handling và concurrent lifecycle; fail closed cho identity không chứng minh được, lưu evidence trong stage report.”
