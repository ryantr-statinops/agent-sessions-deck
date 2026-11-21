# Agent Session Deck

Agent Session Deck (`asd`) là ứng dụng CLI với giao diện terminal (TUI), giúp developer chọn coding-agent CLI, chọn workspace, khởi chạy và tương tác với session từ một giao diện local.

Luồng sử dụng dự kiến:

```text
asd → chọn agent → chọn workspace → chạy → tương tác
```

ASD chạy các công cụ coding agent thực tế, như Claude Code, Codex CLI, OpenCode hoặc command do người dùng cấu hình. ASD quản lý process và terminal của những session do chính nó khởi tạo.

## Trạng thái hiện tại

Dự án đã hoàn tất các Stage từ 00 đến 10 (toàn bộ các mốc **M0, M1, và M2 / V1 Release** đã hoàn thành 100%). Stage 11 (V2 daemon và client migration) là bước kế tiếp thuộc Milestone M3.

| Hạng mục | Trạng thái |
|---|---|
| Đặc tả sản phẩm | Đã hoàn thiện trong [PRODUCT.md](PRODUCT.md) |
| Kế hoạch triển khai | Đã có 16 stage trong [docs/plan/implementation/](docs/plan/implementation/README.md) |
| Stage thực thi đã hoàn thành | 11/16; Stage 00–10 `DONE`, Stage 11–15 `PLANNED` |
| Mốc đã hoàn tất | **M0, M1, M2 (V1 Release)** đã nghiệm thu đầy đủ |
| Stage tiếp theo | [Stage 11 — V2 daemon và client migration](docs/plan/implementation/11-v2-daemon-and-client-migration.md) (`PLANNED`) |
| Mã nguồn, Go module, build và tests | Toàn bộ V1 runtime, CLI, TUI, virtual terminal, recovery hardening và V1 acceptance E2E đạt 100% test pass |
| Bản phát hành và tài liệu cài đặt | Đầy đủ bản đóng gói phát hành Linux amd64/arm64 và hướng dẫn tại [docs/installation.md](docs/installation.md) |

Repo đã có đầy đủ mã nguồn Go, toàn bộ CLI (`scan`, `list`, `inspect`, `new`, `open`, `rename`, `restart`, `stop`, `kill`), TUI đa năng (Dashboard, Inspector, Launcher, Virtual Terminal đa phiên), bộ kiểm thử phục hồi lỗi và ma trận nghiệm thu V1. License MIT.


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

[Stage 10](docs/plan/implementation/10-v1-acceptance-and-release.md) đã hoàn tất; xem [báo cáo Stage 10](docs/plan/implementation/reports/stage-10.md). Toàn bộ mục tiêu Milestone M2 (V1 Release) đã được nghiệm thu và đóng gói hoàn tất.

Bước tiếp theo thuộc Milestone M3:
- [Stage 11 — V2 daemon và client migration](docs/plan/implementation/11-v2-daemon-and-client-migration.md) (`PLANNED`): Tách biệt vòng đời session khỏi TUI qua daemon `agentd`.

## Tài liệu

- [PRODUCT.md](PRODUCT.md): ý tưởng, phạm vi sản phẩm và đặc tả kỹ thuật.
- [Hướng dẫn cài đặt và gỡ bỏ](docs/installation.md): cài đặt binary, biến môi trường, sao lưu và gỡ bỏ.
- [Ma trận nghiệm thu V1](docs/testing/v1-acceptance.md): bằng chứng kiểm thử, tương thích provider và đo lường benchmark.
- [Changelog](CHANGELOG.md): ghi chú phát hành các phiên bản.
- [Hướng dẫn sử dụng TUI](docs/usage.md): điều hướng dashboard, launcher và phím tắt.
- [Virtual Terminal View](docs/terminal.md): kiến trúc terminal ảo, ANSI renderer và prefix machine.
- [Phục hồi lỗi và vòng đời](docs/recovery.md): mô hình recovery, xử lý orphan, instance fencing và shutdown có bounded.
- [Hướng dẫn khắc phục sự cố](docs/troubleshooting.md): mã lỗi, giải pháp và kinh nghiệm vận hành.
- [CLI Reference](docs/cli.md): cú pháp và hành vi lệnh CLI offline và online.
- [Cấu hình](docs/configuration.md): định dạng tệp cấu hình YAML theo XDG.
- [Kế hoạch tổng quan](docs/plan/implementation/README.md): milestones, dependencies, báo cáo từng stage và orchestration.
- [Contributing](CONTRIBUTING.md): ownership và cách làm việc trong repo.
- [Development/testing](docs/testing/development.md): prerequisites, Makefile targets, CI và fake-agent contract.
- [.agents/AGENTS.md](.agents/AGENTS.md): quy tắc dùng skill và cách cập nhật Git subtree SKILLS.
- [Báo cáo nghiệm thu Stage 10](docs/plan/implementation/reports/stage-10.md): tổng kết kết quả V1 release.


## License

License MIT; xem [LICENSE](LICENSE).
