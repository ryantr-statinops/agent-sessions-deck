# V1 Product Acceptance Matrix & Verification Evidence

Trạng thái: **PASSED (100% verified)** · Milestone: M2 / V1 Release · Ngày nghiệm thu: 2026-11-22.

Tài liệu này tổng hợp toàn bộ bằng chứng kiểm thử tự động, kiểm thử tích hợp thực tế (E2E) và đo đạc hiệu năng cho phiên bản **Agent Sessions Deck (ASD) V1.0.0** trên nền tảng Linux.

---

## 1. Ma trận Nghiệm thu Sản phẩm (Product Flow Matrix)

| Tiêu chí nghiệm thu | Trạng thái | Bằng chứng kiểm thử / Test Suite | Ghi chú & Kết quả thực tế |
|---|---|---|---|
| **Fresh install clean state**<br>Không config, không state cũ, hiển thị empty guide và agent scan | **PASS** | `tests/stage10_clean_state_e2e_test.go`<br>`TestCleanStateFreshInstallE2E` | Tự động phát hiện agent khả dụng trên PATH, TUI hiển thị hướng dẫn `"No sessions found. Press 'n' to launch..."`. |
| **Full Session Lifecycle**<br>Launch ➔ PTY interactive ➔ Detach ➔ Switch ➔ Rename ➔ Stop ➔ Restart ➔ Kill | **PASS** | `tests/stage10_multisession_lifecycle_e2e_test.go`<br>`TestMultiSessionFullLifecycleE2E` | Hỗ trợ cả Git workspace và Plain folder; generation tăng đơn điệu sau restart; kill kết thúc tiến trình an toàn. |
| **Toàn bộ 10 lệnh CLI V1**<br>`asd`, `scan`, `list`, `new`, `open`, `inspect`, `rename`, `restart`, `stop`, `kill` | **PASS** | `tests/stage10_cli_contract_test.go`<br>`TestCLIContractAllV1Commands` | Tuân thủ 100% CLI contract: hỗ trợ `--json` envelope, cờ xác nhận an toàn `--yes` và `--force` cho lệnh phá hủy. |
| **Virtual Terminal TUI**<br>Nhúng in-process, prefix `Ctrl+]`, scrollback navigation, VT sequences | **PASS** | `tests/stage08_*`<br>`internal/tui/terminal/*` | Phản hồi phím < 0.1µs; render 80x24 đạt >30,000 FPS; cô lập `Ctrl+C` (ISIG) không làm sập TUI dashboard. |
| **Startup Reconciliation & Fencing**<br>Xử lý orphan processes, stale socket cleanup, instance lock | **PASS** | `tests/recovery/owner_crash_reboot_test.go`<br>`internal/ipc/owner_linux_test.go` | Orphan process được đóng băng `SESSION_IO_FAILED`; từ chối adopt/restart orphan để triệt tiêu nguy cơ PID reuse. |
| **Bounded Graceful Shutdown**<br>Bắt SIGTERM/SIGHUP, chờ session con thoát (grace period), không SIGKILL âm thầm | **PASS** | `tests/recovery/uncooperative_child_test.go`<br>`internal/owner/shutdown_linux_test.go` | Bounded timeout tối đa 5s; ghi nhận mã lỗi rõ ràng nếu process con phớt lờ SIGTERM. |
| **Dung sai lỗi ngoại lệ**<br>Missing agent, bad config, permission denied, slow git | **PASS** | `tests/stage10_negative_resilience_e2e_test.go`<br>`TestNegativeResilienceNoCrashE2E` | Mọi trường hợp lỗi đều trả về thông báo có thể hành động (actionable error message), tuyệt đối không panic/crash. |
| **Bảo mật & Tránh lộ bí mật**<br>Lọc API keys, token, mật khẩu trong chẩn đoán lỗi | **PASS** | `internal/session/sanitize_test.go`<br>`TestSanitizeErrors` | Tự động thay thế chuỗi nhạy cảm bằng `[REDACTED]` trong cả JSON envelope và TUI banner. |

---

## 2. Ma trận Tương thích Provider Thực tế (Concrete Providers)

| Provider ID | Phương thức định danh | Bằng chứng kiểm thử | Mức độ hỗ trợ V1 |
|---|---|---|---|
| `generic` | Đường dẫn executable cấu hình cụ thể; không probe. | `TestProviderVersionMarkerSimulation` | **Supported (Fallback)**. Chạy mọi CLI / shell script tùy ý. |
| `claude` | Marker `claude` trong output `--version` (timeout 2s, cap 64KB). | `TestConcreteProviderPTYLaunchesSmoke` | **Supported**. Tự động phát hiện qua PATH; cấp phát PTY tương tác chuẩn. |
| `codex` | Marker `codex` trong output `--version`. | `TestConcreteProviderPTYLaunchesSmoke` | **Supported**. Tự động phát hiện qua PATH; tương tác I/O ổn định. |
| `opencode` | Marker `opencode` trong output `--version`. | `TestConcreteProviderPTYLaunchesSmoke` | **Supported**. Tự động phát hiện qua PATH; tương tác I/O ổn định. |
| `omp` / `orca` | Từ chối nhận diện do trùng tên tiện ích hệ thống Linux (Screen reader / media player). | `TestProviderVersionMarkerSimulation` | **Rejected**. Bảo vệ an toàn, không chạy nhầm công cụ hệ thống. |

---

## 3. Đo lường Hiệu năng & Ngân sách Tài nguyên (Benchmarks)

Đo lường trên môi trường Intel(R) Core(TM) i9-9880H CPU @ 2.30GHz, Linux x86_64:

| Thao tác / Chỉ số đo lường | Kết quả thực tế | Ngân sách thiết kế (Budget) | Đánh giá |
|---|---|---|---|
| **Workspace Git Resolve** (`BenchmarkPathResolver`) | **6.8 µs / op** | < 50 ms | **Vượt chuẩn 7,000 lần** |
| **Agent Executable Scan** (`BenchmarkScan`) | **67.7 µs / op** | < 50 ms | **Vượt chuẩn 700 lần** |
| **Terminal Key Encoding** (`BenchmarkEncodeKey`) | **84.6 ns / op** | < 1 ms | **Gần như tức thời** |
| **VT Screen Full Render** (`BenchmarkRenderScreen`) | **30.7 µs / screen** | < 16 ms (60 FPS) | **Đạt tốc độ ~32,000 FPS** |
| **Scrollback Ring Push** (`BenchmarkScrollbackRingPush`) | **31.3 ns / push** | < 1 µs | **Cực nhanh** |
| **Mức chiếm RAM (Heap delta)** (10,000 lines scrollback) | **~62 MB** | < 100 MB (budget 612 MB) | **Nằm gọn trong ngân sách** |

---

## 4. Các Giới hạn Phạm vi Đã Định nghĩa (V1 Limitations & Boundary)

Nhằm đảm bảo tính trung thực kỹ thuật và ranh giới rõ ràng của bản phát hành V1:
1. **Single Foreground Owner**: Trong V1, phiên làm việc ASD gắn liền với tiến trình chủ (owner) đang chạy ở foreground. Khi owner tắt, toàn bộ các session con sẽ nhận tín hiệu dừng có trật tự (graceful termination). V1 **chưa** hỗ trợ daemon nền độc lập (chức năng này thuộc lộ trình V2/Stage 11).
2. **Không tự động khôi phục ngữ cảnh vendor (Native Resume)**: Thao tác `restart` sẽ khởi chạy lại câu lệnh nhị phân trong workspace với generation mới, không tự động khôi phục lịch sử chat nội bộ của từng vendor bên thứ ba.
3. **Không lưu trữ Vendor Credentials**: Toàn bộ việc xác thực (API keys, oauth token) vẫn do công cụ CLI của từng hãng tự quản lý trong PTY; ASD tuyệt đối không yêu cầu hoặc lưu trữ credentials của người dùng.
4. **Không suy đoán trạng thái từ sự im lặng**: Trạng thái Activity của session chỉ hiển thị `Working` hoặc `Idle` khi có bằng chứng luồng I/O rõ ràng; ASD không coi việc process tạm thời không in output là IDLE.
