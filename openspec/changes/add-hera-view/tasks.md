## 1. Stage A — Migration + DAOs

- [ ] 1.1 Add migration `0003_archived_at.sql` to `internal/db/schema.go` adding nullable `archived_at TEXT` columns to `orchestrators` and `roles`, with an index on each.
- [ ] 1.2 Add `ArchiveOrchestrator(id)`, `UnarchiveOrchestrator(id)`, `RenameOrchestrator(id, newName)` to `internal/db/orchestrators.go`.
- [ ] 1.3 Add `ArchiveRole(id)`, `UnarchiveRole(id)`, `RenameRole(id, newName)` to `internal/db/roles.go`.
- [ ] 1.4 Update default `List*` paths on orchestrators and roles to filter `archived_at IS NULL` by default; expose `IncludeArchived bool` option (or `*Inclusive` method).
- [ ] 1.5 Tests: `internal/db/orchestrators_test.go`, `internal/db/roles_test.go` exercise archive / unarchive / rename plus uniqueness across archived/non-archived rows.
- [ ] 1.6 Tests: confirm `Get(id)` resolves archived rows; confirm default `List*` filters them out.
- [ ] 1.7 Run `go test ./internal/db/... -race -count=1` until green.

## 2. Stage B — PTY proxy package

- [ ] 2.1 Create `internal/view/proxy/proxy.go` exposing `NewSubscription`, `Subscribe`, `Close`. One subscription per argus task; fan-out listeners over the same upstream snapshot+SSE.
- [ ] 2.2 Create `internal/view/proxy/ring.go` — circular byte buffer with ~256 KiB cap; oldest bytes dropped when full.
- [ ] 2.3 Wire the snapshot fetch (`GET /api/tasks/{id}/output`) + SSE consumer (`GET /api/tasks/{id}/stream?since=N`) via the existing `internal/argus/client.go`. Snapshot returns `X-Output-Total`; pass to the SSE consumer as `since`.
- [ ] 2.4 Tests: `internal/view/proxy/proxy_test.go` — fake argus HTTP/SSE server, assert snapshot-then-stream sequencing, ring boundedness, multi-listener fan-out.
- [ ] 2.5 Run `go test ./internal/view/proxy/... -race -count=1` until green.

## 3. Stage C — Plugin view registration in argus client

- [ ] 3.1 Add `internal/argus/views.go` with `RegisterView(title, hotkey, callbackURL)`, `HeartbeatView(id)`, `DeleteView(id)` matching the existing MCP-tool registrar pattern (see `internal/mcp/registrar.go`).
- [ ] 3.2 Use the existing scope token for auth; treat 409 on re-register as "already registered, fall back to heartbeat" the same way the MCP registrar does.
- [ ] 3.3 Tests: `internal/argus/views_test.go` against a fake argus HTTP server; assert request shape, error handling, idempotency on re-register.
- [ ] 3.4 Run `go test ./internal/argus/... -race -count=1` until green.

## 4. Stage D — Custom `tcell.Screen` backed by WebSocket

- [ ] 4.1 Create `internal/view/screen/wsscreen.go` — a `tcell.Screen` implementation whose `Show()`/`Sync()` emit a full-surface ANSI byte buffer as a WebSocket binary frame; whose event queue receives `tcell.EventKey` events translated from inbound binary frames.
- [ ] 4.2 Translate text-frame control envelopes (`resize`/`focus`/`blur`) into `tcell.EventResize` and focus state changes.
- [ ] 4.3 Tests: `internal/view/screen/wsscreen_test.go` — drive Show/Sync against a fake WebSocket connection, assert ANSI bytes well-formed; inject inbound binary frames, assert tcell events delivered.
- [ ] 4.4 Run `go test ./internal/view/screen/... -race -count=1` until green.
- [ ] 4.5 If implementing the WS-backed Screen proves infeasible in this stage, stop and `hera_send` to coord with the blocker rather than push through — do NOT improvise. Stage J / K remain blocked.

## 5. Stage E — WebSocket server route

- [ ] 5.1 Add `internal/view/server.go` mounting `GET /view` on the existing `:7744` HTTP listener; accept WebSocket upgrade via `github.com/coder/websocket`.
- [ ] 5.2 Per connection: construct a Stage-D `wsscreen`, then a `tview.Application` bound to it, then run the app's event loop on a goroutine; on connection close, stop the app and tear down the goroutine.
- [ ] 5.3 Last-writer-wins: maintain a single-active-session reference; on new upgrade, close the prior session before accepting the new one.
- [ ] 5.4 Tests: `internal/view/server_test.go` — start the route on an `httptest.Server`, open two consecutive WebSocket clients, assert the first connection is closed when the second connects.
- [ ] 5.5 Run `go test ./internal/view/... -race -count=1` until green.

## 6. Stage F — tview app + 3-column layout

- [ ] 6.1 Add `internal/view/streampane.go` — a tview Box widget that renders an ANSI byte stream from a channel (port / fork of argus's `streampane.StreamPane`; see `~/Development/Personal/argus/internal/tui/streampane/`).
- [ ] 6.2 Add `internal/view/layout.go` — tview Flex composing: top bar (literal `HERA` left-aligned, 1 row) + body (rail width ~22, coord pane and agent pane equal-split, remaining rows) + bottom bar (1 row, focus-state-aware hints).
- [ ] 6.3 Add `internal/view/app.go` exposing `BuildApp(db, proxy)` returning a `*tview.Application`. Wire rail data from the orchestrators/roles/bindings DAOs (active-only on first render); wire StreamPane data from Stage B proxy subscriptions.
- [ ] 6.4 Tests: `internal/view/app_test.go` — build the app, drive a fake screen, assert layout cells render the expected content (rail header, top-bar text, pane placeholders when no bindings exist).
- [ ] 6.5 Defer focus, key routing, and rail-state-aware bottom bar to stages G and H respectively; first cut wires layout only.
- [ ] 6.6 Run `go test ./internal/view/... -race -count=1` until green.

## 7. Stage G — Focus + key routing

- [ ] 7.1 Add `internal/view/focus.go` — three-state machine (`RAIL`, `COORD`, `AGENT`) with `Advance()` / `Retreat()` / `ToRAIL()` / `JumpToAGENT()` transitions.
- [ ] 7.2 Add `internal/view/keys.go` — top-level key handler attached to the tview Application; routes Cmd/Ctrl-←/→, Enter, Ctrl-Q to focus transitions and forwards all other keystrokes in `COORD` / `AGENT` focus to the bound task's input endpoint via `internal/argus/client.go`.
- [ ] 7.3 Colored border on the focused element via `SetBorderColor` driven by the focus state.
- [ ] 7.4 Mutation keys `n`/`r`/`^d`/`a`/`l`/`?` are intercepted only when focus is `RAIL`; in `COORD` / `AGENT` they fall through to keystroke forwarding (as ordinary characters).
- [ ] 7.5 Tests: `internal/view/keys_test.go` — drive the focus state machine; assert mutation keys are no-ops in pane focus; assert focus-traversal keys are not forwarded as bytes.
- [ ] 7.6 Run `go test ./internal/view/... -race -count=1` until green.

## 8. Stage H — Rail operations

- [ ] 8.1 Extend `internal/argus/client.go` with `CreateTask(project, prompt, meta)` (HTTP POST to `/api/tasks`). If the shape doesn't match what design.md D5 assumes, stub it and `hera_send` to coord with the substrate question; do NOT improvise the wire format.
- [ ] 8.2 Add `internal/view/ops/new.go` — modal flow for `n`: prompt for name + mission, validate uniqueness against non-archived orchestrators, spawn argus task via `CreateTask` whose prompt invokes `hera_new_orchestrator`.
- [ ] 8.3 Add `internal/view/ops/rename.go` — modal flow for `r`: prompt for new name, validate uniqueness scope (global for orchestrators, per-orchestrator for roles), call DAO `RenameOrchestrator` / `RenameRole`.
- [ ] 8.4 Add `internal/view/ops/delete.go` — modal flow for `^d`: confirmation modal listing what will be removed; on confirm: end binding(s), set `archived_at`, `git worktree remove --force` via `os/exec` (daemon is unsandboxed under launchd). Log every `git worktree remove` invocation with the path. If the worktree path is empty or the directory does not exist, log + skip (soft no-op).
- [ ] 8.5 Add `internal/view/ops/archive.go` — `a` toggle: set/clear `archived_at` via DAO and call argus's `POST /api/tasks/{id}/archive` for non-archived → archived transitions on the bound argus_task_id.
- [ ] 8.6 Add `internal/view/ops/listall.go` — pure view-state toggle for `l`; no DB writes.
- [ ] 8.7 Add `internal/view/ops/help.go` — `?` modal listing all bindings by focus state; dismiss via `q`.
- [ ] 8.8 Add `internal/view/ops/resurrect.go` — Enter on archived coord row when Archive section visible: confirm, clear `archived_at` on orchestrator + coord role, spawn argus task via `CreateTask` in the role's stored `argus_project` with a prompt invoking `hera_join(cwd=$PWD)`.
- [ ] 8.9 Tests per file: `ops/new_test.go`, `ops/rename_test.go`, `ops/delete_test.go`, `ops/archive_test.go`, `ops/resurrect_test.go`. For `delete_test.go` use a temp git worktree fixture to exercise the `git worktree remove` step.
- [ ] 8.10 Run `go test ./internal/view/ops/... -race -count=1` until green.

## 9. Stage I — Dynamic rail updates

- [x] 9.1 Add `internal/db/events.go` exposing a `Broadcaster` type with `Emit(event)` and `Subscribe() chan Event`. Event types cover orchestrator/role/binding insert/update/delete.
- [x] 9.2 Integrate `Broadcaster.Emit` into the relevant DAO methods (insert / update / delete on orchestrators, roles, bindings, including the new Stage A methods).
- [x] 9.3 Add `internal/view/rail.go` subscribing to the broadcaster from `BuildApp`; debounce refreshes (~100 ms) and re-render the rail in tview's `QueueUpdateDraw`.
- [x] 9.4 Tests: `internal/db/events_test.go` (broadcast fan-out, no blocking on slow subscribers); `internal/view/rail_test.go` (insert orchestrator → rail refreshes within ~150 ms; idle 1s → no DB reads).
- [x] 9.5 Run `go test ./internal/... -race -count=1` until green.

## 10. Stage J — Daemon wire-up

- [ ] 10.1 In `internal/daemon/run.go`: at startup, after the existing MCP registrar starts, also start the plugin-view registrar (Stage C) and mount the WebSocket route (Stage E) on the existing `:7744` listener.
- [ ] 10.2 At startup, walk live bindings and seed the Stage-B PTY proxy with snapshot + SSE subscriptions for each.
- [ ] 10.3 At shutdown, unregister the plugin view (DELETE) and tear down proxy subscriptions cleanly.
- [ ] 10.4 Tests: `internal/daemon/run_test.go` asserts the plugin view registers on startup and unregisters on shutdown via a fake argus HTTP server.
- [ ] 10.5 Run `go test ./internal/daemon/... -race -count=1` until green.

## 11. Stage K — Smoke test

- [ ] 11.1 Add `internal/daemon/view_smoke_test.go` — spawn the daemon in-process pointed at a fake argus HTTP+SSE server; open a real WebSocket against `/view`; send a `resize` envelope; assert outbound binary frames are well-formed ANSI; send an inbound key envelope; assert the daemon routes it to the right task's input endpoint on the fake argus server.
- [ ] 11.2 Confirm the smoke test also exercises the last-writer-wins close on a second connection.
- [ ] 11.3 Run `go test ./internal/daemon/... -race -count=1` until green.

## 12. Validation + archive

- [ ] 12.1 Run `openspec validate add-hera-view --strict` against the change folder. Fix any issues.
- [ ] 12.2 Run `openspec validate --all --strict` as a sanity check.
- [ ] 12.3 Manual smoke (Aaron, morning): rebuild the daemon and open hera's plugin view in argus; verify the three panes render, rail navigation works, Cmd/Ctrl-←/→ traverses focus, `?` opens help.
- [ ] 12.4 After Aaron live-verifies, run `openspec archive add-hera-view`.
