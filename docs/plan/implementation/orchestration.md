# Kế hoạch orchestration triển khai

## Mục đích và trạng thái

Tài liệu này gộp các stage tương lai thành execution wave có dependency rõ và cách giao việc cho Orca. Các stage 00–02 đã hoàn tất vẫn giữ nguyên ID, report và evidence; không gộp lại hoặc đổi lịch sử. Stage 03–15 vẫn là các checklist/acceptance con. Một wave chỉ `DONE` khi mọi gate của wave và các stage con tương ứng có evidence; việc merge code hoặc worker báo xong chưa đủ.

**Ưu tiên:** hoàn thành M1–M2 để giao V1 qua Stage 10; chỉ mở M3/V2 và M4/V3 sau khi xem lại nhu cầu thực tế, như implementation README đã yêu cầu.

## Bốn execution wave

| Wave | Stage được gộp | Mục tiêu/gate | DAG điều phối |
|---|---|---|---|
| **W1 — Core V1 runtime (M1)** | 03–06 | Config/store an toàn; discovery/workspace và runtime chạy được; CLI/IPC điều khiển một owner. Gate: fake agent chạy qua CLI, đọc trạng thái từ terminal khác; toàn bộ contract/retry/ownership acceptance của 06 pass. | `03 → (04 ∥ 05) → 06`. Đóng băng config DTO, store/lock API và path/failure policy trước khi 04/05 cùng chạy. 06 chỉ bắt đầu sau khi 04 và 05 hoàn tất. |
| **W2 — V1 UX và release (M2)** | 07–10 | Hoàn thành launcher/dashboard, terminal tương tác, recovery; qua V1 acceptance trên Linux. Gate: Stage 10 có end-to-end evidence, concrete provider matrix và release limitations chính xác. | `07 → 08 → 09 → 10`. 09 cần 06+08; 10 cần 04+07+08+09. Có thể chạy docs/QA bounded song song nhưng không được bỏ các dependency này. |
| **W3 — V2 daemon và history** | 11–13 | Chuyển owner sang daemon, sau đó workspace/metrics và event/history persistence. Gate V2 chỉ đóng sau acceptance của cả 12 và 13. | `11 → (12 ∥ 13-core) → 13/V2-gate`. Stage 13 có thể phát triển core sau 11; không báo stage hoặc wave hoàn tất trước khi dependency Stage 12 và V2 acceptance đều pass. |
| **W4 — V3 MCP và profiles** | 14–15 | Thêm MCP có policy và profiles/automation opt-in. Gate V3 gồm security/policy acceptance của 14, profile/automation acceptance của 15 và hồi quy V1/V2. | Sau 12+13: `(14 ∥ 15-profile-core) → 15/MCP-integration-and-V3-gate`. Stage 15 không bật MCP profile tools hoặc đóng V3 trước khi Stage 14 hoàn tất. |

Stage IDs và từng file stage vẫn là nguồn acceptance chi tiết. Wave là đơn vị lập kế hoạch/điều phối, không nới scope, không thay dependency ghi trong stage, không gộp acceptance thành một kiểm tra mơ hồ. Stage 03–15 không được đổi `PLANNED` sang `DONE` trước khi implementation và evidence thực sự tồn tại.
Ký hiệu `∥` biểu thị hai node không phụ thuộc theo DAG, không bảo đảm được chạy cùng lúc. Chỉ chạy đồng thời khi Orca báo worker/readiness và runtime capacity thực tế đủ; hiện chỉ OpenCode đã được xác nhận dispatch, nên nếu không có slot đã kiểm chứng thì chạy các node độc lập tuần tự, không tính Kilo/Cline/Freebuff vào capacity.

## Worker capability và model gate

| Worker | Bằng chứng hiện có | Cách dùng trong kế hoạch |
|---|---|---|
| **OpenCode** | Đã launch và hoàn tất một Orca worker review. Receipt không pin model (`model: null`); OpenCode worker không chứng minh khả năng override model. | Worker Orca duy nhất đã được kiểm chứng trong repo. Trước task gửi source, xác nhận effective provider/model và data policy; không giả định model theo yêu cầu đã áp dụng. |
| **Kilo Code** | CLI và `/model` picker chạy trực tiếp; Qwen3.8 27B Free hiển thị. Chưa có Orca `worker-start` proof. | Chỉ dùng interactive/manual; không đưa vào DAG Orca cho tới khi có launch/readiness/worker-completion evidence. |
| **Cline** | CLI và `/model` picker chạy trong Plan mode, auto-approve tắt; DeepSeek V4.1 Flash Free hiển thị. Chưa có Orca dispatch proof. | Chỉ dùng interactive/manual; không xem TUI launch là Orca worker readiness. |
| **Freebuff** | CLI và `/model` picker chạy; Space Bunny Alpha hiển thị. Chưa có Orca dispatch proof. | Không đưa vào source-bearing task mặc định: provider ẩn danh có thể giữ prompt. Chỉ dùng cho source sau khi user chấp nhận chính sách dữ liệu; chưa có Orca dispatch proof. |

Các model mong muốn cho OpenCode cần giải quyết trước khi pin vào worker policy: catalog OpenCode trực tiếp hiện có `fledge-alpha-free`, `space-bunny-free`, `muse-spark-1.3-contributor-free`; không thấy `space-bunny-alpha` hoặc Muse Spark 1.4. Không tự thay model yêu cầu bằng model gần tên. Không ghi credentials/tokens vào plan hoặc task prompt.

Ma trận trên là evidence theo lần kiểm tra hiện tại, không phải cam kết hỗ trợ vĩnh viễn. Muốn thêm một worker vào Orca DAG phải chứng minh lần lượt: agent ID được Orca hỗ trợ → launch/readiness pass → worker hoàn tất task bounded → output/diff đúng worktree → model/provider thực tế được xác định. Nếu chưa đủ các bước này, worker là `unverified` và không được tính vào capacity.

## Quy tắc tạo và điều phối task

Mỗi Orca task phải có đủ:

1. **Outcome quan sát được** và acceptance lấy từ stage; ghi rõ non-goals.
2. **Prerequisite**: task/stage nào phải `DONE` trước; không dispatch node đang blocked.
3. **Một owner** cùng allowlist file/package được sửa; không giao hai writer lên cùng shared contract/file.
4. **Contract** đã freeze hoặc quyết định cần coordinator chốt trước khi worker bắt đầu.
5. **Verification** cụ thể: behavior test, integration smoke hoặc artifact/evidence; reviewer không thay implementation acceptance.
6. **Handoff**: file/diff, lệnh đã chạy, kết quả quan sát, known limits, unresolved decision và trạng thái worker/task.

Coordinator giữ PRODUCT, shared DTO/API, dependency changes, task DAG, stage status và integration. Worker không tự mở rộng scope, không tự đổi contract chung, không tự đánh dấu stage `DONE`. Song song chỉ dùng cho các node độc lập sau khi interface/file ownership đã freeze. Mỗi failure giữ evidence; dependent tasks tiếp tục `blocked` cho tới khi coordinator xử lý blocker. Trước retry/replacement phải kiểm tra worker/task/terminal state để tránh chạy trùng side effect. Dùng Orca thật theo version-matched guide tại [skill orchestration](../../../.agents/skills/orchestration/SKILL.md); không mô phỏng worker handoff bằng CLI TUI hoặc subagent ngoài Orca.

## Wave 1 task sequence — bước kế tiếp

Stage 03 là task đầu tiên; không dispatch 04/05 trước khi config/store contract đóng băng.

- **W1.0 — Contract freeze (coordinator):** quyết định XDG config/state/runtime paths; config schema và stable IDs; JSON `schema_version`/`revision`; compatibility/migration policy cho `snake_case` từ Stage 02; logical-session transaction boundary; lock ownership; corrupt/disk-full behavior. Ghi các quyết định vào ADR hoặc stage documentation trước khi chia writer.
- **W1.1 — Config path/schema:** `internal/config/`, config example và configuration docs; strict YAML validation, path resolution, argv literal, permissions trên isolated temp home. Owner riêng.
- **W1.2 — Durable store:** `internal/store/` và state-home locking/atomic persistence; migrations, backup, corruption/fault/concurrent-writer tests. Owner riêng sau W1.0; coordinator xử lý giao điểm với DTO/path API.
- **W1.3 — Stage 03 integration gate:** config absent/invalid, future schema, migration roundtrip, write-failure giữ state cũ hoặc mới nguyên vẹn, lock contention, permissions, không ghi state thật. Sau khi pass, chốt config DTO/path/lock/failure APIs làm input cho 04–06.
- **W1.4 — Stage 04 ∥ Stage 05:** hai task độc lập theo ownership đã ghi ở stage docs; chỉ mở sau W1.3. Không cùng sửa `go.mod`; dependency request do coordinator duyệt. Stage 05 còn phải tuân ADR/spike 00.
- **W1.5 — Stage 06 integration:** sau 04+05; freeze IPC protocol trước khi tách IPC/CLI bounded tasks. Coordinator giữ bootstrap/assembly và chạy multi-terminal owner/client E2E.
- **W1.6 — W1 close:** chạy stage checks và applicable full suite, lưu `reports/stage-03.md` đến `stage-06.md` có status/evidence/handoff; chỉ khi M1 gate pass mới mở W2.

Kiểm tra skill phù hợp trong từng stage trước khi giao việc. Skill là hướng dẫn quy trình, không thay contract, acceptance hoặc evidence của dự án.
