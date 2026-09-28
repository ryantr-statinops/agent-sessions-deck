# Stage 10 — V1 acceptance và release

Trạng thái: `PLANNED` · Milestone: M2 / V1 release · Phụ thuộc: 04, 07, 08, 09 · Cỡ việc: M.

## Mục tiêu

Bàn giao V1 Linux dùng được hằng ngày, có artifact, hướng dẫn cài và giới hạn chính xác; hoàn thành product flow thay vì chỉ có components.

## Phạm vi và đầu ra

Release build workflow, README/usage/config/provider/recovery docs, changelog, sample config, checksums, Linux amd64/arm64 artifacts; `docs/testing/v1-acceptance.md` và stage report với compatibility/performance evidence.

## Checklist nghiệm thu sản phẩm

- [ ] Fresh install không config/account của ASD: `asd` hiển thị installed/configured agents và empty state có hướng dẫn.
- [ ] Chọn agent + plain folder hoặc Git workspace → launch → tương tác ngay → detach → tạo agent thứ hai → switch → rename → stop/restart/kill explicit.
- [ ] Tất cả command V1: `asd`, `scan`, `list`, `new`, `open`, `inspect`, `rename`, `restart`, `stop`, `kill`; filters/help/JSON/exit codes đúng contract.
- [ ] Generic + ít nhất ba concrete providers có real-launch/terminal smoke bằng version được ghi; nếu thiếu, giảm support claim rõ hoặc chưa release theo scope đã chốt.
- [ ] Search theo agent/name/path/repo/branch/status/PID và filters hoạt động; status không dùng silence như IDLE.
- [ ] Active owner/client quit khác nhau; owner crash và PTY unavailable có recovery instructions đã diễn tập.
- [ ] No-Git/missing-agent/bad config/permission/launch failure/slow Git có thông điệp actionable, không crash.
- [ ] Agent authentication vẫn do underlying CLI trong PTY xử lý; ASD không yêu cầu hoặc lưu vendor credentials.

## Engineering và packaging checklist

- [ ] Chạy build/unit/integration/race/vet và relevant fault suites; ghi commands/results. Không chạy lại vô hạn nếu chưa có thay đổi hoặc rủi ro mới.
- [ ] Đo scan/input/UI/memory theo workloads và budgets README; sửa nếu fail hoặc ghi quyết định điều chỉnh mục tiêu có dữ liệu.
- [ ] Clean-state E2E trên Linux amd64; Linux arm64 cần runtime smoke thật nếu claim tested, nếu chỉ cross-build thì ghi đúng.
- [ ] CI release tarballs, checksums và version metadata; chọn GoReleaser hay script nhỏ theo nhu cầu, pin tool nếu dùng.
- [ ] Hướng dẫn cài, uninstall binary, backup/reset metadata explicit, sample config, shortcuts và foreground owner limitations.
- [ ] Dependency/license review và maintainer chốt LICENSE/module/release identity trước public publish; không tự invent license.
- [ ] Release candidate được dogfood ít nhất một workflow có hai agent sessions; ghi concrete failures/fixes và known limitations.
- [ ] Chuẩn bị release notes và artifact reviewable; xuất bản/tag theo phạm vi được giao khi thực thi, không coi tài liệu kế hoạch là authorization publish hiện tại.

## Gate DONE

Acceptance matrix có evidence cho mọi command và flow, không remaining blocker về process ownership, terminal input/rendering hay persistent state. Các giới hạn còn lại phải cụ thể, ví dụ session không sống qua owner shutdown ở V1; không ghi “supports all agents/terminals”. V1 không yêu cầu daemon, dedicated history command, MCP hay profiles.

## Phân công và handoff

QA reviewer kiểm tra fresh-user walkthrough; docs/release worker giữ docs/artifacts; coordinator tích hợp fixes. Handoff cho 11 là tagged/identified V1 baseline, frozen client/service contract, schema versions và migration requirements. Nếu chưa publish, dùng reviewed commit/artifact identity trong report.

Prompt thực thi: “Thực thi Stage 10 bằng V1 acceptance matrix và clean-user walkthrough. Chuẩn bị artifacts/docs/checksums, chứng minh concrete provider compatibility và recovery; chỉ publish trong phạm vi authorization của nhiệm vụ triển khai.”
