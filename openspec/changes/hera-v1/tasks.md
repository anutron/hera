**Design doc:** `openspec/changes/hera-v1/design.md`

**Acceptance criteria:** `openspec/changes/hera-v1/specs/hera-coordination/spec.md` (each `#### Scenario:` is one acceptance criterion)

## 1. Tests

**Depends on:** nothing (this is stage 1).

- [ ] 1.1 Write failing tests for every requirement scenario in `specs/hera-coordination/spec.md`. Each `#### Scenario:` becomes one Go test case using table-driven `testify` or stdlib `testing`. Place under `internal/<package>/*_test.go`. Use mock SQLite (in-memory `:memory:` for fast iteration) and `httptest.Server` for argus mocks. Confirm every test FAILS before any implementation (Prove-It pattern).
- [ ] 1.2 Validate the change: `openspec validate hera-v1 --strict` MUST pass after the spec text is committed (no implementation needed for this gate).

## 2. Project scaffold

**Depends on:** Stage 1 (tests can be parked under empty packages and remain failing).

- [ ] 2.1 Initialize `go.mod` with `module github.com/anutron/hera` and Go version `1.22+`.
- [ ] 2.2 Add Cobra root command at `cmd/hera/main.go`. Subcommands: `start`, `stop`, `status`, `resume`, `list`. Each subcommand file under `cmd/hera/<verb>.go`. `install` is intentionally absent (deferred follow-up).
- [ ] 2.3 Create package layout: `internal/{config,db,argus,events,mcp,inject,idle,daemon,log}`. Each with a placeholder `doc.go`.
- [ ] 2.4 Add `Makefile` with targets: `build`, `test`, `fmt`, `vet`, `clean`. `build` produces `./bin/hera`. `test` runs `go test ./... -race`.
- [ ] 2.5 Add `.gitignore` (Go defaults + `~/.hera/` should not be in repo).
- [ ] 2.6 Add minimal `README.md` (project description, status: "v1 in development", link to OpenSpec change folder).

## 3. SQLite schema + db layer

**Depends on:** Stage 2.

- [ ] 3.1 Implement `internal/db/schema.go`. WAL mode. Migrations as ordered `migration` functions; current version stored in `PRAGMA user_version`.
- [ ] 3.2 Define seven tables per design D8: `orchestrators`, `roles`, `bindings`, `messages`, `role_status`, `event_cursor`, `config`. Include indexes: partial index on `bindings(role_id) WHERE ended_at IS NULL`; composite index on `messages(to_role_id, read_at)`.
- [ ] 3.3 Implement DAOs in `internal/db/`: `Orchestrators`, `Roles`, `Bindings`, `Messages`, `RoleStatus`, `EventCursor`, `Config`. Each method returns concrete typed structs; no `interface{}` in DB layer.
- [ ] 3.4 Add `db.Open(path string) (*DB, error)` and `db.Close()`. `Open` creates parent directories, runs migrations on first open.
- [ ] 3.5 Unit-test every migration step (open empty file → version 0 → run migrations → final version + schema as expected).
- [ ] 3.6 Unit-test all DAO CRUD paths.

## 4. Argus HTTP client

**Depends on:** Stage 2.

- [ ] 4.1 Implement `internal/argus/client.go`. `New(baseURL string, token string) *Client`. Every request sends `Authorization: Bearer <token>`, `X-Argus-Plugin-Version: 1`, and a 30-second default timeout.
- [ ] 4.2 Implement task helpers: `ListTasks(ctx)`, `GetTask(ctx, id)`, `PutTaskMeta(ctx, id, key, value)`, `PostTaskInput(ctx, id, bytes)`.
- [ ] 4.3 Implement MCP registry helpers: `RegisterTool(ctx, tool)`, `UnregisterTool(ctx, name)`.
- [ ] 4.4 Implement event stream helper: `StreamEvents(ctx, sinceID int64, handler func(Event)) error`. SSE client that re-dials on transient errors and surfaces `resync` events to the handler.
- [ ] 4.5 Implement `StreamTaskOutput(ctx, taskID, handler func([]byte))` for the future view feature; can be a thin wrapper that gets wired later.
- [ ] 4.6 Unit-test every helper using `httptest.Server` to mock argus.

## 5. Config + token loading

**Depends on:** Stage 2.

- [ ] 5.1 Implement `internal/config/config.go`. Default config: argus URL `http://127.0.0.1:7743`, hera HTTP listener `127.0.0.1:7744`, state dir `~/.hera`, log file `~/.hera/hera.log`, idle debounce `2s`, MCP heartbeat `5m`.
- [ ] 5.2 Implement `config.LoadToken(stateDir string) (string, error)`. Reads `<stateDir>/api-token`, strips whitespace, errors if file missing or empty with the exact instructional message from the spec.
- [ ] 5.3 Unit-test missing-file, empty-file, valid-file paths.

## 6. Event subscriber + auto-adopt

**Depends on:** Stages 3, 4.

- [ ] 6.1 Implement `internal/events/subscriber.go`. Wraps `argus.Client.StreamEvents`. Reads cursor from `event_cursor` table at start, persists cursor after each successful handler invocation.
- [ ] 6.2 Implement event dispatch: `task.created`, `link.created`, `task.archived`, `task.renamed`, `session.idle`, `session.started`, `session.exited`, `resync`. Each dispatched to a typed handler.
- [ ] 6.3 Implement auto-adopt handler. On a `link.created` event, look up parent's binding via `Bindings.GetLiveByTaskID(parent)`. If parent is bound to a coordinator role AND the new task has `meta:hera.role=worker`, fetch the new task's meta for `hera.mission` + `hera.constraints`, create role + binding rows atomically.
- [ ] 6.4 Implement skipped-adoption logger: when link is bound to a coordinator role but meta is missing, log INFO with task ids and reason.
- [ ] 6.5 Implement resync handler: call `argus.Client.ListTasks` and reconcile bindings (mark ended any binding whose argus task no longer exists).
- [ ] 6.6 Unit-test the auto-adopt rule: stricter condition enforced, mission/constraints propagated, skipped logged.
- [ ] 6.7 Unit-test cursor persistence across restarts (using `:memory:` DB and synthetic event stream).

## 7. Idle tracker

**Depends on:** Stages 3, 4, 6.

- [ ] 7.1 Implement `internal/idle/tracker.go`. Maintains a `sync.Map[taskID, idleState]` where `idleState` is `{lastSessionEvent: enum, eventAt: time.Time}`.
- [ ] 7.2 Subscribe to `session.idle`, `session.started`, `session.exited` events via the event dispatcher.
- [ ] 7.3 `IsIdle(taskID string, debounce time.Duration) bool` returns true iff the most recent session event is `session.idle` AND `time.Since(eventAt) >= debounce`.
- [ ] 7.4 Default debounce is 2 seconds (from config); a comment in code flags this as tunable when substrate clarifies `session.idle` semantics.
- [ ] 7.5 Unit-test the three idle-eligibility branches from the spec (less-than-debounce, at-least-debounce, post-started).

## 8. Message injector

**Depends on:** Stages 4, 7.

- [ ] 8.1 Implement `internal/inject/inject.go`. `Inject(ctx, taskID, senderRoleName, body string) (DeliveryMode, error)`.
- [ ] 8.2 Format body as `[hera from <senderRoleName>] <body>`.
- [ ] 8.3 Look up idle state. Idle → POST body+`\n`, return `DeliveryModeIdleSubmit`. Not idle → POST body, return `DeliveryModeBusyBuffer`.
- [ ] 8.4 Caller writes the chosen mode back to the message row (this package doesn't touch DB).
- [ ] 8.5 Unit-test both modes against a mock argus + mock idle tracker.
- [ ] 8.6 Unit-test that the sender prefix is exactly the documented format (`[hera from <name>] `).

## 9. MCP tool registry + callback HTTP listener

**Depends on:** Stages 3, 4, 5.

- [ ] 9.1 Implement `internal/mcp/server.go`. Hosts an HTTP listener on the configured port. Routes: `POST /mcp/<tool-name>` for each of the five tools.
- [ ] 9.2 Each route parses argus's callback envelope (`{tool, input, context}`), authenticates the request via the shared `auth_header` (constant-time compare), dispatches to a per-tool handler, returns the handler's MCP-native `{content: [...], isError: bool}` response.
- [ ] 9.3 Implement `internal/mcp/registrar.go`. On daemon start, generates a per-session random `auth_header` secret, POSTs all five tool registrations to argus with this secret. On a 5-minute ticker, re-POSTs each registration.
- [ ] 9.4 On daemon shutdown, DELETEs each registration before exit.
- [ ] 9.5 Unit-test: a valid POST with correct auth succeeds; missing/wrong auth returns 401; unknown tool name returns 404 with MCP error envelope.

## 10. Handler: hera_join

**Depends on:** Stages 3, 6, 9.

- [ ] 10.1 Implement `internal/mcp/handler_join.go`. Inputs: `cwd` (required), optional `orchestrator`, `role_name`, `kind`, `mission`, `constraints`, `status`.
- [ ] 10.2 Resolve cwd → argus task via `argus.Client.ListTasks` + worktree-path match. Return `isError:true` if no match.
- [ ] 10.3 If only `cwd` is provided: look up existing binding; on hit, return role identity + unread message count. On miss, return `isError:true` with the "use explicit args to attach as freelance" message.
- [ ] 10.4 If full freelance args are provided: validate orchestrator exists, validate `(orchestrator, role_name)` is not already used with a different kind, create role + binding + role_status atomically. Return role identity.
- [ ] 10.5 Unit-test re-incarnation claim, freelance success, freelance with unknown orchestrator, freelance with conflicting kind.

## 11. Handler: hera_send

**Depends on:** Stages 3, 8, 9, 10.

- [ ] 11.1 Implement `internal/mcp/handler_send.go`. Inputs: `cwd` (required), `body` (required), optional `to`, `in_reply_to`.
- [ ] 11.2 Resolve sender role via cwd. Resolve recipient: explicit `to` → look up by `(orchestrator_id, role_name)`; absent → worker/freelance default-routes to the orchestrator's coordinator. Coordinator senders with no `to` are rejected (morning review: user-routing dropped).
- [ ] 11.3 Insert message row with `delivery_mode=pending`.
- [ ] 11.4 Look up recipient's live binding. If none, set `delivery_mode=queued_no_binding` (drain worker is a v1.1 follow-up).
- [ ] 11.5 If recipient has a live binding, call `inject.Inject`. Record returned mode + `delivered_at` on the message row.
- [ ] 11.6 Return `{message_id, recipient_role, delivery_mode}` in MCP response content.
- [ ] 11.7 Unit-test worker → coord (default route), worker → explicit other-worker, coordinator-without-to rejected, missing recipient role, idle injection path, busy injection path, queued-no-binding path.

## 12. Handler: hera_inbox

**Depends on:** Stages 3, 9, 10.

- [ ] 12.1 Implement `internal/mcp/handler_inbox.go`. Input: `cwd` (required).
- [ ] 12.2 Resolve role via cwd. Query `messages WHERE to_role_id=? AND read_at IS NULL ORDER BY sent_at`.
- [ ] 12.3 Return messages in MCP response content. Each message rendered as `<id> <sent_at> from <from_role_name>: <body>` plus an `in_reply_to` field when present.
- [ ] 12.4 Unit-test empty inbox, single message, multiple messages with deterministic ordering.

## 13. Handler: hera_mark_read

**Depends on:** Stages 3, 9, 10.

- [ ] 13.1 Implement `internal/mcp/handler_mark_read.go`. Inputs: `cwd` (required), `message_ids` (array of int64).
- [ ] 13.2 Resolve caller role via cwd. UPDATE messages SET read_at=NOW() WHERE id IN (...) AND to_role_id=<caller_role_id> AND read_at IS NULL.
- [ ] 13.3 Return `{marked_read_count}` in response.
- [ ] 13.4 Unit-test: own-message mark-read works, cross-role mark-read silently ignored (no rows affected), already-read messages not double-updated.

## 14. Handler: hera_status

**Depends on:** Stages 3, 4, 9, 10.

- [ ] 14.1 Implement `internal/mcp/handler_status.go`. Inputs: `cwd` (required), `status` (enum: `idle` / `working` / `blocked` / `done`).
- [ ] 14.2 Resolve role via cwd. UPSERT into `role_status` with new status + updated_at.
- [ ] 14.3 Write `meta:hera.thread_status=<status>` to the bound argus task via `argus.Client.PutTaskMeta`.
- [ ] 14.4 Return `{role_name, status, updated_at}` in response.
- [ ] 14.5 Unit-test status updates, invalid status rejected with MCP error, meta write happens, meta write failure surfaced.

## 15. Binding meta mirror

**Depends on:** Stages 3, 4, 6.

- [ ] 15.1 When `Bindings.Create` runs (called by auto-adopt and freelance-join paths), trigger a `meta:hera.role=<kind>` write via `argus.Client.PutTaskMeta`.
- [ ] 15.2 When `Bindings.End` runs (called on `task.archived` events), trigger a `meta:hera.thread_status=ended` write (or similar) to mark the prior binding done in argus task_meta.
- [ ] 15.3 Unit-test meta mirroring on both create and end paths.

## 16. Daemon main loop

**Depends on:** Stages 3, 4, 5, 6, 7, 8, 9.

- [ ] 16.1 Implement `internal/daemon/run.go`. `Run(ctx context.Context, cfg *config.Config) error`. Order: load token, open DB, build argus client, start event subscriber, start idle tracker subscription, start MCP server, register all five tools, log "hera ready" to stderr + log file.
- [ ] 16.2 Implement graceful shutdown on SIGINT/SIGTERM. Order: stop event subscriber, unregister MCP tools, stop MCP server, close DB.
- [ ] 16.3 Implement `cmd/hera/start.go`. Daemonize via double-fork (or use `daemon.Run` in foreground when `--foreground` is passed). Writes PID to `~/.hera/hera.pid`.
- [ ] 16.4 Implement `cmd/hera/stop.go`. Read PID, send SIGTERM, wait up to 10 seconds for the process to exit.
- [ ] 16.5 Implement `cmd/hera/status.go`. Print: PID (if running), uptime, last-seen event id, MCP tool registration status, orchestrator count, role count, live binding count.
- [ ] 16.6 Implement `cmd/hera/list.go`. Print orchestrators with their roles and live binding state (live | between incarnations | none).
- [ ] 16.7 Implement `cmd/hera/resume.go` as a STUB returning "not implemented in v1; spawn-fresh-incarnation will land in a follow-up change." (Resume depends on argus task_create + worktree setup details that are easier to land alongside the view.)
- [ ] 16.8 Unit-test daemon startup against a mock argus (tools register, event stream connects).
- [ ] 16.9 Smoke test: boot daemon against mock argus, send a fake `task.created` + `link.created` sequence with proper meta, verify hera writes a role + binding row.

## 17. Wrap-up

**Depends on:** Stages 1-16.

- [ ] 17.1 Re-run `openspec validate hera-v1 --strict`. Must pass.
- [ ] 17.2 Re-run `make test` with all tests passing (excluding any with `// pending v1.1` comments).
- [ ] 17.3 Write `OVERNIGHT_LOG.md` at repo root summarizing what shipped, what stayed stubbed, open substrate questions, and morning-review checklist for Aaron.
- [ ] 17.4 Final commit + push. Push tag `v0.1.0-headless` (annotated, message: "hera v1 headless surface: five MCP tools, role-as-identity, auto-injection, auto-adoption. Plugin view and settings UI deferred.").
