# Báo cáo Stage 00 — Product contracts và technical spikes

Ngày chạy cuối: 2026-09-29 · Trạng thái: DONE theo acceptance của Stage 00.

Stage này hoàn tất các contract và spike độc lập; không thêm mã nguồn production, root `go.mod`, build/test suite hay binary `asd`. Các ADR vẫn là đề xuất cho maintainer phê duyệt trước khi trở thành product fact.

## Deliverables

- Contract và đề xuất PRODUCT: `PRODUCT.md`, `docs/architecture/cli-contract.md`, `docs/architecture/session-state-machine.md`.
- Quyết định đề xuất: ADR 0001 (foreground owner/V1–V2), ADR 0002 (PTY/emulator), ADR 0003 (XDG/state/concurrency).
- Terminal spike có lệnh rerun và cleanup: `experiments/terminal/`.
- Ma trận evidence và giới hạn: `docs/testing/terminal-compatibility.md`.

## Verification

Từ repository root:

```bash
timeout 300 bash experiments/terminal/run.sh
bash experiments/terminal/cleanup.sh
```

Lần chạy cuối kết thúc `SPIKE RESULT: ALL PROBES PASSED`; `cleanup: OK`, không còn `experiments/terminal/.run`. Kết quả: PTY 8/8, query/response 4/4, alt-screen transport 6/6, two-session PTY 4/4, corpus 10/10; Go fidelity 32/32 blocking PASS cùng một gap có chủ đích `gap_vt_sco_save_restore` (SCO save/restore không có trong bản `x/vt` đã pin). Evidence được tái tạo tại `experiments/terminal/evidence/`.

Twin two-session fake-fullscreen integration: 7/7 blocking PASS. Hai PTY/emulator riêng biệt; A tiếp tục nhận output khi B được xem; input và resize cô lập; reattach lấy frame mới nhất; không có screen cross-contamination. Screen payload không đi vào runner stdout.

Baseline quan sát một lần trên host này, không phải SLO hay ngưỡng production:

- `x/vt` `Render()` tối đa 153.445 µs trên 100 lần đo ở 80×24.
- Input-to-echo: 1.390 ms (B), 1.577 ms (A); đo bằng polling 1 ms.
- Resize-to-`WINCH`: 2.259 ms; đo bằng polling 1 ms.
- PTY drain 1 MiB: 0.03 s (~36,683 KiB/s) trong lần chạy này.

`git diff --check` pass sau lần chạy. Harness chỉ dùng PTY riêng, không đổi termios hay ghi escape sequence vào terminal gọi nó; cleanup không quét `/tmp` hay kill theo tên process.

## Quyết định và giới hạn được bàn giao

- ADR 0001–0003 và CLI/state contracts ở trạng thái Proposed, không phải phê duyệt maintainer.
- `creack/pty` v1.1.24 được đề xuất cho prototype; `charmbracelet/x/vt` được đề xuất có điều kiện ở pseudo-version đã pin. `x/vt` không có tagged release; SCO save/restore thiếu, query identity chưa được xác nhận với agent thật.
- Twin integration dùng fake children CUP+EL, không phải coding agents và không dùng alt-screen trong vòng twin. Chưa có production compatibility claim.
- Không chạy interactive agent CLI vì điều kiện xác thực/chi phí. Cần smoke matrix agent thật trước Stage 10; agent compatibility vẫn chưa được chứng minh.
- Chưa đo flood/RAM ở 20 session, dung lượng paste lớn, UX shortcut thực tế, cross-check với tmux hay PTY ngoài Linux. Ctrl+] cùng literal-prefix, Ctrl+C và terminal-restore được ghi là contract Proposed, Stage 08 cần kiểm chứng.
- Maintainer cần chọn license trước public release; module identity được chọn ở Stage 01; runtime-dir fallback ở Stage 06; store atomicity/layout ở Stage 03.

## Handoff

Stage 01 tiếp theo: chọn module path/toolchain theo repo identity, dựng build/test/CI baseline và fake agent theo `docs/plan/implementation/01-repository-and-engineering-baseline.md`. Giữ các gate tương ứng ở Stage 03/05/06/08/10; không diễn giải kết quả spike thành đảm bảo tương thích production.
