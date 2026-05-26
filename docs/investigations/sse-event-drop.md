# SSE event drop: hera misses `task.archived` (root cause in argus)

- **Status:** Diagnosed. Smoking gun is in argus, not hera.
- **TL;DR:** Argus never emits `task.archived` when the archive happens through the HTTP API or MCP `task_archive`. Those entrypoints route through the generic `db.Update`, while `events.Emit(EventTypeTaskArchived, ...)` lives only inside the partial-column `db.SetArchived`. Hera's SSE reader, dispatcher, and handler are all correct - they receive every event argus emits.

## 1. Timeline of evidence

- **2026-05-25 15:10:02** argus stops session `1779727800185385000` (the prior coord), then status flips to `in_review` (event id 822, `task.status_changed`). No archive event yet.
- **2026-05-25 15:12:06** MCP call: `[mcp] task_archive ok: id=1779727800185385000 archived=false` (unarchive).
- **2026-05-25 15:12:09** MCP call: `[mcp] task_archive ok: id=1779727800185385000 archived=true` (re-archive). This is the unarchive→re-archive cycle the orchestrator referenced.
- **2026-05-25 15:19:33** Hera daemon restart - subscriber starts with cursor 829. The user's "even after a full daemon restart" check.
- **2026-05-25 15:25:24** This worker joins; hera bindings table still shows bindings 1–6 live (rows for tasks already archived in argus).
- **Hera log at `/Users/aaron/.hera/launchd.log`** across 430 lines:
  - 10 `event subscriber starting` lines (one per daemon restart, cursor advancing 543 → 549 → 639 → 651 → 716 → 813 → 824 → 829).
  - **Zero** `binding ended on task.archived` lines.
  - **Zero** mentions of `task.archived` anywhere.
- **Argus events table** (`/Users/aaron/.argus/data.sql`):
  - Tasks archived: **15**.
  - Events with `type='task.archived'`: **0**.
  - Histogram of all event types stored: `session.idle` (579), `session.started` (68), `task.status_changed` (61), `task.created` (34), `session.exited` (33), `message.sent` (26), `task.completed` (22), `message.acked` (18). No `task.archived`, no `task.renamed`, no `task.forked`, no `link.created`, no `link.removed`, no `resync`.
- **Argus daemon log** (`/Users/aaron/.argus/daemon.log`) shows 5 `[mcp] task_archive ok` lines over the past two days - each one is an archive that argus committed but never emitted.

The cursor IS advancing (829 → 840 just from the live tail below), so the SSE stream itself is healthy. Events are flowing - they just don't include the type hera is waiting for, because argus never inserts that type into the ring.

## 2. Wire-level observation

Streaming `GET /api/events/stream?since=839` directly with curl returned (truncated to first ~10 events):

```
event: session.idle
data: {"id":840,"type":"session.idle","at":"2026-05-25T15:25:12.267893-07:00","task_id":"1779747871515412000"}

event: session.idle
data: {"id":841,"type":"session.idle","at":"2026-05-25T15:26:42.270015-07:00","task_id":"1779747886238354000"}

event: session.exited
data: {"id":845,"type":"session.exited","at":"2026-05-25T23:00:04.429981-07:00","task_id":"1779746718642643000","payload":{"err":"exit status 143","pending_restart":false,"stopped":true}}

event: session.started
data: {"id":846,"type":"session.started","at":"2026-05-25T23:00:09.381745-07:00","task_id":"1779746718642643000","payload":{"pid":53757,"resume":true}}

event: task.status_changed
data: {"id":847,"type":"task.status_changed","at":"2026-05-25T23:00:14.411982-07:00","task_id":"1779747871515412000","payload":{"from":"in_progress","to":"in_review"}}
```

- Format is exactly `event: <type>\ndata: <json>\n\n`, matching hera's reader at `internal/argus/events.go:111-147`.
- `type` is set redundantly in both the SSE `event:` field and the JSON body, so hera's `if eventType != "" && ev.Type == ""` patch-up never has to fire - `ev.Type` always comes back populated.
- `task_id` is always present and matches the bindings table.

Everything on the wire is well-formed. The drop is not at the parse layer.

## 3. Root-cause hypothesis ranked by likelihood

### (1) **Confirmed root cause** - argus's HTTP `/api/tasks/{id}/archive` handler and MCP `task_archive` tool use `db.Update` (full-row UPDATE), not `db.SetArchived` (partial-column UPDATE). Only `db.SetArchived` emits the event.

Evidence:

- The only call to `events.Emit(model.EventTypeTaskArchived, ...)` in the entire argus codebase is at `/Users/aaron/Development/Personal/argus/internal/db/tasks.go:374-376`, inside `(*db.DB).SetArchived` after the partial-column `UPDATE tasks SET archived=1, pinned=0 WHERE id=?` commits.
- The HTTP archive handler at `/Users/aaron/Development/Personal/argus/internal/api/handlers.go:497-508` does:
  ```go
  task, err := s.db.Get(id)
  task.SetArchived(archived)         // model-level toggle
  s.db.Update(task)                  // full-row UPDATE, no event emit
  ```
- The MCP `task_archive` tool at `/Users/aaron/Development/Personal/argus/internal/mcp/server.go:1537-1538` does the same thing:
  ```go
  task.SetArchived(newArchived)
  if err := s.taskDB.Update(task); err != nil { ... }
  ```
- `(*db.DB).Update` at `db/tasks.go:172-187` is a generic full-row UPDATE with no event side effect.
- The TUI archive path at `tui/app.go:374` calls `a.db.SetArchived(t.ID, t.Archived)` on the store interface. In **local mode** that interface is `*db.DB` (emits the event). In **remote mode** that interface is `apistore.Store`, whose `SetArchived` (apistore/store.go:419-425) hits the HTTP archive route - which is the broken path.
- 15 archived rows in `tasks` + 5 `[mcp] task_archive ok` log lines + **0** `task.archived` events in the ring is the exact pattern this bug predicts.

The bug surfaces unconditionally whenever archive happens through MCP or HTTP. The only path that still works is direct TUI use against a local DB store (rare in the running daemon).

### (2) `db.Update` is the canonical "write back a Task" used by other partial paths too - this same class of bug likely exists for any field whose `Set*` partial-column method emits an event.

Evidence: `model.Task.SetArchived` (model/task.go) and `model.Task.SetPinned` only toggle a struct field; the event emit lives in the DB-layer partial-column method, not the model toggle. Anything that does `task.SetX(); db.Update(task)` bypasses the same emit hook. I did not exhaustively audit other event types because the user asked for the archive case, but this is worth a sweep.

### (3) Ruled out: hera-side parse / dispatch / handler problems.

- SSE reader (`internal/argus/events.go:106-153`) handles the wire format correctly; both `event:` and `data:` are parsed and JSON `type` is honored.
- Subscriber `Run` loop (`internal/events/subscriber.go:68-85`) advances the cursor only after dispatching to every handler, and persists the cursor on each event. Cursor in `event_cursor` table is 840, matching the latest live event - consistent with "stream is healthy, no archive events were ever sent."
- `AdoptHandler.HandleEvent` (`internal/events/adopt.go:35-42`) cases `TypeTaskArchived` correctly; `handleTaskArchived` (`adopt.go:134-151`) ends the binding and logs `binding ended on task.archived`. None of these lines have ever fired - because the event never arrives.

## 4. Proposed fix sketch (no code in this worker)

Argus needs to emit `task.archived` regardless of which entrypoint flipped the column. Two reasonable fixes:

The narrow fix is to mirror `db.SetArchived`'s emit inside the HTTP `setArchive` handler and the MCP `task_archive` tool: after the successful `db.Update`, check whether `archived` transitioned `false → true` and call `events.Emit(model.EventTypeTaskArchived, id, nil)`. Same gate as the partial-column site (`err == nil && archived`). This is localized but leaves the trap open for any future caller who does `task.SetArchived(); db.Update()` somewhere else.

The structurally cleaner fix is to push emission into the DB layer so it cannot be bypassed - have `db.Update` diff the persisted-vs-incoming `archived` column and emit on transition. That eliminates the entrypoint-by-entrypoint discipline problem and matches the user's mental model ("the daemon emits the event when archive flips"). The cost is one extra `SELECT archived FROM tasks WHERE id=?` per `Update`, which is fine - `Update` is not a hot path. Same treatment should be considered for any other field that has a dedicated emit (this is the (2) hypothesis above worth auditing first).

Either way, this is an **argus** change, not a hera change. After the argus fix lands, hera's resync handler will not retroactively clean up the orphans below - argus only emits `resync` when a subscriber's cursor predates the ring. The orphans need an explicit one-time cleanup (see section 5).

## 5. Stale binding inventory + cleanup

Live bindings in `~/.hera/state.sqlite` whose argus task is already archived (the orphans):

| Binding ID | Argus task ID         | Role name              | Role kind   | Archived in argus |
|-----------:|-----------------------|------------------------|-------------|-------------------|
| 1          | 1779727800185385000   | coord                  | coordinator | yes               |
| 2          | 1779729078056022000   | w1-argus-client        | worker      | yes               |
| 3          | 1779729099623836000   | w2-runtime-setters     | worker      | yes               |
| 4          | 1779729122629777000   | w3-config-loader       | worker      | yes               |
| 5          | 1779729164432016000   | w4-registrar-handler   | worker      | yes               |
| 6          | 1779741531505419000   | smoke-decode-fix       | worker      | yes               |

Healthy live bindings (argus task is genuinely live):

| Binding ID | Argus task ID         | Role name                        | Role kind   |
|-----------:|-----------------------|----------------------------------|-------------|
| 7          | 1779746718642643000   | coord                            | coordinator |
| 8          | 1779747871515412000   | setup-rebuild-fix                | worker      |
| 9          | 1779747886238354000   | idle-gate-closeout               | worker      |
| 10         | 1779747910351595000   | sse-event-drop-investigation     | worker      |

### Safe cleanup once the fix lands

Option A - retroactive sweep at hera daemon boot. Walk every live binding, call `argus.GetTask(ctx, bnd.ArgusTaskID)`, and if the task comes back with `archived=true` (or `404`), `Bindings.End(bnd.ID, "argus_archived_backfill")`. Mirrors the resync handler's reconcile logic at `internal/events/resync.go:42-82` but treats archive as also-missing. Cheap (one `GET /api/tasks/{id}` per live binding), idempotent, and runs only on startup so steady-state cost is zero. Right answer if the fix lands and you want a clean state on next deploy.

Option B - one-shot script. From the project root: `SELECT b.id FROM bindings b JOIN roles r ON r.id=b.role_id WHERE b.ended_at IS NULL` → for each, `curl GET /api/tasks/{argus_task_id}` → if `archived=true` or `404`, `UPDATE bindings SET ended_at=CURRENT_TIMESTAMP, ended_reason='backfill_archived' WHERE id=?`. Faster to ship but less defensible long-term.

Option A is the recommendation because the same code path also covers the case where a task gets deleted out from under hera between event-stream sessions (the resync handler only catches that on a synthetic resync, which requires the cursor to be older than the retained ring - not a guarantee).

For the immediate orphans listed above, since archive-via-argus is currently impossible to detect from hera's side, either option is the right move; Option B is fine as a one-time job, but Option A pays for itself the first time another archive sneaks past.
