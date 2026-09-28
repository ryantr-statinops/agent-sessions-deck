# Stage 14 — V3 MCP control plane

Trạng thái: `PLANNED` · Milestone: M4 · Phụ thuộc: 11, 12, 13 · Cỡ việc: L.

## Mục tiêu

Cho AI client truy vấn và thực hiện các thao tác ASD đã cấu hình, theo policy explicit; adapter không được trở thành arbitrary shell endpoint.

## Phạm vi và đầu ra

`internal/mcp/`, entrypoint `asd mcp serve` hoặc binary nhỏ theo ADR, policy tại `internal/app/`, SDK version pin, `docs/mcp.md`, schemas/examples và protocol integration tests.

## Checklist thực thi

- [ ] Đối chiếu specification/official Go SDK tại thời điểm triển khai; chốt protocol version, client compatibility và transport. Đề xuất stdio local, không network listener mặc định.
- [ ] MCP server là client của `agentd`, không có SessionManager/store writer thứ hai; daemon absent có actionable error, không ngầm launch agent session.
- [ ] Read tools: list_agents/list_sessions/get_session/find_session/list_workspaces/get_workspace/get_process/get_recent_events, stable IDs và bounded pagination.
- [ ] `start_session` (hợp nhất tên `start_asd` draft), stop_session/restart_session, interact_session chỉ qua app service; annotations/schema phản ánh read-only/destructive behavior.
- [ ] Default read-only; config grant mutations theo tool/agent IDs/workspace scope. Raw executable/argv/env do MCP client cung cấp bị từ chối mặc định.
- [ ] Canonical workspace allowlist, symlink/traversal handling, configured launch templates; path prefix string không đủ để chứng minh nằm trong workspace scope.
- [ ] Mutation request IDs + session attempt generation, rate/concurrency limits; retries hoặc timeout không duplicate launch/restart/input.
- [ ] Destructive tool policy có approval/grant rõ ở app layer; không dựa riêng vào prompt hoặc MCP tool annotations làm enforcement. Noninteractive server không treo đợi TUI confirm; trả approval-required nếu thiếu grant.
- [ ] Chốt interact_session: bounded screen read theo cursor/sequence; input write explicit bytes/text encoding và expected attempt. Cùng lease arbitration với human TUI, không giành input session đang có user.
- [ ] Terminal output là dữ liệu không đáng tin; responses phân biệt metadata và content, cap bytes, không tự thực thi instructions từ output.
- [ ] Audit mutation metadata (tool/session/attempt/time/result), không log input/prompt/secret mặc định; permission error typed và actionable.
- [ ] Cancellation/connection loss chỉ kết thúc request/subscription, không kill child; query timeout không mở thread xử lý vô hạn.
- [ ] Docs nêu local OS trust model, scope config, opt-in mutations, sample read-only setup và phiên bản clients đã kiểm tra.

## Acceptance và verification

- Protocol client fixture negotiate đúng SDK/spec đã pin; stdout chỉ protocol, logs trên stderr, malformed schema/oversize input có bounded error.
- Read-only client không start/stop/write; configured grant cho đúng agent/workspace hoạt động, outside/symlink escape bị từ chối.
- Duplicate mutation ID không launch hai sessions; stale attempt/input lease collision bị từ chối.
- Một AI workflow tìm session theo workspace → get metadata → read bounded screen; mutation workflow chỉ pass khi policy được bật explicit.
- Không có TCP listener hoặc arbitrary shell payload bypass; policy contract có tests độc lập với MCP transport.

## Phân công và handoff

MCP worker giữ adapter/schema; app owner giữ policy enforcement và lease contract. Worker không copy lifecycle code vào tool handlers. Handoff cho 15 là names/schemas/scoped tool policy và client examples verified.

Tham chiếu: [Official MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk), [SDK protocol lifecycle](https://go.sdk.modelcontextprotocol.io/protocol/). Đây là nguồn để verify/pin lại lúc triển khai; không hard-code API từ bản draft hiện tại.

Prompt thực thi: “Thực thi Stage 14 như adapter cho daemon service. Ưu tiên read-only MCP, thêm mutations theo scoped policy và idempotency; giữ bounded terminal I/O và shared human/AI lease.”
