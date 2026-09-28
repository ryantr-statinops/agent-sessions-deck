# Agent Session Deck

Agent Session Deck (`asd`) là ứng dụng CLI với giao diện terminal (TUI), giúp developer chọn coding-agent CLI, chọn workspace, khởi chạy và tương tác với session từ một giao diện local.

Luồng sử dụng dự kiến:

```text
asd → chọn agent → chọn workspace → chạy → tương tác
```

ASD chạy các công cụ coding agent thực tế, như Claude Code, Codex CLI, OpenCode hoặc command do người dùng cấu hình. ASD quản lý process và terminal của những session do chính nó khởi tạo.

## Trạng thái hiện tại

**Dự án đang ở giai đoạn lập kế hoạch, trước khi triển khai Stage 00.**

| Hạng mục | Trạng thái |
|---|---|
| Đặc tả sản phẩm | Đã có bản draft trong [PRODUCT.md](PRODUCT.md) |
| Kế hoạch triển khai | Đã có 16 stage trong [docs/plan/implementation/](docs/plan/implementation/README.md) |
| Stage thực thi đã hoàn thành | 0/16; tất cả đang ở trạng thái `PLANNED` |
| Stage tiếp theo | [Stage 00 — Product contracts và technical spikes](docs/plan/implementation/00-product-contracts-and-spikes.md) |
| Mã nguồn, Go module, build và tests | Chưa triển khai |
| Bản phát hành có thể cài đặt | Chưa có |

Hiện repo chứa tài liệu sản phẩm và kế hoạch. Command `asd`, các tính năng và stack bên dưới là mục tiêu triển khai; chưa có binary hoặc hướng dẫn chạy ứng dụng ở thời điểm này.

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

Các lựa chọn cụ thể, library versions và lifecycle semantics sẽ được chốt sau kiểm chứng kỹ thuật ở Stage 00. Thiết kế đề xuất được giải thích trong [kế hoạch tổng quan](docs/plan/implementation/README.md#quyết-định-kiến-trúc-đề-xuất).

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

[Stage 00](docs/plan/implementation/00-product-contracts-and-spikes.md) tập trung vào ba việc trước khi viết tính năng production:

1. Chốt contract cho session lifecycle, process ownership, CLI và storage.
2. Kiểm chứng PTY, terminal rendering, input/resize và việc chuyển giữa nhiều session.
3. Ghi quyết định kiến trúc và bằng chứng kiểm thử làm đầu vào cho các stage tiếp theo.

Khi thực thi một stage, cập nhật trạng thái trong tài liệu stage và kế hoạch tổng quan, lưu report có bằng chứng, rồi cập nhật mục **Trạng thái hiện tại** của README này. Chỉ đánh dấu hoàn thành khi đạt tiêu chí nghiệm thu.

## Tài liệu

- [PRODUCT.md](PRODUCT.md): ý tưởng, phạm vi sản phẩm và đặc tả kỹ thuật.
- [Kế hoạch tổng quan](docs/plan/implementation/README.md): milestones, dependencies, kiến trúc đề xuất, rủi ro và multi-agent execution.
- [Stage 00](docs/plan/implementation/00-product-contracts-and-spikes.md): điểm bắt đầu triển khai.
- [V1 acceptance và release](docs/plan/implementation/10-v1-acceptance-and-release.md): tiêu chí bàn giao V1.

## License

Chưa chọn license. Quyết định license nằm trong kế hoạch và cần được chốt trước khi phát hành public.
