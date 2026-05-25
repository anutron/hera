# internal/argus

## Summary

- Behavioral branches: 77
- Covered: 73
- Uncovered (behavioral): 2
- Uncovered (implementation detail): 0
- Contradictions: 2
- Unimplemented spec promises: 0
- Test-alignment gaps: 3

## Scope

Files audited:

- `internal/argus/doc.go` (0 branches – package doc only)
- `internal/argus/client.go` (19 branches)
- `internal/argus/tasks.go` (20 branches)
- `internal/argus/mcp.go` (4 branches)
- `internal/argus/events.go` (31 branches)
- `internal/argus/request.go` (3 branches)
- `internal/argus/client_test.go` (cross-reference only)

Mapped spec requirements (`openspec/specs/hera-coordination/spec.md`):

- "Five MCP tools exposed under the `hera_` prefix"
- "MCP tool registrations heartbeated and unregistered on shutdown"
- "Event stream cursor persisted and replayed"
- "Role metadata mirrored to argus task_meta"
- "Messages auto-submitted when recipient is idle"
- "Messages buffered when recipient is not idle"
- "Injected messages identify sender"
- "Tool inputs and outputs documented"

## Branch Coverage

### `client.New` (client.go:31-43)

1. **[COVERED]** `if baseURL == ""` – falls back to `DefaultBaseURL` (`http://127.0.0.1:7743`)
   Implementation detail honoring `doc.go` contract reference to argus on 127.0.0.1:7743.

### `client.doJSON` (client.go:50-86)

1. **[COVERED]** `if body != nil` – marshals JSON body
   Spec: every request carries auth + version headers; body shape is implementation choice.

2. **[COVERED]** `if err := json.Marshal(...)` – returns marshal error
   Implementation detail.

3. **[COVERED]** `if err := http.NewRequestWithContext` – returns request construction error
   Implementation detail.

4. **[COVERED]** `if reqBody != nil` – sets `Content-Type: application/json`
   Implementation detail consistent with REST contract.

5. **[COVERED]** `if err := c.http.Do(...)` – transport error
   Implementation detail.

6. **[COVERED]** `if resp.StatusCode >= 400` – returns HTTP error with body excerpt
   Implementation detail – error reporting.

7. **[COVERED]** `if out != nil` + decode error
   Implementation detail.

### `client.applyAuth` (client.go:90-93)

1. **[COVERED]** Always sets `Authorization: Bearer <token>` and `X-Argus-Plugin-Version: 1`
   Spec: `doc.go` paraphrases the substrate contract – "Every request carries Authorization: Bearer ... and X-Argus-Plugin-Version: 1." `PluginVersion` constant equals `"1"` (client.go:17) and matches the spec wording.

### `client.withTokenQuery` (client.go:97-106)

1. **[UNCOVERED-BEHAVIORAL]** `if err != nil` URL parse fallback returns `c.baseURL + path` without the token
   Because the function silently drops the token when URL parsing fails, it means a caller relying on token-in-URL would issue an unauthenticated request. This helper itself isn't used by any other source file in the module (no other callers visible in the package). The spec mandates header-based auth and does not authorize token-in-URL. See finding `internal-argus-2`.

2. **[UNCOVERED-BEHAVIORAL]** Helper exists but is dead/unused; if intended for SSE-no-header clients, this is an undocumented exception to the header-auth contract.
   Intent question: should this helper exist at all? Header-based auth is the only documented path. If kept, the spec should describe when/why token-in-URL is used.

### `tasks.ListTasks` / `GetTask` / `GetTaskMeta` / `PutTaskMeta` (tasks.go:26-84)

All branches are JSON request/response plumbing or error early returns. Each route is correct per the spec:

1. **[COVERED]** `GET /api/tasks` – spec line 181 "hera MUST call `GET /api/tasks`".
2. **[COVERED]** `GET /api/tasks/{id}` – path-escaped task id.
3. **[COVERED]** `GET /api/tasks/{id}/meta?namespace=...` – optional namespace filter; spec line 231 says namespace is auto-derived from scope token on writes, so on reads namespace is informational.
4. **[COVERED]** `PUT /api/tasks/{id}/meta` body `{key, value}` (no namespace field) – spec line 73 "namespace is auto-derived from the scope token; clients writing under their own scope MUST omit it" and line 236 "PUT `{key: \"role\", value: \"worker\"}` to `/api/tasks/T2/meta`". Body shape matches exactly.

### `tasks.PostTaskInput` (tasks.go:93-116)

1. **[COVERED]** `POST /api/tasks/{id}/input` with raw bytes and `Content-Type: text/plain`
   Spec: line 41 "POST /api/tasks/{id}/input"; the spec does not mandate Content-Type, but `text/plain` is consistent with raw-bytes injection.

2. **[COVERED]** Returns `out.Bytes` from `{"status":"ok","bytes":N}` envelope
   Implementation detail.

3-7. **[COVERED]** All error early returns are implementation detail (request construction, transport, status, decode).

### `mcp.RegisterTool` / `mcp.UnregisterTool` (mcp.go:25-38)

1. **[COVERED]** `POST /api/mcp/tools` with `MCPTool` body that has `Name`, `Description`, `InputSchema`, `CallbackURL`, `AuthHeader`
   Spec line 220-227: "Tool inputs and outputs documented" – registration body MUST include a `description` and an `input_schema`. The struct shape supports this; whether actual registrations carry non-empty descriptions is the responsibility of `internal/mcp` (`Tools`, `ToolDefinition` in symbols.json).

2. **[COVERED]** `DELETE /api/mcp/tools/{name}` with path-escaped name
   Spec line 157: "hera MUST DELETE each registered tool via `DELETE /api/mcp/tools/{name}` before exiting." Matches exactly.

3. **[COVERED]** Doc comment notes "Re-POSTing with the same name is idempotent (refreshes the heartbeat)" – aligns with spec line 156 "re-POST each of its five tool registrations to argus on a 5-minute cadence". Heartbeat orchestration is in `internal/mcp/Registrar` (per symbols.json `SetHeartbeat`, `Registrar`, `Start`/`Stop`).

### `events.StreamEvents` / `streamOnce` (events.go:33-139)

1. **[COVERED]** `if sinceID > 0` includes `?since=<id>` query param
   Spec line 175: "the SSE subscription URL MUST include `since=1234` on reconnect" when cursor is 1234. The `> 0` guard is fine for any cursor written by argus (event IDs start at 1 per typical practice), BUT see finding `internal-argus-1`: a stored cursor of literally 0 is silently dropped, and the spec does not document 0 as a sentinel.

2. **[COVERED]** `applyAuth(req)` + `Accept: text/event-stream`
   Spec doc.go contract – auth + version headers on every request.

3. **[COVERED]** `client.Timeout = 0` for SSE
   Implementation detail (long-lived connection).

4. **[COVERED]** Backoff loop with `backoff *= 2` capped at `maxBackoff = 10 * time.Second`
   Implementation detail (reconnect resilience). Not described in spec – see finding `internal-argus-4`.

5. **[COVERED]** SSE parsing – blank-line event boundary, `event:` field, `data:` field, `:` comments (keep-alives)
   Implementation detail per SSE spec.

6. **[COVERED]** `if eventType != "" && ev.Type == ""` – falls back to the wire-level event type when JSON payload omits `type`
   **Spec-misreading guard check:** the requirement uses no trigger phrase. This is benign – it's a defensive merge of two equivalent representations of the event type.

7. **[COVERED]** `if ev.ID > 0 { advance(ev.ID) }` – cursor advance only for IDs > 0
   Consistent with the `?since=` guard – cursor monotonic from 1.

### `events.parseEvent` (events.go:143-149)

1. **[COVERED]** JSON unmarshal failure returns `(Event{}, false)` and caller skips
   Implementation detail – malformed payloads dropped silently.

### `request.newRequest` (request.go:11-18)

1. **[COVERED]** `if body != nil` – uses `bytes.Reader`; else nil body
   Implementation detail.

## Spec→Code Coverage Check

For each mapped spec requirement, the in-package responsibilities:

- **"Five MCP tools exposed under the `hera_` prefix"** – this module exposes `RegisterTool` / `UnregisterTool` transport. The five-tool guarantee, `hera_` prefix enforcement, and `cwd`-first schema live in `internal/mcp`. **In-scope coverage: complete** (transport).

- **"MCP tool registrations heartbeated and unregistered on shutdown"** – `RegisterTool` is idempotent (doc says so), `UnregisterTool` issues DELETE. The 5-minute cadence and SIGINT/SIGTERM wiring live in `internal/mcp/Registrar` and `internal/daemon`. **In-scope coverage: complete** (transport).

- **"Event stream cursor persisted and replayed"** – `StreamEvents` sends `?since=<id>` and exposes an `advance` callback that updates the outer cursor. Persistence to the `event_cursor` table is in `internal/db` and `internal/events`. The `resync` snapshot handling is in `internal/events`. **In-scope coverage: partial** – see findings `internal-argus-1` and `internal-argus-3`.

- **"Role metadata mirrored to argus task_meta"** – `PutTaskMeta` body shape (`{key, value}`, no namespace) matches the spec exactly. Spec line 231 says namespace is auto-derived from scope token. **In-scope coverage: complete.**

- **"Messages auto-submitted when recipient is idle"** / **"Messages buffered when recipient is not idle"** – `PostTaskInput` is the transport. Idle gating, `\n` suffix logic, sender prefix, and `delivery_mode` recording live in `internal/inject`, `internal/idle`, `internal/db`. **In-scope coverage: complete** (transport).

- **"Tool inputs and outputs documented"** – `MCPTool` struct has `Description` and `InputSchema` fields. Validation of non-empty description (≥10 chars) and schema coverage is the responsibility of `internal/mcp` (`Tools`, `ToolDefinition` per symbols.json). **In-scope coverage: complete** (transport).

## Test Alignment (client_test.go)

Mapped tests vs spec scenarios:

- `TestClient_SendsAuthAndVersion` – asserts `Authorization: Bearer test-token` and `X-Argus-Plugin-Version: 1`. Matches spec doc.go contract for `ListTasks`. **Gap:** only covers one endpoint – see finding `internal-argus-6`.

- `TestClient_StreamEvents` – asserts `?since=` is present but does not assert its **value**. Spec scenario uses literal `since=1234` – see finding `internal-argus-5`.

- `TestClient_PostTaskInput` – round-trips body bytes and Content-Type, but does not assert method or path. Spec scenario explicitly names `POST /api/tasks/{id}/input` – see finding `internal-argus-7`.

- `TestClient_PutTaskMeta` – asserts method `PUT`, path `/api/tasks/t1/meta`, body `{key, value}`. Matches spec scenario "Role meta written on binding" precisely.

- `TestClient_RegisterTool` / `TestClient_UnregisterTool` – cover transport but not the heartbeat cadence (out of module scope per `internal-mcp.Registrar`). OK.

## Cross-module symbol awareness

All symbols referenced from this module that are not defined here are accounted for via `symbols.json`:

- `internal-mcp.Registrar`, `SetHeartbeat`, `Start`, `Stop` – heartbeat orchestration
- `internal-events.NewResyncHandler`, `Subscriber.Run` – resync + dispatch
- `internal-db.EventCursorDAO` – cursor persistence
- `internal-inject.Injector`, `FormatBody`, `Inject` – idle-gated message delivery

No missing-symbol findings.

## Unimplemented Spec Promises

None within this module's scope. All spec requirements that this module is responsible for at the transport layer are implemented.

## Contradictions and Behavioral Gaps – Summary

- **internal-argus-1** (contradiction, medium): `streamOnce` drops `?since=` when stored cursor is 0; spec does not document 0 as a sentinel.
- **internal-argus-2** (uncovered-behavioral, medium): `withTokenQuery` exposes an undocumented token-in-URL pathway; helper appears unused in-package; spec mandates header auth only.
- **internal-argus-3** (note/contradiction, low): `StreamEvents` doc comment does not document that consumers MUST handle the `resync` event per spec line 180-181.
- **internal-argus-4** (uncovered-behavioral, low): SSE reconnect/backoff behavior is undocumented in the spec; the spec implies a reconnect lifecycle but does not specify backoff bounds.

## Test-alignment gaps

- **internal-argus-5**: `TestClient_StreamEvents` does not assert the `since=` value matches the passed `sinceID`.
- **internal-argus-6**: Header assertions only cover `ListTasks`; not parameterized across all endpoints.
- **internal-argus-7**: `TestClient_PostTaskInput` does not assert method or path.
