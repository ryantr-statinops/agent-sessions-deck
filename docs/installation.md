# Installation and Lifecycle Management Guide

Tài liệu hướng dẫn cài đặt, thiết lập biến môi trường, sao lưu/khôi phục dữ liệu và gỡ bỏ hoàn toàn **Agent Sessions Deck (`asd`)** trên hệ điều hành Linux.

---

## 1. Yêu cầu Hệ thống (System Requirements)

- **Hệ điều hành**: Linux (hạt nhân kernel ≥ 4.18, hỗ trợ chuẩn `/proc`, PTY, Unix domain sockets).
- **Kiến trúc CPU**: `x86_64` (amd64) hoặc `aarch64` (arm64).
- **Môi trường đầu cuối (Terminal)**: Trình giả lập terminal hỗ trợ ANSI 256 màu / truecolor và bộ phím VT (ví dụ: Alacritty, Kitty, WezTerm, GNOME Terminal, Tmux).
- **Go toolchain (nếu build từ mã nguồn)**: Go ≥ 1.26.0 (đã tích hợp đầy đủ offline vendoring).

---

## 2. Cài đặt Binary (Pre-built Release)

### Cách 1: Tải bản phát hành chính thức (Khuyên dùng)

1. Tải gói phát hành phù hợp với kiến trúc máy của bạn từ GitHub Releases:
   ```bash
   # Cho kiến trúc Linux x86_64 (amd64)
   curl -LO https://github.com/ryantr-statinops/agent-sessions-deck/releases/latest/download/asd_v1.0.0_linux_amd64.tar.gz
   curl -LO https://github.com/ryantr-statinops/agent-sessions-deck/releases/latest/download/sha256sums.txt
   ```

2. Kiểm tra tính toàn vẹn gói tin bằng SHA-256:
   ```bash
   sha256sum --ignore-missing -c sha256sums.txt
   # Kết quả mong muốn: asd_v1.0.0_linux_amd64.tar.gz: OK
   ```

3. Giải nén và đưa binary vào thư mục thực thi người dùng (`~/.local/bin`):
   ```bash
   tar -xzf asd_v1.0.0_linux_amd64.tar.gz
   mkdir -p ~/.local/bin
   install -m 755 asd_v1.0.0_linux_amd64/asd ~/.local/bin/asd
   ```

4. Đảm bảo `~/.local/bin` đã có trong biến môi trường `$PATH` của shell (thêm vào `~/.bashrc` hoặc `~/.zshrc` nếu chưa có):
   ```bash
   export PATH="$HOME/.local/bin:$PATH"
   ```

5. Kiểm tra cài đặt thành công:
   ```bash
   asd --version
   asd scan
   ```

---

### Cách 2: Biên dịch trực tiếp từ mã nguồn (Build from source)

```bash
git clone https://github.com/ryantr-statinops/agent-sessions-deck.git
cd agent-sessions-deck
make check
make build
install -m 755 bin/asd ~/.local/bin/asd
```

---

## 3. Vị trí Lưu trữ Dữ liệu & Cấu hình (XDG Directories)

Theo chuẩn XDG Base Directory:
- **Tệp cấu hình**: `~/.config/asd/config.yaml` (Tùy chọn, không bắt buộc khi mới cài đặt).
- **Dữ liệu phiên (Store & State)**: `~/.local/state/asd/`
  - `store.json`: Cơ sở dữ liệu ghi nhận trạng thái toàn bộ session.
  - `owner.lock`: File khóa chủ quyền của owner process hiện thời.
  - `owner.sock`: Unix domain socket phục vụ giao tiếp IPC cục bộ.
- **Runtime Socket**: `~/.local/run/asd/` hoặc `$XDG_RUNTIME_DIR/asd/`.

---

## 4. Sao lưu và Khôi phục Trạng thái (Backup & State Reset)

### Sao lưu dữ liệu (Backup)
Để lưu trữ lại toàn bộ danh mục phiên và lịch sử cấu hình:
```bash
cp ~/.local/state/asd/store.json ~/.local/state/asd/store.json.bak
```

### Khôi phục hoặc Đặt lại hoàn toàn (Clean State Reset)
Nếu muốn làm sạch toàn bộ dữ liệu phiên làm việc về trạng thái ban đầu:
1. Đảm bảo tiến trình owner đã dừng hoàn toàn (nhấn `q` trong TUI hoặc `Ctrl+C`).
2. Xóa dữ liệu phiên cũ:
   ```bash
   rm -rf ~/.local/state/asd
   ```
3. Lần khởi động tiếp theo của `asd` sẽ tự động tạo lại môi trường trắng tinh và hướng dẫn người dùng qua màn hình trống.

---

## 5. Gỡ bỏ Hoàn toàn (Uninstallation)

Để gỡ bỏ `asd` khỏi hệ thống:
```bash
# 1. Xóa file nhị phân
rm -f ~/.local/bin/asd

# 2. Xóa toàn bộ file cấu hình và dữ liệu phiên
rm -rf ~/.config/asd
rm -rf ~/.local/state/asd
rm -rf ~/.local/run/asd
```

---

## 6. Giới hạn Phạm vi V1 (Foreground Owner Limitations)

- **Owner duy nhất**: `asd` trong phiên bản V1 chạy dưới dạng một tiến trình foreground (không phải background systemd daemon). 
- Khi bạn đóng cửa sổ terminal giữ owner process, các phiên session con đang chạy sẽ được thông báo dừng tuần tự an toàn (`SIGTERM` với grace period 5 giây).
- Tính năng chạy nền độc lập với TUI (Daemonization) sẽ được ra mắt trong bản V2 (Stage 11).
