# Agent Session Deck

Agent Session Deck (`asd`) là ứng dụng CLI với giao diện terminal (TUI), giúp developer chọn coding-agent CLI, chọn workspace, khởi chạy và tương tác với session từ một giao diện local.

Luồng sử dụng dự kiến:

```text
asd → chọn agent → chọn workspace → chạy → tương tác
```

ASD chạy các công cụ coding agent thực tế, như Claude Code, Codex CLI, OpenCode hoặc command do người dùng cấu hình. ASD quản lý process và terminal của những session do chính nó khởi tạo.

## Trạng thái hiện tại

Dự án đã hoàn tất Stage 00 và Stage 01; Stage 02 hiện là stage kế tiếp đã lên kế hoạch.

| Hạng mục | Trạng thái |
|---|---|
| Đặc tả sản phẩm | Đã có bản draft trong [PRODUCT.md](PRODUCT.md) |
| Kế hoạch triển khai | Đã có 16 stage trong [docs/plan/implementation/](docs/plan/implementation/README.md) |
| Stage thực thi đã hoàn thành | 2/16; Stage 00 `DONE`, Stage 01 `DONE`, Stage 02–15 `PLANNED` |
| Stage tiếp theo | [Stage 02 — Domain và application contracts](docs/plan/implementation/02-domain-and-application-contracts.md) (`PLANNED`) |
| Mã nguồn, Go module, build và tests | Stage 01 baseline được nghiệm thu; session/product features chưa triển khai |
| Bản phát hành có thể cài đặt | Chưa có |

Repo có tài liệu sản phẩm, Stage 00 terminal evidence và Stage 01 build/test/CI/fake-agent baseline. Tính năng session chưa triển khai; chưa có binary phát hành. License MIT đã được chọn.

## Mục tiêu sản phẩm

V1 hướng tới một công cụ dùng được hằng ngày trên Linux với các khả năng:

- Phát hiện coding-agent CLI đã cài và hỗ trợ command tùy chỉnh qua generic provider.
- Chọn workspace, nhận diện repository và hiển thị Git metadata liên quan.
- Khởi chạy agent trong PTY, nhập liệu và xem terminal của session.
- Quản lý nhiều session: tạo, liệt kê, mở, đổi tên, restart, stop và kill.
- Tìm kiếm, lọc và điều hướng session bằng bàn phím trong TUI.
- Cung cấp CLI có thể dùng trong script, lưu metadata và xử lý lỗi/recovery rõ ràng.

ASD chỉ quản lý session do ASD khởi tạo; V1 không tìm và kết nối lại các session được chạy từ công cụ khác. Restart chạy lại command của session; khả năng resume conversation của từng agent cần adapter riêng.

## Hướng kỹ thuật

| Thành phần | Hướng triển khai dự kiến |
|---|---|
| Ngôn ngữ và nền tảng | Go, Linux trước |
| CLI | Cobra |
| TUI | Bubble Tea, Bubbles, Lip Gloss |
| Tương tác terminal | Managed PTY và terminal state riêng cho từng session |
| Providers | Adapter cho từng agent và generic command |
| Workspace metadata | Filesystem và Git CLI |
| Persistent state V1 | JSON, tách khỏi domain model |
| Runtime V1 | Foreground owner và Unix socket local cho các CLI client |
| Runtime V2 | Daemon `agentd`, để session tiếp tục chạy khi đóng TUI |

Stage 00 ghi các contract và lựa chọn kỹ thuật dưới dạng đề xuất có evidence; chúng chưa phải product fact được maintainer phê duyệt. Xem [báo cáo Stage 00](docs/plan/implementation/reports/stage-00.md) và [kế hoạch tổng quan](docs/plan/implementation/README.md#quyết-định-kiến-trúc-đề-xuất).

Ở V1, session phụ thuộc vào tiến trình foreground sở hữu PTY. Khi đóng owner vẫn còn session sống, giao diện phải cho người dùng quyết định xử lý chúng. V2 bổ sung daemon để tách lifetime của session khỏi TUI; daemon vẫn phải đang hoạt động để giữ PTY.

## Lộ trình

| Mốc | Stage | Kết quả hướng tới |
|---|---|---|
| M0 — Nền tảng | 00–01 | Chốt contract, kiểm chứng terminal và dựng build/test baseline |
| M1 — Luồng CLI chạy được | 02–06 | Domain, config/store, discovery, PTY runtime và CLI |
| M2 — V1 hoàn chỉnh | 07–10 | TUI, terminal view, recovery, nghiệm thu và phát hành Linux |
| M3 — V2 control plane | 11–13 | Daemon, workspace dashboard, metrics và history |
| M4 — V3 programmable platform | 14–15 | MCP, workspace profiles và automation opt-in |

V1 có mốc nghiệm thu và phát hành riêng ở Stage 10. V2/V3 là các đợt mở rộng sau V1.

Mỗi stage có checklist, dependency, đầu ra, tiêu chí hoàn thành, kiểm thử và hướng dẫn bàn giao. Kế hoạch cũng mô tả cách chia việc cho nhiều agent và tránh xung đột ở các file dùng chung.

## Bước triển khai tiếp theo

[Stage 01](docs/plan/implementation/01-repository-and-engineering-baseline.md) đã hoàn tất; xem [báo cáo evidence](docs/plan/implementation/reports/stage-01.md). Stage kế tiếp là Stage 02 (`PLANNED`).

1. Thực thi Stage 02: domain và application contracts.
2. Giữ file `LICENSE` và thông báo bản quyền MIT trong artifacts phát hành.
3. Giữ các gate Stage 03/05/06/08/10: state durability, PTY runtime, IPC, shortcut UX và agent compatibility.

Chỉ đánh dấu stage hoàn tất khi acceptance đạt và report evidence đã lưu.

## Tài liệu

- [PRODUCT.md](PRODUCT.md): ý tưởng, phạm vi sản phẩm và đặc tả kỹ thuật.
- [Kế hoạch tổng quan](docs/plan/implementation/README.md): milestones, dependencies, kiến trúc đề xuất, rủi ro và multi-agent execution.
- [Stage 00](docs/plan/implementation/00-product-contracts-and-spikes.md): contract/spike hoàn tất; [report](docs/plan/implementation/reports/stage-00.md).
- [Contributing](CONTRIBUTING.md): ownership và cách làm việc trong repo.
- [Development/testing](docs/testing/development.md): prerequisites, Makefile targets, CI và fake-agent contract.
- [.agents/AGENTS.md](.agents/AGENTS.md): quy tắc dùng skill và cách cập nhật Git subtree SKILLS.
- [V1 acceptance và release](docs/plan/implementation/10-v1-acceptance-and-release.md): tiêu chí bàn giao V1.

## License

License MIT; xem [LICENSE](LICENSE).
