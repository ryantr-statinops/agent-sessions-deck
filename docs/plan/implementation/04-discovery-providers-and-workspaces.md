# Stage 04 — Discovery, providers và workspaces

Trạng thái: `PLANNED` · Milestone: M1 · Phụ thuộc: 02, 03 · Cỡ việc: M.

## Mục tiêu

Tìm agent cài trên máy và resolve workspace/Git context nhanh, chính xác, có generic fallback.

## Phạm vi và đầu ra

`internal/discovery/`, `internal/providers/{generic,claude,codex,opencode,omp,orca,aider}/` khi có adapter thật, `internal/workspace/`, `internal/git/`; `docs/providers.md` và compatibility matrix. Release V1 cần generic + ít nhất ba concrete providers được kiểm chứng (đề xuất Claude/Codex/OpenCode).

## Checklist thực thi

- [ ] Discovery dùng PATH và extra_paths có precedence, canonical path và executable permission checks; dedupe symlink/binary, không scan toàn filesystem.
- [ ] Configured provider overrides theo stable ID rõ ràng; collision name/ID báo lỗi thay vì launch binary bất ngờ.
- [ ] Version/identity probes có context timeout, output cap, concurrency limit và cache; không chạy command interactive chỉ để detect version.
- [ ] Built-in launch mặc định chỉ binary/argv tối thiểu đã kiểm tra; không đoán flags session/resume của vendor.
- [ ] Generic provider nhận executable và argv chính xác, workspace thành cwd; mọi shell expansion là lựa chọn explicit của người cấu hình.
- [ ] OMP/Orca xác minh coding-agent identity, không coi mọi `orca` executable là coding tool; trên Linux có thể trùng screen reader. Không chạy binary chưa rõ identity để dò bằng hành vi có side effect.
- [ ] Detection trả available/not-found/invalid/uncertain + reason/version provenance; không ép “available” từ tên binary.
- [ ] Nguồn workspace: current cwd, explicit CLI path, config và recent paths. Validate existing directory, readability/cwd access, expand tilde và canonical symlink policy.
- [ ] Workspace identity theo canonical directory; Git metadata không thay thế cwd bằng repo root nếu user chọn subdirectory.
- [ ] Git CLI có timeout; repo root/branch/detached HEAD/worktree/bare/no-Git/permission handled; `status --porcelain=v1 -z` hoặc v2 -z theo parser contract.
- [ ] Đếm changed files theo record Git, không đếm dòng: rename/untracked/unmerged/filename newline cần fixtures.
- [ ] Cache Git theo TTL và refresh có cancel; slow repository không block launcher.

## Acceptance và verification

- PATH fixtures: missing binary, symlink, path chứa khoảng trắng, extra_paths precedence, timeout và duplicate configured agent.
- `BuildCommand` giữ argv literal với ký tự shell; test chứng minh không chạy shell injection.
- Git fixtures gồm normal repo, nested cwd, linked worktree, detached HEAD, rename và non-repo.
- Compatibility matrix nêu version, executable identity, launch args, terminal coverage, native capabilities và thời điểm kiểm tra; provider chưa xác minh không nằm trong supported list.

## Phân công và handoff

Discovery worker sở hữu provider registry; Git/workspace có thể tách worker khi contracts ổn định. Không cùng sửa go.mod: dependency request gửi coordinator. Handoff cho 06–08 là agent/workspace selection API và launch specs; provider real-smoke còn thiếu phải vào gate Stage 10.

Prompt thực thi: “Thực thi Stage 04, ưu tiên generic rồi provider có identity rõ. Viết detection/Git fixtures, không launch binary trùng tên hoặc giả định vendor capabilities; cung cấp compatibility matrix có evidence.”
