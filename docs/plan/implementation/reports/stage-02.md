# Stage 02 — Domain và application contracts

Trạng thái: `DONE` · Milestone: M1 · Nhánh: `dev`.

## Kết quả

- Đóng băng domain model cho Agent, Workspace, Session, Attempt, ProcessIdentity, three-axis lifecycle state, typed errors, reducer T1–T20, snapshots, ports, metadata events và application use cases.
- Reducer từ chối liveness probe trên `starting` khi chưa có ProcessIdentity; giữ attempt `starting` đến kết quả T2. `Attempt.Validate` cũng yêu cầu identity cho lifecycle `unknown`, nên một record không thể biến thành trạng thái reconciliation không thể giải quyết.
- `workspace.Git.Validate` từ chối branch rỗng trên non-detached HEAD dù working tree dirty.
- T14 chỉ ghi `NoteOrphaned` khi lifecycle sau probe là `running`; T14 khi `stopping` giữ đúng `stopping/unavailable` mà không gắn orphan note.
- T6 từ `running` chỉ nhận `natural-exit` hoặc `killed`; các reason `stopped`, `dead` và `stale-identity` bị từ chối ở đây. `killed` được giữ hợp lệ cho runtime report một reap đã xác nhận sau một lệnh kill chưa xác nhận; app regression `TestKillRecordsOnlyAConfirmedReap` pin hành vi đó.
- Durable JSON dùng `snake_case` cho nested `Reason`, `ProcessIdentity` và `Evidence`; tests pin key set và round-trip. Stage 03 phải thêm schema version và migration quanh wire format này.
- Chuyển Cobra root command khỏi `internal/app` sang `cmd/asd`; use-case package không còn import terminal framework. Build metadata được linker đặt trên package `main`.
- Không thêm production process, PTY hoặc persistent-store backend; các backend đó thuộc Stage 03/05 theo plan.

## Review

Independent review báo cáo không có P0 và một P1: probe T15/T18 có thể đẩy persisted `starting` không identity sang `unknown`, sau đó T3/T4 không thể giải quyết. Đã sửa bằng liveness guards và regression ở reducer/application: probe bị từ chối typed `CONFLICT`, state/revision/events không đổi. Direct reducer smoke in ra `T18 refused unidentified starting attempt; lifecycle=starting`.

Các P2 trong Stage 02 đã xử lý: Git validation, nested JSON key stability, orphan note trên `stopping`, identity requirement cho `unknown`, T6 reason restrictions và ranh giới Cobra/application. Reviewer cũng nêu các handoff dưới đây; không có P1 còn mở.

## Verification

Đã chạy trên Linux, offline với vendored modules:

- `make check` — `fmt-check`, `go vet`, unit suite, integration suite, build và `asd --help`/`asd --version` đều pass. Binary báo build version/commit/date từ linker flags.
- `make test-race` — unit + integration suite pass dưới `-race`.
- `make build-arm64` — `linux/arm64` compile pass; compile-only, không thực thi binary.
- `go run ./internal/session/stage02smoke` — one-off executable smoke cho reducer T18; file scaffold đã xóa sau khi chạy.
- `make clean` — đã xóa `bin/` và test cache.

## Handoff

- **Stage 03:** thêm `schema_version`, durable-store migration/atomic write và quyết định compatibility policy cho snake_case format.
- **Stage 05/09:** triển khai process/PTY runtime, child wait/reap, identity-verified signals, crash recovery và reconciliation. Stage 02 không có backend production để kiểm chứng FD/goroutine leaks.
- **Stage 06:** ADR 0003 request-ID dedup chưa có trong `SessionStore`/`Client`; thêm request ID cho retry an toàn và expose `Report` qua client surface.
- **Stage 09:** import-boundary guard hiện chưa bao phủ `internal/events`; bổ sung guard nếu architecture suite được tái lập. Review cũng ghi nhận Restart không reconcile exit của observer khác (hiện không reachable khi mutations cùng serialize qua `mutMu`) và lỗi `apply(spawned)` sau Launch thành công (hiện chỉ reachable nếu reducer vi phạm contract); xem lại khi thay đổi concurrency/launch transaction.

Stage 02 đóng băng hợp đồng, không khẳng định runtime ownership, PTY survival hoặc crash recovery đã được triển khai.
