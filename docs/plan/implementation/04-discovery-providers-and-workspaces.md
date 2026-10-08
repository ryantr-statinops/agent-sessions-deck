# Stage 04 — Discovery, providers và workspaces

Trạng thái: `DONE` (acceptance passed on isolated worktree; W1 integration into `dev` pending) · Milestone: M1 · Phụ thuộc: 02, 03 · Cỡ việc: M.

## Mục tiêu

Tìm agent cài trên máy và resolve workspace/Git context nhanh, chính xác, có generic fallback.

## Phạm vi và đầu ra

`internal/discovery/`, `internal/providers/{generic,claude,codex,opencode,omp,orca,aider}/` khi có adapter thật, `internal/workspace/`, `internal/git/`; `docs/providers.md` và compatibility matrix. Release V1 cần generic + ít nhất ba concrete providers được kiểm chứng (đề xuất Claude/Codex/OpenCode).

## Checklist thực thi

- [x] Discovery dùng PATH và extra_paths có precedence, canonical path và executable permission checks; dedupe symlink/binary, không scan toàn filesystem.
- [x] Configured provider overrides theo stable ID rõ ràng; collision name/ID báo lỗi thay vì launch binary bất ngờ.
- [x] Version/identity probes có context timeout, output cap, concurrency limit và cache; không chạy command interactive chỉ để detect version.
- [x] Built-in launch mặc định chỉ binary/argv tối thiểu đã kiểm tra; không đoán flags session/resume của vendor.
- [x] Generic provider nhận executable và argv chính xác, workspace thành cwd; mọi shell expansion là lựa chọn explicit của người cấu hình.
- [x] OMP/Orca xác minh coding-agent identity, không coi mọi `orca` executable là coding tool; trên Linux có thể trùng screen reader. Không chạy binary chưa rõ identity để dò bằng hành vi có side effect.
- [x] Detection trả available/not-found/invalid/uncertain + reason/version provenance; không ép “available” từ tên binary.
- [x] Nguồn workspace: current cwd, explicit CLI path, config và recent paths. Validate existing directory, readability/cwd access, expand tilde và canonical symlink policy.
- [x] Workspace identity theo canonical directory; Git metadata không thay thế cwd bằng repo root nếu user chọn subdirectory.
- [x] Git CLI có timeout; repo root/branch/detached HEAD/worktree/bare/no-Git/permission handled; `status --porcelain=v1 -z` hoặc v2 -z theo parser contract.
- [x] Đếm changed files theo record Git, không đếm dòng: rename/untracked/unmerged/filename newline cần fixtures.
- [x] Cache Git theo TTL và refresh có cancel; slow repository không block launcher.

## Acceptance và verification

- PATH fixtures: missing binary, symlink, path chứa khoảng trắng, extra_paths precedence, timeout và duplicate configured agent.
- `BuildCommand` giữ argv literal với ký tự shell; test chứng minh không chạy shell injection.
- Git fixtures gồm normal repo, nested cwd, linked worktree, detached HEAD, rename và non-repo.
- Compatibility matrix nêu version, executable identity, launch args, terminal coverage, native capabilities và thời điểm kiểm tra; provider chưa xác minh không nằm trong supported list.

## Skills tham khảo

Skill là hướng dẫn thao tác; PRODUCT, quyết định đã duyệt và acceptance của stage vẫn là nguồn chuẩn. Chỉ đọc skill cần cho task và kiểm tra trạng thái trong `../../../.agents/SKILLS/data/promoted.json`.

- [`common/foundation/repository-onboarding`](../../../.agents/SKILLS/common/foundation/repository-onboarding/SKILL.md)
- [`common/engineering/testing`](../../../.agents/SKILLS/common/engineering/testing/SKILL.md)
- [`common/engineering/debugging`](../../../.agents/SKILLS/common/engineering/debugging/SKILL.md)
- [`common/engineering/code-review`](../../../.agents/SKILLS/common/engineering/code-review/SKILL.md)

Điều phối worker Orca chỉ khi cần phối hợp Orca thật: [skill orchestration](../../../.agents/skills/orchestration/SKILL.md). Đây là discovery stub; trước lệnh Orca tải guide đúng phiên bản theo file.

## Phân công và handoff

Discovery worker sở hữu provider registry; Git/workspace có thể tách worker khi contracts ổn định. Không cùng sửa go.mod: dependency request gửi coordinator. Handoff cho 06–08 là agent/workspace selection API và launch specs; provider real-smoke còn thiếu phải vào gate Stage 10.

Prompt thực thi: “Thực thi Stage 04, ưu tiên generic rồi provider có identity rõ. Viết detection/Git fixtures, không launch binary trùng tên hoặc giả định vendor capabilities; cung cấp compatibility matrix có evidence.”
