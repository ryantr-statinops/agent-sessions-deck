# Stage 01 — Repository và engineering baseline

Trạng thái: `PLANNED` · Milestone: M0 · Phụ thuộc: 00 · Cỡ việc: S.

## Mục tiêu

Có nền Go build/test và fixture độc lập với vendor để các stage sau triển khai có evidence liên tục.

## Phạm vi và đầu ra

`cmd/asd/`, `go.mod`, `go.sum`, `Makefile`, `.gitignore`, `.github/workflows/ci.yml`, `README.md`, `CONTRIBUTING.md`, `tests/testagent/`, `docs/testing/development.md`. Cấu trúc package chỉ tạo khi có code thật; không sinh hàng loạt interface rỗng.

## Checklist thực thi

- [ ] Chọn module path theo repo identity đã xác minh; không dùng GitHub organization giả. Pin Go/toolchain và versions từ ADR 00.
- [ ] Dựng `cmd/asd/main.go` nhỏ, composition tại `internal/app`; compile Linux amd64 và arm64.
- [ ] Thêm targets `build`, `test`, `test-integration`, `lint`, `fmt`, `clean`; integration có timeout và cleanup.
- [ ] CI chạy formatting, `go vet`, unit/integration phù hợp và race suite trên Linux; dependency cache không chứa config/credentials.
- [ ] Viết fake agent có modes: echo/line-input, ANSI full-screen, exit code, slow-start, ignored TERM, child/grandchild, output flood, Unicode, terminal query và size report.
- [ ] Fixture dùng control pipe/handshake thay cho sleeps cố định; tạo workspace/state home tạm cho mỗi test.
- [ ] Test cleanup theo identity fixture; không dùng `pkill` theo executable name.
- [ ] Thêm version/build metadata và `asd --help`/`--version` tối thiểu; command khác triển khai ở 06.
- [ ] Viết setup/dev instructions; nêu Linux-only và nhu cầu TTY của integration.
- [ ] Ghi module/license decisions còn chờ; chỉ thêm LICENSE khi maintainer đã chọn.

## Ngoài phạm vi

Chưa cần release automation đầy đủ, SQLite, vendor API hoặc codegen framework. Không làm global installer thay đổi PATH của người dùng trong test.

## Acceptance và verification

- Clean checkout có thể build một binary `asd` và chạy help/version theo hướng dẫn.
- `make test` không cần installed coding agents, network hay tài khoản.
- Fake agent chứng minh modes bằng test: exit, signal, child và resize handshake; cleanup không còn fixture PID/PTY.
- CI có commands cụ thể có thể tái chạy local; arm64 cross-build kiểm tra compile, không quảng cáo runtime tested nếu chưa chạy.

## Skills tham khảo

Skill là hướng dẫn thao tác; PRODUCT, quyết định đã duyệt và acceptance của stage vẫn là nguồn chuẩn. Chỉ đọc skill cần cho task và kiểm tra trạng thái trong `../../../.agents/SKILLS/data/promoted.json`.

- [`common/foundation/repository-onboarding`](../../../.agents/SKILLS/common/foundation/repository-onboarding/SKILL.md)
- [`common/foundation/task-planning`](../../../.agents/SKILLS/common/foundation/task-planning/SKILL.md)
- [`common/engineering/testing`](../../../.agents/SKILLS/common/engineering/testing/SKILL.md)
- [`common/engineering/git-workflow`](../../../.agents/SKILLS/common/engineering/git-workflow/SKILL.md)

Điều phối worker Orca chỉ khi cần phối hợp Orca thật: [skill orchestration](../../../.agents/skills/orchestration/SKILL.md). Đây là discovery stub; trước lệnh Orca tải guide đúng phiên bản theo file.

## Phân công và handoff

Coordinator giữ `go.mod/go.sum` và build tooling; worker có thể giữ riêng fake agent sau khi mode protocol chốt. Handoff cho 02 là source layout, fake-agent protocol và lệnh kiểm tra chuẩn.

Prompt thực thi: “Thực thi Stage 01 sau ADR Stage 00. Dựng Go baseline và fake agent có handshake, CI và hướng dẫn dev; không triển khai session product trong stage này.”
