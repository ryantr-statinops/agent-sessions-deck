# Stage 03 — Config và persistent state

Trạng thái: DONE · Milestone: M1 · Phụ thuộc: 02 · Cỡ việc: M.

## Mục tiêu

Config nhỏ, dễ hiểu; metadata bền vững qua crash, không lost-update và không lưu secrets ngẫu nhiên.

## Phạm vi và đầu ra

`internal/config/`, `internal/store/`, state-home lock/path helpers, `config.example.yaml`, `docs/configuration.md`. V1 lưu JSON versioned; chưa có event database.

## Hợp đồng Stage 03 đã chốt

ADR 0003 được chốt cho Stage 03. `sessions.json` là snapshot có version, revision và toàn bộ session/attempt theo `EncodeSessions` của Stage 02; revision này chính là revision của `Store.Load/Commit`.

`state.json` lưu riêng danh sách `recent_workspaces` với schema version và revision độc lập. Config giữ workspace khai báo tường minh; cập nhật recents không sửa file config. Không có thao tác Stage 03 nào transaction qua hai file.

Mỗi file được ghi qua temp file cùng thư mục, flush/fsync, atomic rename và directory sync khi phù hợp. Không được giả lập transaction chéo hai file bằng các lần ghi tuần tự.

Chỉ import legacy `~/.config/asd/sessions.json` khi file XDG đích chưa tồn tại: strict-decode mảng Stage 02, giữ backup, ghi envelope schema v1. File XDG hợp lệ là nguồn authoritative; dữ liệu corrupt hoặc future-version phải được giữ nguyên và fail an toàn.

## Checklist thực thi
- [x] Resolve XDG config/state/runtime theo ADR; test override trong isolated temp home. Không dùng một biến test để ghi đè HOME của shell làm việc.
- [x] Schema config khớp Product và Stage 03: refresh, extra_paths, stable agent ID/name/executable/argv, workspace khai báo tường minh và terminal limits.
- [x] Strict YAML: unknown/duplicate keys, invalid hoặc non-positive duration, duplicate agent IDs, empty executable và trailing document fail với lỗi file/key có hướng sửa.
- [x] Command là executable + argv array; không shell-split/eval. Expand ~ theo quy tắc đã chốt; display không làm lộ argv secret.
- [x] Directory/state/config mới đúng mode; first write tạo state home private; không chmod destructively file/dir người dùng đã có.
- [x] sessions.json/state.json có envelope schema_version/revision; reject corrupt, null/missing payload và trailing JSON.
- [x] Atomic per-file write dùng temp cùng directory, flush/fsync, rename và directory sync khi phù hợp; fault không truncate image cũ.
- [x] Owner giữ state-home lock cả lifetime nhưng store owner vẫn load/commit; offline read/mutate dùng lock ngắn và bị từ chối khi owner live.
- [x] Session transaction ghi cả snapshot/revision atomically; recent workspace revision của state.json độc lập.
- [x] Corrupt/truncated/future state được giữ nguyên, fail an toàn, recovery guidance rõ.
- [x] Legacy import chỉ khi XDG path thật sự absent; strict decode; backup riêng tư không ghi đè; giữ source và destination authoritative.
- [x] Không persist env, token, terminal raw output; argv có thể chứa secret nên không log command line mặc định và redact display.

## Kế hoạch triển khai Stage 03

### Worker, branch và phạm vi

- Dùng một OpenCode worker qua Orca `--agent opencode`, `--worktree current`; không truyền `--model`, dùng model default theo yêu cầu. Ping gần nhất tự báo model `opencode/fledge-alpha-free`; receipt không pin model, nên worker phải ghi model thực tế trong handoff, không xem đó là model override.
- Làm trên nhánh `dev`, bắt đầu từ working tree sạch. Coordinator giữ contract chung, `go.mod`, review/integration và Stage 03 status. Không giao code Stage 04/05 trước khi Stage 03 qua gate.
- Chỉ sửa các scope Stage 03: `internal/config/`, `internal/store/`, state-home path/lock helpers, config example và `docs/configuration.md`; không sửa state thật trong `$HOME`.

### Slices, acceptance và commits

1. **XDG config/path + schema defaults:** thêm resolver theo ADR 0003, config absent defaults, strict YAML validation, stable agent IDs, explicit workspace, argv literal; tests cho missing/unknown/duplicate/invalid values. Commit nhỏ gồm behavior và tests liên quan.
2. **Config security/command contract:** permissions `0700`/`0600`, `~` path expansion, executable + argv arrays, redacted display; chứng minh không dùng shell/eval và không persist secrets. Commit riêng sau targeted tests.
3. **Versioned stores:** implement `sessions.json` full-snapshot envelope và `state.json` recent-workspace envelope, strict decode, per-file revisions và Store expected-revision conflict. Commit riêng với serialization/round-trip/conflict tests.
4. **Atomic persistence/lock:** per-file temp-write, flush/fsync, atomic rename, directory sync, owner lifetime lock và offline mutation rules; fault/permission/concurrent-writer tests trong isolated temp home. Commit riêng sau targeted tests.
5. **Legacy migration:** chỉ import legacy sessions khi XDG target absent; backup, preserve source, không merge lên destination; round-trip/backup/corrupt/future-version tests. Commit riêng.
6. **Docs/report:** config example và mỗi Markdown file được commit riêng; Stage 03 report là commit cuối sau khi acceptance pass.

Mỗi code commit phải nhỏ nhất nhưng hoàn chỉnh về behavior và test. Chạy test liên quan trước commit; chỉ push commit đã pass lên `origin/dev` ngay sau khi tạo, rồi mới bắt đầu slice kế tiếp. Docs: một file mỗi commit theo quy ước maintainer. Không gom các slice vào một stage-end commit.

### Gate Stage 03

Giữ status `PLANNED` cho tới khi các acceptance ở trên có evidence, `make check` pass, config/store tests đã chạy trong temporary home, và report/handoff được ghi. Sau đó handoff cho Stage 04/06 là config DTO, state path/revision, lock/Store APIs, legacy migration rule và failure modes.
## Acceptance và verification

- Config absent dùng defaults; config invalid fail rõ mà không chạy agent.
- Atomic write fault tests giữ được state trước hoặc sau, không half JSON; concurrent writers bị lock từ chối.
- Migration roundtrip giữ ID/name/exit metadata; unknown future schema fail an toàn.
- File permission tests và isolated home xác nhận không ghi vào state thật.
- CLI client không có quyền ghi store độc lập khi owner đang giữ lock.
- Revision của commit session chỉ tăng trong `sessions.json`; cập nhật recents chỉ tăng revision `state.json`. Fault tests chứng minh lỗi ở mỗi file giữ nguyên image trước đó và không để half JSON.

## Skills tham khảo

Skill là hướng dẫn thao tác; PRODUCT, quyết định đã duyệt và acceptance của stage vẫn là nguồn chuẩn. Chỉ đọc skill cần cho task và kiểm tra trạng thái trong `../../../.agents/SKILLS/data/promoted.json`.

- [`common/foundation/requirements-analysis`](../../../.agents/SKILLS/common/foundation/requirements-analysis/SKILL.md)
- [`common/engineering/testing`](../../../.agents/SKILLS/common/engineering/testing/SKILL.md)
- [`common/engineering/code-review`](../../../.agents/SKILLS/common/engineering/code-review/SKILL.md)
- [`common/engineering/dependency-management`](../../../.agents/SKILLS/common/engineering/dependency-management/SKILL.md)

Điều phối worker Orca chỉ khi cần phối hợp Orca thật: [skill orchestration](../../../.agents/skills/orchestration/SKILL.md). Đây là discovery stub; trước lệnh Orca tải guide đúng phiên bản theo file.

## Phân công và handoff

Store/config owner thực thi theo ADR 0003; coordinator review revision/transaction semantics. Handoff cho 04–06 gồm config DTO, path policy, lock API, recent-workspace state API và failure modes.

Prompt thực thi: “Thực thi Stage 03 theo schema đã chốt, xây strict config và single-writer JSON store có atomic persistence/migration. Chạy fault tests trên temporary state home, không sửa state thật.”
