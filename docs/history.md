# V2 History & Event Persistence User Guide

Tài liệu hướng dẫn truy vấn lịch sử thực thi, nhật ký sự kiện và cấu hình lưu trữ retention trong Agent Sessions Deck (ASD) V2.

---

## 1. Tổng quan Kiến trúc Lưu trữ V2

Trong ASD V2, kiến trúc lưu trữ được chuẩn hóa theo quyết định [ADR 0005](architecture/adr/0005-history-and-event-persistence.md):
- **Snapshot Chỉ mục Phiên làm việc (`sessions.json`):** Lưu trữ snapshot trạng thái và lịch sử toàn bộ các execution attempts của từng session. Quá trình restart tạo attempt mới với generation tăng dần mà không bao giờ ghi đè dữ liệu quá khứ.
- **Nhật ký Sự kiện Append-Only (`events.jsonl`):** Lưu trữ tuần tự các sự kiện vòng đời (`events.Event`) với số hiệu revision tăng đơn điệu (`monotonic revision sequence`). Đảm bảo subscribers và clients có thể resync không bị thiếu sót sự kiện sau khi daemon khởi động lại.
- **Cam kết Bảo mật Tuyệt đối (Zero Secrets Policy):** File lịch sử và journal chỉ lưu trữ metadata có cấu trúc (timestamps, durations, exit codes, process PIDs, provenance argv). Tuyệt đối **không bao giờ** ghi raw PTY terminal output, prompts, token hoặc credentials vào đĩa.

---

## 2. Truy vấn Lịch sử Phiên làm việc (`asd history`)

Lệnh `asd history` cung cấp giao diện truy vấn toàn bộ các phiên làm việc đã tạo, đang chạy hoặc đã kết thúc:

```bash
# Xem danh sách lịch sử mặc định (20 sessions mới nhất)
asd history

# Xem chi tiết từng execution attempt trong mỗi session
asd history --attempts

# Lọc theo agent ID hoặc workspace
asd history --agent opencode --workspace ~/Projects/agent-sessions-deck

# Lọc theo trạng thái (active, finished, all)
asd history --status finished

# Lọc theo thời gian tạo (RFC3339) và giới hạn số lượng kết quả
asd history --since 2026-11-01T00:00:00Z --limit 50

# Xuất dữ liệu định dạng JSON có cấu trúc chuẩn
asd history --json
```

### Ví dụ Bảng Kết quả Nhân tính (Human-Readable Table):
```text
SESSION ID  NAME            AGENT     STATUS    ATTEMPTS  CREATED              WORKSPACE
s-000001    kestrel-worker  opencode  exited    2         2026-11-26 10:00:00  /workspaces/default
  └─ gen 1                  exited    10:00:00 (30m0s)    exit=0 natural_exit(0)
  └─ gen 2                  exited    10:31:00 (29m0s)    exit=0 natural_exit(0)
```

---

## 3. Truy vấn Nhật ký Sự kiện (`asd events`)

Lệnh `asd events` cho phép theo dõi và lọc nhật ký sự kiện hệ thống:

```bash
# Xem 50 sự kiện mới nhất
asd events

# Lọc theo session ID cụ thể
asd events --session s-000001

# Lọc theo loại sự kiện (event type)
asd events --type session.started

# Đọc sự kiện sau một revision cụ thể (cursor-based pagination/resync)
asd events --after 42 --limit 100

# Xuất định dạng JSON cho tự động hóa và logging
asd events --json
```

---

## 4. Cấu hình & Dọn dẹp Hạn mức Lưu trữ (Retention)

### Cấu hình trong `config.yaml`
Bạn có thể định cấu hình hạn mức retention trong tệp `$XDG_CONFIG_HOME/asd/config.yaml`:

```yaml
retention:
  # Số attempt tối đa lưu lại cho mỗi session (mặc định: 50)
  max_attempts_per_session: 50

  # Số lượng sự kiện tối đa lưu trong journal events.jsonl (mặc định: 10000)
  max_events: 10000

  # Thời hạn TTL cho sự kiện journal (mặc định: 720h = 30 ngày)
  max_event_age: "720h"

  # Thời hạn TTL cho session đã kết thúc (mặc định: 2160h = 90 ngày)
  max_finished_session_age: "2160h"
```

### Lệnh Dọn dẹp Thủ công (`asd history prune`)

```bash
# Kiểm tra mô phỏng dọn dẹp trước khi thực hiện (an toàn tuyệt đối)
asd history prune --dry-run

# Thực hiện dọn dẹp theo cấu hình mặc định
asd history prune

# Ghi đè tham số tạm thời cho lần dọn dẹp hiện tại
asd history prune --max-attempts 20 --max-events 5000 --max-event-age 168h
```

> [!IMPORTANT]
> **Quy tắc Bảo vệ Tiến trình Đang chạy (Active Record Protection):**
> Quá trình prune **nghiêm cấm** xóa bỏ bất kỳ session nào đang ở trạng thái active (`running`, `created`, `stopping`, `unknown`) hoặc attempt hiện tại đang thực thi, bất kể thời gian hoạt động kéo dài bao lâu.

---

## 5. Sao lưu và Phục hồi (Backup & Restore)

Tất cả trạng thái bền vững của ASD nằm trong thư mục `$XDG_STATE_HOME/asd` (hoặc `~/.local/state/asd`):
- `sessions.json`: Danh mục phiên làm việc và toàn bộ attempts.
- `events.jsonl`: Nhật ký chuỗi sự kiện tuần tự.

### Sao lưu (Backup)
```bash
mkdir -p ~/asd-backups
cp ~/.local/state/asd/sessions.json ~/asd-backups/sessions-$(date +%F).json
cp ~/.local/state/asd/events.jsonl ~/asd-backups/events-$(date +%F).jsonl
```

### Phục hồi (Restore)
1. Dừng daemon nền nếu đang chạy: `asd daemon stop`
2. Khôi phục các tệp tin từ bản sao lưu:
   ```bash
   cp ~/asd-backups/sessions-2026-11-26.json ~/.local/state/asd/sessions.json
   cp ~/asd-backups/events-2026-11-26.jsonl ~/.local/state/asd/events.jsonl
   ```
3. Khởi động lại daemon: `asd daemon start` hoặc khởi chạy `asd`.
