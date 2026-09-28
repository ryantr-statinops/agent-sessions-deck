# Stage 11 — V2 daemon và client migration

Trạng thái: `PLANNED` · Milestone: M3 · Phụ thuộc: 10 · Cỡ việc: L.

## Mục tiêu

Chuyển ownership từ foreground owner sang local daemon `agentd`, để đóng TUI/client không kết thúc các session đang chạy.

## Phạm vi và đầu ra

`cmd/agentd/`, `internal/daemon/`, runtime/bootstrap/IPC migration, optional user service example, `docs/daemon.md`, `docs/migration-v2.md`. CLI/TUI tái dùng application client contract V1.

## Checklist thực thi

- [ ] ADR chuyển ownership và daemon lifetime; `agentd` foreground mode dễ debug, explicit start/status/stop interface nhỏ (chốt syntax trước code), optional user service.
- [ ] Giữ single owner per state home, protocol handshake và state schema compatibility; daemon và foreground V1 không cùng writer.
- [ ] Chốt migration với live V1 sessions: require stop/quiesce trước ownership switch; không claim chuyển PTY sống giữa process nếu chưa có FD-transfer mechanism được kiểm thử. Mặc định không triển khai live transfer.
- [ ] `asd`/CLI trở thành client; daemon absent có explicit startup flow hoặc documented controlled auto-start. Không tạo daemon nhiều lần khi clients start đồng thời.
- [ ] Daemon giữ PTY/emulator/reader khi không có client, reconciliation/events chạy liên tục; attach lease mất chỉ detach.
- [ ] Client reconnect resync revision/screen; old connection không ghi vào replacement daemon/session generation.
- [ ] Daemon stop với active sessions nêu tác động và cần explicit stop/force policy; service restart/crash có honest recovery, không hứa session sống sau daemon chết.
- [ ] Socket same-user, private paths, no TCP listener mặc định. Daemon không chạy root, không persist full env/secrets.
- [ ] Logs bounded/rotated, metadata-only; status/diagnostics dùng bootstrap interface không cần PTY.
- [ ] Optional systemd user unit được tài liệu hóa; không enable autostart/linger tự động khi install. Không coi logout survival là mặc định nếu service policy không bảo đảm.
- [ ] Version mismatch có migration instruction; newer client không silently corrupt older daemon state.

## Acceptance và verification

- Start daemon → launch hai agents → đóng mọi TUI → CLI list vẫn Running → open lại với screen/PTY cùng attempt.
- Đóng một client không signal child; second input lease/reconnect có hành vi đúng.
- Concurrent daemon starts chỉ một owner; V1 owner đang active chặn migration rõ ràng.
- Daemon crash/restart phân loại orphan/exit/unknown đúng; không reconnect PTY đã mất hoặc auto-kill external process.
- Unit example và standalone daemon đều có bounded shutdown, private permissions và không mở network port.

## Phân công và handoff

Daemon owner làm ownership/startup migration; UI/CLI worker chỉ thay bootstrap routing sau contract freeze. Coordinator review compatibility và service policy. Handoff cho 12/13 là daemon event/snapshot/subscription API ổn định và V1→V2 migration evidence.

Prompt thực thi: “Thực thi Stage 11 trên V1 đã nghiệm thu. Tách agentd làm sole owner, giữ client contract và state migration an toàn; prove sessions sống qua client close nhưng mô tả đúng giới hạn daemon crash.”
