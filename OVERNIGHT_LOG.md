# Overnight log – hera v1 headless shipping pass

**Date:** 2026-05-24 (overnight; morning-review applied; project renamed ludwig → hera 2026-05-24 afternoon)
**Branch:** `argus/ludwig-argus-coordinator` (argus-owned slug from when the task spawned; the project itself is now hera)
**Worktree:** `/Users/aaron/.argus/worktrees/Ludwig/ludwig-argus-coordinator` (argus-managed; not renamed)
**Repo:** `anutron/hera`

## Morning review applied

Aaron's Plannotator pass on 2026-05-24 settled the open questions below. Changes folded into v1:

- **Coordinator → user routing removed entirely.** Coordinators talk to the human in their own Claude pane (the plugin view's left panel). `hera_send` from a coordinator with no `to` now returns an error. The `to_kind` column on messages, the `DeliveryUserInbox` mode, and the `user` pseudo-recipient are gone from schema, types, handler, and spec.
- **Next change's settings shape locked.** Two fields: idle debounce (int seconds) and auto-inject enabled (bool). No active-orchestrators list (lives in the view), no default-orchestrator field (always a project name), no user-message-surface field (no user-bound messages exist).
- **Build order for follow-ups locked:** settings → view → install.
- **`hera_new_orchestrator` confirmed as a separate tool** (will split out of `hera_join`'s freelance path) in the next tool-surface change. v1 keeps it on `hera_join` so the headless surface ships; the split is a non-breaking addition.
- **Per-message `urgent=true` postponed** – revisit only if needed once Aaron is using hera.

The substrate question on `session.idle` semantics is still in flight – the 2-second conservative gate stays until the coordinator answers; the answer only determines if it can drop.

## TL;DR

The OpenSpec change folder is fully written, validated (`openspec validate hera-v1 --strict` passes), and committed. Everything in the headless v1 surface is implemented and unit-tested with the race detector. `make build` produces `./bin/hera`; `make test` is fully green. The plugin view, settings section UI, and `hera install` (launchd) are intentionally deferred to follow-up changes per the brainstorm carve-outs.

## What shipped

| Stage (tasks.md) | Status | Where |
|------------------|--------|-------|
| 1. OpenSpec change folder | done | `openspec/changes/hera-v1/{proposal,design,tasks}.md` + delta spec at `specs/hera-coordination/spec.md` |
| 2. Go project scaffold | done | `cmd/hera/*`, `internal/*/doc.go`, `Makefile`, `.gitignore`, `README.md` |
| 3. SQLite schema + DAOs | done | `internal/db/` – orchestrators, roles, bindings, messages, role_status, event_cursor, config tables; full CRUD with tests |
| 4. argus HTTP client | done | `internal/argus/` – tasks, meta, input, MCP registry, SSE event stream with reconnect-on-error |
| 5. Event subscriber + auto-adopt | done | `internal/events/` – stricter adoption rule, mission/constraints meta-mirroring, task.archived → end binding, resync handler |
| 6. MCP tool registry + HTTP listener | done | `internal/mcp/` – Server (callback HTTP), Registrar (register + 5m heartbeat + unregister), constant-time auth, per-session random shared secret |
| 7. Four basic handlers | done | `hera_join`, `hera_status`, `hera_inbox`, `hera_mark_read` in `internal/mcp/handler_*.go` |
| 8. Idle tracker | done | `internal/idle/` – per-task session-event state, 2-second debounce, injectable clock for tests |
| 9. Message injector | done | `internal/inject/` – `[hera from <role>] <body>` formatting, idle = + `\n` (auto-submit), busy = no `\n` |
| 10. `hera_send` handler | done | `internal/mcp/handler_send.go` – default routing (worker → coord, coord → user), explicit `to`, queued-no-binding state |
| 11. Daemon main loop + CLI verbs | done | `internal/daemon/`, `internal/config/`, `cmd/hera/start.go` + `stop.go` + `status.go` + `list.go` |

## Test coverage

`go test ./... -race -count=1` passes. Per-package coverage:

- `internal/db` – schema migrations idempotent across reopen; every DAO CRUD path; cross-role mark-read silently skips; user-kind messages have nil to_role_id.
- `internal/argus` – headers (Bearer + Plugin-Version), ListTasks/GetTask/GetTaskMeta/PutTaskMeta/PostTaskInput/RegisterTool/UnregisterTool, SSE stream parses event blocks and ignores keep-alive comments, error responses propagate.
- `internal/events` – stricter adoption rule (parent must be coordinator-bound AND child meta:hera.role=worker), missing-meta skip, wrong-meta skip, parent-not-coordinator skip, task.archived ends binding, multi-handler fan-out, cursor advances and persists.
- `internal/idle` – within debounce → not idle, after debounce → idle, session.started/exited supersedes idle, fresh idle re-debounces, non-session events ignored, per-task isolation.
- `internal/inject` – format string is exact, idle path writes body + `\n`, busy path writes body, PTY errors propagate.
- `internal/mcp` – callback dispatch + auth + envelope mismatch + 404 + 405; full register/heartbeat/unregister roundtrip; every handler's happy + error branches.
- `internal/daemon` – smoke test boots against stubbed argus, asserts all five tools register, then asserts all unregister on shutdown.
- `internal/config` – defaults reasonable; token missing/empty errors carry the suggested fix; state dir created with right perms.

## Design decisions made tonight without you

These are decisions I made autonomously while building. None break what we discussed in the brainstorm. The morning review section above supersedes any that conflict.

- **Coordinator bootstrap path through `hera_join`.** `hera_join` with `kind=coordinator` idempotently creates the orchestrator if it doesn't exist. `kind=worker` and `kind=freelance` still require an existing orchestrator. Stays as v1 behavior; will move into `hera_new_orchestrator` in the next tool-surface change.
- **Queued-no-binding mode.** When a message's recipient role exists but has no live argus task, the message is persisted with `delivery_mode=queued_no_binding`. There is no drain worker yet (the role's next incarnation won't catch up automatically). Flagged in `tasks.md` 11.5 as a v1.1 follow-up.
- **5-minute MCP heartbeat.** Hera re-POSTs each of its tool registrations to argus every 5 minutes so argus's MCP idle sweep (10-minute default) doesn't garbage-collect them. Not related to worker idle detection; that's the `session.idle` event subscription, a different mechanism.
- **`hera start` without `--foreground` is unimplemented.** Background daemonization (double-fork or launchd) would be 50-100 lines of fiddly OS-specific code that we'll get for free once `hera install` ships. For now, the verb returns an explanatory error pointing at `nohup` and launchd.
- **Idle debounce is hardcoded at 2 seconds.** Configurable via `config.Config.IdleDebounce` but no CLI flag exposes it (the planned settings panel will). Tune once the coordinator answers the still-pending session.idle semantics question.

## What's still open

- **Substrate question still in flight.** I asked the coordinator agent for `session.idle` semantics (does the event fire on drainable-input vs after a debounce post-generation). No reply yet. Until that lands, the 2-second debounce in `internal/idle/tracker.go` holds.
- **Plugin view (ships next-next).** Substrate is ready. Open design choices: TUI library, rail rendering details. Per build order: ships after settings.
- **Settings section (ships next).** Two fields locked: idle debounce + auto-inject toggle. No active-orchestrators list, no default-orchestrator field, no user-message-surface field.
- **`hera install` (launchd plist).** Ships last.
- **`hera resume`.** Stubbed in `cmd/hera/resume.go`. Needs argus task_create wiring for fresh incarnations; touches the spawn flow we deferred from v1.

## Suggested next actions

1. **Annotate the change folder as a group.** All four artifacts in one Plannotator session:
   ```
   plannotator annotate /Users/aaron/.argus/worktrees/Ludwig/ludwig-argus-coordinator/openspec/changes/hera-v1/
   ```
   (Run from outside the argus task sandbox – either your shell or via `!` in this session.)
2. **Smoke-run hera.** Once your argus binary has the PR 9 substrate plus the gap-1 fix:
   ```bash
   argus token mint --scope hera > ~/.hera/api-token
   chmod 600 ~/.hera/api-token
   ./bin/hera start --foreground
   ```
   In another argus task, call `hera_join` with `kind=coordinator` to bootstrap. Then spawn a worker via argus's `task_create` with `meta:hera.role=worker` and watch hera adopt it. `hera list` will show the orchestrator tree.
3. **Forward the coordinator's `session.idle` reply when it arrives.** I'll tune the debounce.
4. **Start the settings change folder.** Two fields locked; quick `openspec new change hera-settings` and we can run it under the same brainstorm + execute pipeline.

## Commits this session

```
9139cc5 Add OpenSpec scaffold and hera-v1 change folder
7fddacf Scaffold Go project layout (cmd + internal packages + Makefile)
3f3eb91 Implement SQLite schema, migrations, and typed DAOs for the db layer
0cfb028 Implement argus HTTP client with tasks, meta, input, MCP registry, and SSE
078d297 Implement event subscriber + auto-adopt handler + resync handler
ac5daa2 Implement per-task idle tracker with 2-second conservative debounce
3895086 Implement message injector with idle gate and standard sender prefix
cd52ecf Implement MCP tool registry + callback HTTP listener
a823cea Implement four MCP handlers: join, status, inbox, mark_read
536431e Implement hera_send handler with idle-gated injection and default routing
dcebf92 Wire daemon main loop: config, Start/Stop/Run, smoke test, and CLI verbs
```

Every commit pushed to `origin/argus/ludwig-argus-coordinator`. You can `gh pr create` against `main` whenever you're ready; the branch is in a coherent state.

Good morning.
