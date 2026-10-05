# Stage 03 — Config và persistent state

Trạng thái: `PLANNED` · Milestone: M1 · Phụ thuộc: 02 · Cỡ việc: M.

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

- [ ] Resolve XDG config/state/runtime theo ADR; test override trong isolated temp home. Không dùng một biến test để ghi đè HOME của shell làm việc.
- [ ] Schema config: refresh, discovery `extra_paths`, agents (stable ID/name/executable/args), workspace khai báo tường minh và terminal limits; defaults nhỏ, có tài liệu. Recent workspace history nằm trong `state.json`.
- [ ] Parse YAML strict, validate unknown keys/duplicate ID/invalid duration/empty executable. Lỗi nêu file/key và cách sửa.
- [ ] Command là executable + argv array; không tự `sh -c`, eval, split chuỗi shell. Expand `~` cho path rõ ràng, không expand environment secret tùy tiện.
- [ ] Directory riêng mode 0700, state/config mới 0600; không chmod file người dùng hiện hữu theo cách phá quyền mà không thông báo.
- [ ] `sessions.json` và `state.json` đều có envelope `schema_version`/`revision`; sessions/attempts/workspace references nằm trong snapshot đầy đủ của `sessions.json`, mỗi file có revision riêng.
- [ ] Save qua temp cùng directory, flush/fsync, atomic rename và directory sync khi phù hợp; xử lý disk-full/write-failure, không truncate bản cũ.
- [ ] Lock state home bằng cơ chế Linux rõ ràng; owner giữ lock suốt đời, CLI offline lấy lock ngắn trước read/mutate được cho phép.
- [ ] Một session transaction ghi atomically toàn bộ session/attempt snapshot và revision vào `sessions.json`; không có transaction spanning `sessions.json` và `state.json`.
- [ ] Detect corrupt/truncated JSON, giữ nguyên dữ liệu lỗi; báo recovery instructions, không tự reset state thành rỗng.
- [ ] Chỉ migrate legacy `~/.config/asd/sessions.json` khi file XDG đích chưa tồn tại; strict-decode, giữ backup/source và không merge đè lên destination hợp lệ.
- [ ] Không persist env, token, terminal raw output; argv có thể chứa secret nên nêu usage, tránh command line logging mặc định và hỗ trợ display redaction.

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
