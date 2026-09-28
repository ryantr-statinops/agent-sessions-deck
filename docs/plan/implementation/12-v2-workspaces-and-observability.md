# Stage 12 — V2 workspace view và observability

Trạng thái: `PLANNED` · Milestone: M3 · Phụ thuộc: 11 · Cỡ việc: M.

## Mục tiêu

Nhìn các sessions theo project/workspace và đủ thông tin process để hiểu đang chạy gì, mà không biến ASD thành system monitor toàn máy.

## Phạm vi và đầu ra

Workspace projections tại `internal/workspace/`, `internal/metrics/`, daemon pollers, workspace/dashboard/details tại TUI, `asd workspace list`, `docs/observability.md`.

## Checklist thực thi

- [ ] Projection workspace → logical sessions → attempts; preserve launch cwd khác repo root, grouped repo option rõ và stable workspace identity.
- [ ] Workspace list có active/historical counts, agent/name/status, branch/dirty; navigation tới terminal/inspect cùng service contracts.
- [ ] Linux metrics sampler đọc CPU/memory/starttime/process tree cho verified ASD attempts; không theo dõi arbitrary agent binary toàn máy.
- [ ] CPU percent theo interval, units và normalization được định nghĩa; memory là RSS hay aggregate descendants ghi rõ, không double count shared pages như private memory.
- [ ] Uptime dùng attempt start time/monotonic clock cho live measurement, không logical session created time sau restart.
- [ ] Descendant process attribution verify parent/start identity; detached processes không tự adopt từ similarity cwd/command.
- [ ] Per-field `unavailable/permission denied/stale` + sampled_at; last ASD event khác provider semantic event, không bịa “tool execution” từ PTY text.
- [ ] Git refresh cached/throttled, timeout và configured interval; dirty state không block terminal input.
- [ ] Sampling pause/backoff khi không cần detail hoặc theo daemon policy; bounded concurrency và no FD leak.
- [ ] CLI JSON workspace/inspect metrics có unit/timestamp/null reason; TUI compact fallback cho small terminal.

## Acceptance và verification

- Workspaces plain folder/nested Git/worktree chứa nhiều agents được group đúng, search và open tới đúng attempt.
- Fake proc/clock fixtures kiểm tra CPU delta, exit/PID reuse, permission denial và process tree attribution.
- Budget sampler đo được dưới workload 20 sessions, UI/input không chậm rõ vì metrics refresh.
- Child spawn/exit lúc sampling không crash daemon; stale observation hiển thị khác current metric.

## Phân công và handoff

Metrics worker giữ sampler/proc parsing; UI worker giữ workspace view sau DTO freeze. Handoff cho 13–15 là workspace IDs, summaries, metric semantics và no-provider-semantic-event limitation.

Prompt thực thi: “Thực thi Stage 12, xây workspace grouping và Linux metrics có identity/timestamps/units. Không scan process ngoài ASD hoặc diễn giải terminal text thành agent activity.”
