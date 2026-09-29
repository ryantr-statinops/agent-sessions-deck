# Stage 15 — V3 workspace profiles và opt-in automation

Trạng thái: `PLANNED` · Milestone: M4 / V3 acceptance · Phụ thuộc: 12, 13 cho profile core; 14 cho MCP và gate V3 · Cỡ việc: L.

## Mục tiêu

Khởi chạy topology agent quen thuộc của một workspace bằng profile và hỗ trợ triggers opt-in, không tự chạy command từ repo chưa được tin cậy.

## Phạm vi và đầu ra

`internal/profile/`, `internal/automation/`, `asd workspace start <name>`, TUI profile flows, optional MCP profile tools qua adapter 14, `docs/profiles.md`, `docs/automation.md`, V3 acceptance/release notes.

## Checklist thực thi

- [ ] Chốt schema profile version/name/path/sessions với stable role IDs, configured agent IDs, optional argv templates đã cho phép. Chỉ config nhỏ theo PRODUCT, không thêm DSL/scheduler lớn.
- [ ] Profile sources mặc định user config; repo-local profile là untrusted cho đến explicit trust/import. Không auto-run profile vì vừa cd vào repo hoặc mở ASD.
- [ ] Validate workspace, agent availability, role/name duplicates và policy trước launch; `workspace start --dry-run` cho reviewed launch plan.
- [ ] Start topology qua application service, mỗi role một idempotency key; invoke lại hiển thị existing role/session thay vì duplicate theo default policy đã chốt.
- [ ] Partial failure report từng role: started/existing/failed/skipped và session IDs. Không auto-kill role đã chạy thành công nếu chưa chốt rollback explicit; retry chỉ failed/missing roles.
- [ ] Role labels là workspace workflow metadata, không giả định các agents tự trao đổi hay hiểu prompt role nếu chưa có configured behavior.
- [ ] TUI profile preview và start actions; `workspace list` có roles/active sessions, không cần nhớ command vendor.
- [ ] Automation engine chỉ trong daemon, explicit enable per trigger/profile. Initial trigger set nhỏ (workspace opened theo explicit ASD event, profile requested), không filesystem-scan phát hiện mọi IDE/shell activity.
- [ ] Dedupe event ID, debounce/cooldown, concurrency cap, no self-trigger loop (session.started không quay lại workspace-opened); persisted checkpoint khi daemon restart.
- [ ] Actions suggest trước hoặc auto-start sau explicit opt-in; destructive automation stop/restart/kill disabled mặc định và qua policy service.
- [ ] Disable/pause/manual override hoạt động ngay; audit bounded event outcomes, tránh spam UI lặp cho trạng thái không đổi.
- [ ] Expose profile tools qua MCP chỉ sau schema/policy 14; không cho AI sửa trust/auto-run grants ngầm.
- [ ] End-to-end V3 regression: V1 launcher/terminal/lifecycle, V2 daemon/history/metrics, V3 read-only MCP + approved profile automation.
- [ ] Chuẩn bị V3 docs/artifacts/release notes theo release process 10; rà lại scope từ dogfood thay vì thêm mọi ý tưởng tương lai.

## Acceptance và verification

1. Profile ba roles → dry-run đúng → start → ba sessions đúng workspace; start lại không duplicate mặc định.
2. Một agent missing: preflight hoặc partial-failure response đúng policy; retry không chạy lại role đã thành công.
3. Repo-local profile chưa trust không được tự launch; disabled automation và read-only MCP không bypass policy.
4. Một workspace-open event flood tạo tối đa số actions policy cho phép; daemon restart không replay duplicate launch.
5. User manual stop/rename/switch session vẫn bình thường; pause automation không kill session đang chạy.
6. V3 acceptance report có scenario evidence, known limits và các mục ngoài scope; không claim remote orchestration/cross-platform/agent collaboration chưa xây.

## Skills tham khảo

Skill là hướng dẫn thao tác; PRODUCT, quyết định đã duyệt và acceptance của stage vẫn là nguồn chuẩn. Chỉ đọc skill cần cho task và kiểm tra trạng thái trong `../../../.agents/SKILLS/data/promoted.json`.

- [`common/workflow/feature-delivery`](../../../.agents/SKILLS/common/workflow/feature-delivery/SKILL.md)
- [`common/security/security-review`](../../../.agents/SKILLS/common/security/security-review/SKILL.md)
- [`common/engineering/testing`](../../../.agents/SKILLS/common/engineering/testing/SKILL.md)
- [`common/engineering/documentation`](../../../.agents/SKILLS/common/engineering/documentation/SKILL.md)
- [`common/engineering/code-review`](../../../.agents/SKILLS/common/engineering/code-review/SKILL.md)

Điều phối worker Orca chỉ khi cần phối hợp Orca thật: [skill orchestration](../../../.agents/skills/orchestration/SKILL.md). Đây là discovery stub; trước lệnh Orca tải guide đúng phiên bản theo file.

## Phân công và handoff

Profile worker có thể làm core độc lập với MCP sau V2; automation owner cần immutable event IDs từ 13. Coordinator review idempotency/trust/partial failure. Kết thúc bằng reviewed product artifacts và acceptance report; đây là mốc hoàn chỉnh của roadmap hiện tại, các hướng mới cần scope riêng.

Prompt thực thi: “Thực thi Stage 15 với versioned profiles, dry-run, idempotent role launch và triggers opt-in. Verify partial failure, trust, dedupe và V3 regression; không thêm arbitrary shell automation hoặc agent-to-agent orchestration ngoài PRODUCT.”
