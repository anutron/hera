# Overnight log – ludwig v1 headless shipping pass

**Date:** 2026-05-24 (overnight)
**Branch:** `argus/ludwig-argus-coordinator`
**Worktree:** `/Users/aaron/.argus/worktrees/Ludwig/ludwig-argus-coordinator`
**Repo:** `anutron/ludwig` (already provisioned; nine commits on this branch tonight)

## TL;DR

The OpenSpec change folder is fully written, validated (`openspec validate ludwig-v1 --strict` passes), and committed. Everything in the headless v1 surface is implemented and unit-tested with the race detector. `make build` produces `./bin/ludwig`; `make test` is fully green. The plugin view, settings section UI, and `ludwig install` (launchd) are intentionally deferred to follow-up changes per the brainstorm carve-outs.

## What shipped

| Stage (tasks.md) | Status | Where |
|------------------|--------|-------|
| 1. OpenSpec change folder | done | `openspec/changes/ludwig-v1/{proposal,design,tasks}.md` + delta spec at `specs/ludwig-coordination/spec.md` |
| 2. Go project scaffold | done | `cmd/ludwig/*`, `internal/*/doc.go`, `Makefile`, `.gitignore`, `README.md` |
| 3. SQLite schema + DAOs | done | `internal/db/` – orchestrators, roles, bindings, messages, role_status, event_cursor, config tables; full CRUD with tests |
| 4. argus HTTP client | done | `internal/argus/` – tasks, meta, input, MCP registry, SSE event stream with reconnect-on-error |
| 5. Event subscriber + auto-adopt | done | `internal/events/` – stricter adoption rule, mission/constraints meta-mirroring, task.archived → end binding, resync handler |
| 6. MCP tool registry + HTTP listener | done | `internal/mcp/` – Server (callback HTTP), Registrar (register + 5m heartbeat + unregister), constant-time auth, per-session random shared secret |
| 7. Four basic handlers | done | `ludwig_join`, `ludwig_status`, `ludwig_inbox`, `ludwig_mark_read` in `internal/mcp/handler_*.go` |
| 8. Idle tracker | done | `internal/idle/` – per-task session-event state, 2-second debounce, injectable clock for tests |
| 9. Message injector | done | `internal/inject/` – `[ludwig from <role>] <body>` formatting, idle = + `\n` (auto-submit), busy = no `\n` |
| 10. `ludwig_send` handler | done | `internal/mcp/handler_send.go` – default routing (worker → coord, coord → user), explicit `to`, queued-no-binding state |
| 11. Daemon main loop + CLI verbs | done | `internal/daemon/`, `internal/config/`, `cmd/ludwig/start.go` + `stop.go` + `status.go` + `list.go` |

## Test coverage

`go test ./... -race -count=1` passes. Per-package coverage:

- `internal/db` – schema migrations idempotent across reopen; every DAO CRUD path; cross-role mark-read silently skips; user-kind messages have nil to_role_id.
- `internal/argus` – headers (Bearer + Plugin-Version), ListTasks/GetTask/GetTaskMeta/PutTaskMeta/PostTaskInput/RegisterTool/UnregisterTool, SSE stream parses event blocks and ignores keep-alive comments, error responses propagate.
- `internal/events` – stricter adoption rule (parent must be coordinator-bound AND child meta:ludwig.role=worker), missing-meta skip, wrong-meta skip, parent-not-coordinator skip, task.archived ends binding, multi-handler fan-out, cursor advances and persists.
- `internal/idle` – within debounce → not idle, after debounce → idle, session.started/exited supersedes idle, fresh idle re-debounces, non-session events ignored, per-task isolation.
- `internal/inject` – format string is exact, idle path writes body + `\n`, busy path writes body, PTY errors propagate.
- `internal/mcp` – callback dispatch + auth + envelope mismatch + 404 + 405; full register/heartbeat/unregister roundtrip; every handler's happy + error branches.
- `internal/daemon` – smoke test boots against stubbed argus, asserts all five tools register, then asserts all unregister on shutdown.
- `internal/config` – defaults reasonable; token missing/empty errors carry the suggested fix; state dir created with right perms.

## Design decisions made tonight without you

These are decisions I made autonomously while building. None of them break what we discussed in the brainstorm, but they're worth your sanity-check.

- **Coordinator bootstrap path through `ludwig_join`.** `ludwig_join` with `kind=coordinator` will now idempotently create the orchestrator if it doesn't exist. `kind=worker` and `kind=freelance` still require an existing orchestrator. Reason: the brainstorm said "the user types `ludwig spawn`", but I deferred `ludwig spawn` to a follow-up change, so the first agent of a new project needs a way to bootstrap its own orchestrator. This is the cleanest hole I could fill.
- **Coordinator → user routing is silent.** When a coordinator sends a message with no explicit `to`, ludwig persists it with `to_kind=user` and `to_role_id=NULL`. No PTY injection happens (there's no human PTY to inject into). The message lives in the DB and will surface in the plugin view once that ships. For now, `ludwig list` doesn't expose user-bound messages – they're effectively a deferred drain queue.
- **Queued-no-binding mode.** When a message's recipient role exists but has no live argus task, the message is persisted with `delivery_mode=queued_no_binding`. There is no drain worker yet (the role's next incarnation won't catch up automatically). Flagged in `tasks.md` 11.5 as a v1.1 follow-up.
- **5-minute MCP heartbeat.** Argus's idle sweep defaults to 10 minutes; ludwig re-POSTs each tool registration every 5 minutes. Half-window margin per the substrate doc's recommendation.
- **`ludwig start` without `--foreground` is unimplemented.** Background daemonization (double-fork or launchd) would be 50-100 lines of fiddly OS-specific code that we'll get for free once `ludwig install` ships. For now, the verb returns an explanatory error pointing at `nohup` and launchd. Per Aaron's habits, this is fine for the dev cycle.
- **Idle debounce is hardcoded at 2 seconds.** Configurable via `config.Config.IdleDebounce` but no CLI flag exposes it. We tune it once the coordinator answers the still-pending session.idle semantics question.

## What's still open

- **Substrate question still in flight.** I asked the coordinator agent for `session.idle` semantics (does the event fire on drainable-input vs after a debounce post-generation). No reply yet. Until that lands, the 2-second debounce in `internal/idle/tracker.go` line 9-12 holds; tune to match argus's actual signal once we know.
- **Plugin view.** Substrate landed (PR 9 on the argus side – `POST /api/plugins/views`, `GET /api/tasks/{id}/stream` SSE, `POST /api/tasks/{id}/input`). Ludwig has the pieces it needs (argus.Client.StreamEvents shape can be lifted into a PTY-stream helper); the open design questions are the TUI library choice and the rail rendering details we deferred from the brainstorm.
- **Settings section.** Form-only, single section, probably one field for cadence (which we no longer need since auto-injection replaced the scheduler), and maybe a read-only summary of orchestrators. Worth deferring or revisiting.
- **`ludwig install` (launchd plist).** Deferred per the brainstorm carve-out.
- **`ludwig resume`.** Stubbed in `cmd/ludwig/resume.go`. Needs the actual argus task_create wiring for fresh incarnations. Touches the spawn flow we deferred from v1.

## Suggested morning actions

1. **Read the change folder.** Either via `openspec show ludwig-v1` or by opening the four files: `proposal.md`, `design.md`, `specs/ludwig-coordination/spec.md`, `tasks.md`. If anything in there is wrong, push back; I'd rather rewrite than build on a wrong spec.
2. **Sanity-check the design decisions in the section above.** Especially the coordinator-bootstrap-through-ludwig_join change – that was my call.
3. **Check the substrate question reply.** When the coordinator replies on session.idle semantics, the only code change is the debounce constant. I'll update it as a follow-up commit if you forward me the answer.
4. **Try a smoke run.** Once you re-mint your argus binary with PR 9 merged:
   ```bash
   argus token mint --scope ludwig > ~/.ludwig/api-token
   chmod 600 ~/.ludwig/api-token
   ./bin/ludwig start --foreground
   ```
   In another argus task, call `ludwig_join` with `kind=coordinator` to bootstrap. Then spawn a worker via argus's `task_create` with the right meta and watch ludwig adopt it.
5. **Decide the next change folder.** Three obvious candidates: plugin view, settings section, `ludwig install`. The view is the most user-visible. The view + settings can land together if you want, or the view can land first and settings can stay deferred.

## Open questions for you

- **Should the orchestrator-bootstrap path stay in `ludwig_join`, or move to its own `ludwig_new_orchestrator` tool?** Current design folds it into `ludwig_join(kind=coordinator)` for tool-surface minimalism. The downside is the path is slightly hidden – you might not think to bootstrap an orchestrator from the join verb.
- **Should `ludwig_send` to=`user` be persisted differently?** Right now it lands in the messages table with `to_kind=user`, `to_role_id=NULL`. When the view ships, those messages need a UI surface. Worth deciding now if user-bound messages should also have something like a per-orchestrator sequence so the view can group them.
- **Per-message `urgent=true` flag?** If we want the option to inject + auto-submit even when the recipient is busy, we add a boolean to `ludwig_send`. Not implemented in v1; flag a preference.

## Commits this session

```
9139cc5 Add OpenSpec scaffold and ludwig-v1 change folder
7fddacf Scaffold Go project layout (cmd + internal packages + Makefile)
3f3eb91 Implement SQLite schema, migrations, and typed DAOs for the db layer
0cfb028 Implement argus HTTP client with tasks, meta, input, MCP registry, and SSE
078d297 Implement event subscriber + auto-adopt handler + resync handler
ac5daa2 Implement per-task idle tracker with 2-second conservative debounce
3895086 Implement message injector with idle gate and standard sender prefix
cd52ecf Implement MCP tool registry + callback HTTP listener
a823cea Implement four MCP handlers: join, status, inbox, mark_read
536431e Implement ludwig_send handler with idle-gated injection and default routing
dcebf92 Wire daemon main loop: config, Start/Stop/Run, smoke test, and CLI verbs
```

Every commit pushed to `origin/argus/ludwig-argus-coordinator`. You can `gh pr create` against `main` whenever you're ready; the branch is in a coherent state.

Good morning.
