# Stage 07 — TUI launcher và session dashboard

Trạng thái: `PLANNED` · Milestone: M2 · Phụ thuộc: 06 · Cỡ việc: M.

## Mục tiêu

`asd` mở một giao diện dễ dùng để chọn agent/workspace, tạo session và quản lý nhiều session theo đúng flow PRODUCT.

## Phạm vi và đầu ra

`internal/tui/` launcher, forms, list, detail, search, filters, confirmations; `docs/usage.md` và keyboard reference. Stage 08 phụ trách terminal view; ở stage này launch chuyển qua attach interface đã có từ 06.

## Checklist thực thi

- [ ] Model/update/view chỉ dùng application client; discovery/Git/IPC I/O chạy async với cancellation, stale responses không overwrite state mới.
- [ ] First screen ưu tiên chọn agent → workspace → run; recent workspaces tiện truy cập, session list là view rõ ràng có thể mở ngay.
- [ ] New-session form: discovered/configured agent, workspace path/recent/config, optional name; validation inline, launch failure giữ inputs.
- [ ] List/status/details show agent/name/workspace/branch/attempt và truthful process/I/O/activity; no fabricated IDLE.
- [ ] Fuzzy search across session name, agent, workspace/repository, branch, status, PID; ranking deterministic, empty query stable ordering.
- [ ] Agent/workspace/status filters kết hợp search; clear/reset và result count rõ. Chỉ metadata search, chưa search terminal transcript.
- [ ] Keyboard navigation ↑↓/j/k, Enter, n, /, details, restart, stop/kill, help; keymap theo mode, không cướp phím của terminal agent.
- [ ] Explicit confirmations nêu session và tác động: restart-running, graceful stop, force kill, owner quit. Client quit đơn thuần detach.
- [ ] Responsive compact layout cho terminal nhỏ; Unicode width, truncated path có detail xem đầy đủ; no-color palette/status text hữu ích.
- [ ] Loading/empty/no-agent/disconnected-owner/permission-error có CTA cụ thể; refresh không làm mất selection/search hay nhảy cursor.
- [ ] Owner TUI và secondary TUI dùng cùng view contract, nhưng quit dialog khác nhau đúng ownership.
- [ ] Status updates batch/coalesce; list với nhiều historical rows vẫn responsive theo budget README.

## Acceptance và verification

- Walkthrough: fresh config → chọn generic fake agent + workspace → run → session xuất hiện → detach/list → rename/filter/inspect → stop; không cần nhớ command vendor.
- Model tests tập trung form validation, search/filter composition, stale async result và confirmation dispatch; không snapshot mọi pixel.
- Manual QA ở ít nhất 80×24 và narrow terminal; lỗi không tràn layout và luôn có cách back/cancel/help.
- UI không block khi Git probe chậm hoặc owner mất kết nối; cancel không spawn session muộn.

## Skills tham khảo

Skill là hướng dẫn thao tác; PRODUCT, quyết định đã duyệt và acceptance của stage vẫn là nguồn chuẩn. Chỉ đọc skill cần cho task và kiểm tra trạng thái trong `../../../.agents/SKILLS/data/promoted.json`.

- [`common/workflow/feature-delivery`](../../../.agents/SKILLS/common/workflow/feature-delivery/SKILL.md)
- [`common/engineering/testing`](../../../.agents/SKILLS/common/engineering/testing/SKILL.md)
- [`common/engineering/documentation`](../../../.agents/SKILLS/common/engineering/documentation/SKILL.md)
- [`common/engineering/code-review`](../../../.agents/SKILLS/common/engineering/code-review/SKILL.md)

Điều phối worker Orca chỉ khi cần phối hợp Orca thật: [skill orchestration](../../../.agents/skills/orchestration/SKILL.md). Đây là discovery stub; trước lệnh Orca tải guide đúng phiên bản theo file.

## Phân công và handoff

UI worker giữ package TUI; coordinator review action semantics và assembly. Search helper có thể tách task bounded, không sửa runtime. Handoff cho 08 gồm view switching, dimensions, prefix help và owner/client quit routing.

Prompt thực thi: “Thực thi Stage 07 với service client thật. Hoàn thành launcher/dashboard/search/forms/actions và keyboard help; giữ async UI responsive và đúng owner/client semantics.”
