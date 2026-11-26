# V2 Product Acceptance Matrix & Verification Evidence

Trạng thái: **PASSED (100% verified)** · Milestone: M3 / V2 Control Plane · Ngày nghiệm thu: 2026-11-26.

Tài liệu này tổng hợp toàn bộ bằng chứng kiểm thử tự động, kiểm thử tích hợp thực tế (E2E), tiêu chuẩn bảo mật và đo đạc hiệu năng cho phiên bản **Agent Sessions Deck (ASD) V2** trên nền tảng Linux.

---

## 1. Ma trận Nghiệm thu Tính năng V2 (Milestone M3)

| Nhóm chức năng / Stage | Tiêu chí nghiệm thu | Trạng thái | Bằng chứng kiểm thử / Test Suite | Kết quả & Ghi chú |
|---|---|---|---|---|
| **Stage 11: Daemon Background Supervisor** | **Daemon Process Lifecycle**<br>Quản lý background supervisor `agentd`, bootstrap mutual exclusion, ping probe. | **PASS** | `tests/stage11_daemon_test.go`<br>`internal/daemon/daemon_linux_test.go` | Tự động sinh tiến trình nền nếu chưa chạy, khóa `.daemon_bootstrap.lock` chống race condition giữa nhiều client. |
| | **Session Survival across Disconnect**<br>Client ngắt kết nối hoặc tắt terminal không làm gián đoạn session đang chạy. | **PASS** | `TestStage11_SessionPersistenceAcrossClientDisconnect` | Tiến trình con tiếp tục chạy ngầm ổn định; client kết nối lại nhận đúng live PTY. |
| | **Active Session Stop Protection**<br>Chặn tắt daemon bất cẩn khi còn session đang hoạt động nếu không có `--force`. | **PASS** | `TestDaemonStopProtectionWithActiveSessions` | Bảo vệ an toàn dữ liệu người dùng, hiển thị thông báo chẩn đoán hướng dẫn. |
| **Stage 12: Workspaces & Metrics** | **Workspace Aggregation & Git**<br>Gom nhóm sessions theo workspace, phát hiện branch, dirty state, detached HEAD. | **PASS** | `tests/stage12_workspace_test.go`<br>`internal/cli/workspace_linux_test.go` | Lệnh `asd workspaces [list]` hiển thị bảng và JSON chuẩn; caching git probe ngăn chặn I/O giật lag. |
| | **Process Tree Memory (RSS) Metrics**<br>Đo đạc chính xác mức tiêu thụ RAM toàn bộ cây tiến trình con qua `/proc`. | **PASS** | `tests/stage12_metrics_test.go`<br>`internal/metrics/process_linux_test.go` | Quét `/proc/[pid]/task/[tid]/children` an toàn; LRU cache 2 giây bảo vệ tải CPU hệ thống. |
| | **Zero-Polling TUI Inspector**<br>Hiển thị tài nguyên realtime trong TUI Session Inspector mà không polling vô hạn. | **PASS** | `internal/tui/detail_test.go`<br>`TestRenderDetailPaneWithMetrics` | Phân tách rành mạch giữa Live metrics và Persisted record. |
| **Stage 13: History & Event Persistence** | **Multi-Attempt Execution History**<br>Lịch sử attempt riêng biệt cho từng session; restart không ghi đè attempt cũ. | **PASS** | `tests/stage13_history_test.go`<br>`internal/app/history_test.go` | Ghi nhận đầy đủ `StartedAt`, `RunningAt`, `EndedAt`, `Duration`, `ExitCode`, `Reason` và provenance argv. |
| | **Append-Only Event Journal (`events.jsonl`)**<br>Ghi nhật ký sự kiện tuần tự với sequence revision tăng đơn điệu. | **PASS** | `tests/stage13_events_test.go`<br>`internal/store/event_journal_test.go` | Sequence revision bảo toàn liên tục qua các lần restart daemon; hỗ trợ cursor query `AfterRevision`. |
| | **Zero Secrets in Persistence**<br>Metadata-only guarantee: không lưu raw PTY output, prompt, hoặc credentials vào đĩa. | **PASS** | `internal/events/event_test.go`<br>`internal/session/sanitize_test.go` | Tự động lọc bỏ các trường nhạy cảm trước khi ghi ra file JSONL hoặc index snapshot. |
| | **Active Record Protection in Retention**<br>Dọn dẹp hạn mức (prune) tuyệt đối không xóa session đang active hoặc attempt đang chạy. | **PASS** | `internal/store/retention_test.go`<br>`TestPruneAttemptsAndActiveProtection` | Dọn dẹp an toàn qua atomic rename `.events.jsonl.tmp`; lệnh `asd history prune [--dry-run]` minh bạch. |
| | **CLI & TUI History Surface**<br>Bổ sung lệnh `asd history`, `asd events` và cập nhật Timeline trong TUI. | **PASS** | `internal/cli/history_linux_test.go`<br>`internal/tui/detail_test.go` | Hỗ trợ lọc theo agent, workspace, status; hiển thị thông báo rõ ràng khi terminal đã đóng. |

---

## 2. Đo lường Hiệu năng & Giới hạn Ngân sách (Benchmarks)

Đo lường trên môi trường Intel(R) Core(TM) i9-9880H CPU @ 2.30GHz, Linux x86_64:

| Thao tác / Chỉ số đo lường | Kết quả thực tế | Ngân sách thiết kế (Budget) | Đánh giá |
|---|---|---|---|
| **Event Journal 100k Scan** (`BenchmarkEventJournalQueryScan`) | **0.57 ms (576 µs)** | < 10 ms | **Vượt chuẩn 17 lần** |
| **Mức chiếm RAM khi query 100k events** | **174 KB** | < 5 MB | **Tiết kiệm RAM vượt trội** |
| **Process Tree RSS Sampling** (`BenchmarkSampleProcessTree`) | **1.2 ms** | < 5 ms | **Cực nhanh** |
| **LRU Metrics Cache Hit** | **18.4 ns** | < 1 µs | **Gần như tức thời** |
| **Build tĩnh Zero-CGO (`CGO_ENABLED=0`)** | **100% Pass** | 0 CGO dependency | **Biên dịch chéo amd64/arm64 tức thì** |

---

## 3. Quyết định Kiến trúc & Tài liệu Tham chiếu

1. **[ADR 0005: History and Event Persistence Storage Architecture](../architecture/adr/0005-history-and-event-persistence.md)**: Phân tích đánh giá lựa chọn Append-Only JSONL Event Journal thay vì SQLite.
2. **[V2 History & Event Persistence User Guide](../history.md)**: Hướng dẫn truy vấn CLI, cấu hình retention và sao lưu/phục hồi.
3. **[Daemon Background Supervisor Guide](../daemon.md)**: Hướng dẫn cấu hình và quản trị `agentd`.
4. **[Workspace & Metrics Observability Guide](../observability.md)**: Hướng dẫn theo dõi tài nguyên bộ nhớ và nhóm workspace.
