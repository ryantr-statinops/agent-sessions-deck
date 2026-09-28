# Stage 03 — Config và persistent state

Trạng thái: `PLANNED` · Milestone: M1 · Phụ thuộc: 02 · Cỡ việc: M.

## Mục tiêu

Config nhỏ, dễ hiểu; metadata bền vững qua crash, không lost-update và không lưu secrets ngẫu nhiên.

## Phạm vi và đầu ra

`internal/config/`, `internal/store/`, state-home lock/path helpers, `config.example.yaml`, `docs/configuration.md`. V1 lưu JSON versioned; chưa có event database.

## Checklist thực thi

- [ ] Resolve XDG config/state/runtime theo ADR; test override trong isolated temp home. Không dùng một biến test để ghi đè HOME của shell làm việc.
- [ ] Schema config: general refresh, discovery extra_paths, agents (stable ID/name/executable/args), explicit/recent workspaces, terminal limits; defaults nhỏ và tài liệu hóa.
- [ ] Parse YAML strict, validate unknown keys/duplicate ID/invalid duration/empty executable. Lỗi nêu file/key và cách sửa.
- [ ] Command là executable + argv array; không tự `sh -c`, eval, split chuỗi shell. Expand `~` cho path rõ ràng, không expand environment secret tùy tiện.
- [ ] Directory riêng mode 0700, state/config mới 0600; không chmod file người dùng hiện hữu theo cách phá quyền mà không thông báo.
- [ ] JSON envelope có schema_version/revision, session attempts và workspace references; lưu clock dạng UTC nhất quán.
- [ ] Save qua temp cùng directory, flush/fsync, atomic rename và directory sync khi phù hợp; xử lý disk-full/write-failure, không truncate bản cũ.
- [ ] Lock state home bằng cơ chế Linux rõ ràng; owner giữ lock suốt đời, CLI offline lấy lock ngắn trước read/mutate được cho phép.
- [ ] Một transaction cập nhật logical session + attempt + revision; hạn chế trạng thái chia hai file không atomic bằng aggregate hoặc journal/commit marker theo ADR.
- [ ] Detect corrupt/truncated JSON, giữ nguyên dữ liệu lỗi; báo recovery instructions, không tự reset state thành rỗng.
- [ ] Migration có version và backup; nếu có mẫu legacy `~/.config/asd/sessions.json`, import chỉ theo quy tắc đã tài liệu hóa, không xóa source.
- [ ] Không persist env, token, terminal raw output; argv có thể chứa secret nên nêu usage, tránh command line logging mặc định và hỗ trợ display redaction.

## Acceptance và verification

- Config absent dùng defaults; config invalid fail rõ mà không chạy agent.
- Atomic write fault tests giữ được state trước hoặc sau, không half JSON; concurrent writers bị lock từ chối.
- Migration roundtrip giữ ID/name/exit metadata; unknown future schema fail an toàn.
- File permission tests và isolated home xác nhận không ghi vào state thật.
- CLI client không có quyền ghi store độc lập khi owner đang giữ lock.

## Phân công và handoff

Store/config owner có thể làm độc lập sau 02; coordinator review revision/transaction semantics. Handoff cho 04–06 là config DTO, path policy, lock API và failure modes.

Prompt thực thi: “Thực thi Stage 03 theo schema đã chốt, xây strict config và single-writer JSON store có atomic persistence/migration. Chạy fault tests trên temporary state home, không sửa state thật.”
