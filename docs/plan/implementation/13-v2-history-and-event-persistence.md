# Stage 13 — V2 history và event persistence

Trạng thái: `PLANNED` · Milestone: M3 / V2 acceptance · Phụ thuộc: 11 để triển khai; 12 để hoàn tất gate V2 · Cỡ việc: M.

## Mục tiêu

Có lịch sử sessions/attempts và recent events đủ để debug workflow, với retention/migration rõ ràng và không thu thập terminal secrets mặc định.

## Phạm vi và đầu ra

History store/event persistence tại `internal/store/`, history application query, `asd history`, TUI history, `docs/history.md`, storage ADR và `docs/testing/v2-acceptance.md`.

## Checklist thực thi

- [ ] Đo nhu cầu history/events và chọn versioned JSON/event journal hoặc SQLite; ADR nêu atomicity, indexing, migration, dependency/CGO/cross-build tradeoff. Không bắt buộc SQLite chỉ vì V2.
- [ ] Logical session history chứa attempts riêng: start/end/duration/exit reason/command provenance/workspace/agent; restart không overwrite attempt cũ.
- [ ] Persist event ordering/revision transactionally với lifecycle mutations; bootstrap/crash không nhân đôi session.started hoặc xóa event đã commit.
- [ ] Event payload metadata-only, typed schema/version; không persist raw PTY output, prompt, env hoặc credentials tự động.
- [ ] `asd history` filters agent/workspace/time/status, sort và pagination stable; TUI inspect past session không giả vờ còn PTY.
- [ ] Recent events API bounded cursor/limit; subscriber gap/resync và reconnect semantics giữ nhất quán với persisted revisions.
- [ ] Retention configurable cho attempts/events, sensible documented default, explicit prune behavior; không prune active session hoặc references mà profile/runtime còn dùng.
- [ ] Backup/export phù hợp store, integrity checks và recovery instructions; migration V1 JSON → store mới transactionally giữ ID và restore được nếu fail.
- [ ] Corrupt/unsupported schema fail an toàn; disk-full không báo lifecycle success nếu durable contract chưa đạt.
- [ ] Query benchmark với dataset history lớn (đề xuất 100.000 events) và capped result sizes; UI không load toàn dataset vào memory.
- [ ] Hoàn thành V2 acceptance: daemon survive-client-close, workspace view, metrics, history, migrations và service stop/crash policy.

## Acceptance và verification

- Restart tạo hai attempts được inspect sau daemon restart; durations/exit reasons đúng.
- Migration + rollback/fault tests giữ session identity và không duplicate events; backups có hướng dẫn restore thực tế.
- Pagination/filter fixtures, retention active-record protection và redaction/absence-of-secrets checks pass.
- V2 release candidate chạy lại V1 critical workflow và V2 walkthrough, ghi versioned compatibility và storage limitations.

## Skills tham khảo

Skill là hướng dẫn thao tác; PRODUCT, quyết định đã duyệt và acceptance của stage vẫn là nguồn chuẩn. Chỉ đọc skill cần cho task và kiểm tra trạng thái trong `../../../.agents/SKILLS/data/promoted.json`.

- [`common/workflow/feature-delivery`](../../../.agents/SKILLS/common/workflow/feature-delivery/SKILL.md)
- [`common/engineering/testing`](../../../.agents/SKILLS/common/engineering/testing/SKILL.md)
- [`common/engineering/debugging`](../../../.agents/SKILLS/common/engineering/debugging/SKILL.md)
- [`common/engineering/dependency-management`](../../../.agents/SKILLS/common/engineering/dependency-management/SKILL.md)
- [`common/engineering/code-review`](../../../.agents/SKILLS/common/engineering/code-review/SKILL.md)

Điều phối worker Orca chỉ khi cần phối hợp Orca thật: [skill orchestration](../../../.agents/skills/orchestration/SKILL.md). Đây là discovery stub; trước lệnh Orca tải guide đúng phiên bản theo file.

## Phân công và handoff

Store owner quyết định schema/migration; history UI worker sau query contract freeze. Stage 12/13 có thể song song nếu fields và event schema đã chốt. Handoff cho 14/15 gồm read APIs, cursor semantics và immutable event IDs phục vụ dedupe.

Prompt thực thi: “Thực thi Stage 13, chọn persistence theo dữ liệu/ADR, bổ sung attempt history/events/retention và migration. Verify V2 acceptance; không bật transcript collection mặc định.”
