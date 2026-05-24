## Context

Argus today is a terminal-native LLM code orchestrator: it owns task lifecycles, worktrees, dependency DAGs, PTY sessions, and a peer-to-peer message bus between argus tasks. Its plugin substrate (drn/argus PRs 1-9) ships an external-process extension surface: scope-bound tokens, an SSE event stream, per-task metadata writes, runtime-registered MCP tools, runtime-registered settings sections, and runtime-registered plugin views.

The team has been hand-rolling a coordination layer on top of argus for months. Existing patterns:

- The `/orchestrate`, `/check-messages`, `/ask-orchestrator` skills already operate on named **roles**, not on argus task IDs. A worker re-launches into a fresh worktree, claims its role, and pulls its inbox. The role outlives the worktree.
- The substrate buildout itself (eight parallel PRs coordinated through argus's messaging and DAG) demonstrated that explicit coordination scales – when you have a structured place to log decisions, ask questions, and track thread status, parallel work doesn't collapse into chaos.
- The friction in those patterns is the impedance mismatch with argus's own primitives: argus thinks in task IDs; the user thinks in roles. Every coordination action requires the user to translate ("what task ID is `foo-coordinator` right now?") or to rely on out-of-band skills (CCC orchestrator inbox).

Ludwig formalizes the role layer as a first-class entity model and exposes it through argus's plugin contract. Roles are the durable identity; argus tasks are temporary vessels that incarnate them.

## Goals / Non-Goals

**Goals:**

- Make role-as-identity a real entity in code. Roles, decisions, questions, threads, and freeform notes persist in ludwig's own SQLite, independent of argus task lifecycle.
- Keep argus the source of truth for execution (worktree, PTY, sandbox, branch). Keep ludwig the source of truth for coordination (role, inbox, decisions, history).
- Provide a small, opinionated MCP surface (five tools) that an agent can use without thinking about ludwig's data model. Tool ergonomics matter: every call takes `cwd`, every other input is the actual data.
- Deliver messages to recipients automatically. When the recipient agent is idle, ludwig auto-submits messages directly into its PTY. When the recipient is busy, ludwig leaves the message in the input buffer for the user to submit.
- Auto-adopt coordinator-spawned worker tasks via argus's event stream, so the coordinator can use argus's existing `task_create` (and get the DAG view for free) without ludwig adding a redundant spawn verb.
- Support **cross-repo orchestration.** An orchestrator's roles can be in different argus projects; ludwig has no preference about co-location.
- Ship as a single Go binary. CLI verbs handle ops; the daemon does the work.

**Non-Goals:**

- **No plugin view in this change.** Substrate (drn/argus PR 9) is shipped, but the view's TUI library, rail rendering details, and per-pane focus behavior require their own design pass. Tracked as a follow-up change.
- **No settings section UI in this change.** The substrate supports `form` sections; the actual fields (cadence interval, view prefs, etc.) need their own design pass.
- **No `ludwig install` (launchd auto-start) in this change.** Depends on the view + settings being settled.
- **No multi-host operation.** One ludwig daemon per user account, talking to one argus daemon on `127.0.0.1`. Tailscale-routed multi-host is out of scope.
- **No external network egress.** Ludwig only talks to `127.0.0.1:7743`. No telemetry, no remote logging.
- **No conversational "talk to ludwig" surface.** The user talks to coordinator agents; coordinators talk to ludwig via MCP tools. Ludwig itself does not host a chat surface for the user.
- **No production-mode hardening of the MCP callback secret.** The shared secret is in-process state; if an attacker can write to `127.0.0.1:7744` they have already compromised the host.

## Decisions

### D1. Role-as-identity is the data model

An orchestrator is a named coordination group (e.g., `foo-coordinator`). A role is a named participant under an orchestrator (e.g., `f2-impl`). A binding is the (role_id, argus_task_id, started_at, ended_at) row that records each incarnation of a role.

**Why:** This matches how argus orchestration already works at the skill layer (`/orchestrate role <name>`). Lifting it into ludwig's data model makes the model formal and the workflows resumable across argus task lifetimes.

**Alternatives considered:**

- **Projection only** (argus tasks tagged with `meta:ludwig.role`; ludwig holds no entities). Rejected: archive-a-task-and-the-orchestrator-vanishes is the wrong outcome for the "come back in three weeks and add F4" use case.
- **Hybrid** (argus tasks plus ludwig stick-on task_meta keys). Rejected: same lifecycle problem; argus task_meta fills up with ludwig-internal keys.

### D2. Five-tool MCP surface (message bus, not specialized verbs)

The original spec listed seven tools (`decision_add`, `question_add`, `question_resolve`, `thread_set_status`, `ask`, `update`, `join`). Collapsed to:

- `ludwig_join` – claim/create role for the calling cwd.
- `ludwig_send` – send a message; type tagging is post-hoc, not part of the verb.
- `ludwig_inbox` – list unread messages.
- `ludwig_mark_read` – mark read.
- `ludwig_status` – set the caller's role status.

**Why:** Email/Slack/Notion all use one send verb. Specialized verbs front-load taxonomy choices the agent can't reliably make. Tagging messages as decisions/questions/asks/updates can happen later (after the fact, by the user or by a post-processor) without changing the send-side ergonomics. Status is the only verb kept separate because it's structured state, not prose.

**Alternatives considered:**

- **Original seven-tool surface.** Rejected: too many verbs for the actual semantic content; `ask` vs `question_add` were ambiguous; `update` vs `thread_set_status` overlapped.
- **One unified `ludwig_message` verb that takes a `kind` parameter.** Considered, but `status` doesn't fit cleanly (it's not really a message body, it's a state field).

### D3. Auto-injection with idle gating, no notification-only mode

When `ludwig_send` writes a message addressed to a role whose binding is live, ludwig delivers it directly into the recipient's PTY:

- **Recipient idle** (`session.idle` is the current state for the bound task, sustained ≥2 seconds): ludwig injects `<formatted-body>\n`. Trailing newline triggers Claude Code's submit. The agent processes the message as if the user typed it.
- **Recipient busy** (anything other than idle): ludwig injects `<formatted-body>` without `\n`. The text sits in the input buffer. The user submits when ready.

Formatted body: `[ludwig from <sender-role-name>] <body>`. One-line prefix so the agent knows the message is from ludwig and from whom.

**Why:** The agent never has to call `ludwig_inbox` to discover new messages in steady state – ludwig pushes. The notification-line approach (argus's pattern for inter-task messages) would require the recipient to know to call `ludwig_inbox` and then re-process the body. Direct injection is one less round trip; the body is the prompt.

**Alternatives considered:**

- **Notify-only (argus's existing pattern).** Inject `[ludwig] new message – call ludwig_inbox` and let the agent decide when to read. Rejected: extra round trip per message.
- **Always auto-submit, ignore idle state.** Rejected: collides with user-typed input mid-flight, producing Frankenmessages.
- **Queue-until-next-idle in the busy case.** Considered, rejected for v1: queueing makes messages invisible to the human user who might be away from the recipient pane; inject-and-let-the-user-see is more honest.

**Open dependency on substrate:** The exact semantics of `session.idle` (fires immediately on drainable input vs after a debounce post-generation) are pending the coordinator's reply. v1 takes the conservative path: a task counts as idle only after `session.idle` has been the active state for ≥2 seconds. Tunable when we learn more.

### D4. Auto-adoption of coordinator-spawned worker tasks

When a coordinator agent wants to split work into F1/F2/F3, it uses argus's existing `mcp__argus__task_create` with `parent_task=<self>` and `meta:ludwig.role=worker`. Ludwig watches the event stream; when it sees `task.created` + `link.created` (parent bound to a ludwig coordinator role), AND the new task has `meta:ludwig.role=worker`, ludwig adopts the task as a worker role and creates a role + binding row.

**Why:** Argus already builds DAGs from `task_link` and renders them in the DAG view. Adding a `ludwig_spawn` MCP tool would duplicate that and pollute the surface. The stricter rule (also require `meta:ludwig.role=worker`) prevents accidental adoption of unrelated task spawns by the same coordinator.

**Alternatives considered:**

- **Add a `ludwig_spawn` MCP tool that wraps `task_create` and creates the binding atomically.** Rejected: redundant with `task_create`; doesn't get the DAG view for free.
- **Coordinator messages the user, user runs `ludwig spawn` at the shell.** Rejected: human-in-loop friction for what is normally an autonomous coordinator action.

### D5. Freelance join via `ludwig_join` with extended args

An argus task already running in some project (potentially a different repo than the orchestrator's coordinator) can attach itself to an existing orchestrator post-hoc by calling `ludwig_join(cwd, orchestrator=<name>, role_name=<self-named>, kind="freelance", mission=<text>, constraints=<text>, status=<state>)`. Ludwig creates a freelance role + binding atomically.

**Why:** This is the "second-thought" pattern – the user spun up a one-off worktree, then decided it should be managed by the orchestrator. The freelance agent self-introduces with name + mission + constraints + status.

**Alternatives considered:**

- **Dedicated `ludwig_attach` tool separate from `ludwig_join`.** Rejected: two tools doing similar things. `ludwig_join` already exists for the re-incarnation case; extending its signature is cheaper than a new verb.

### D6. Cross-repo orchestration

Orchestrators have no `argus_project` column. Roles have one (set when first bound). Different roles under the same orchestrator can live in different argus projects (= different repos).

**Why:** Real coordination spans repos – the buildout project itself coordinated tasks across the argus repo and various plugins-to-be. Forcing single-project orchestrators rules out the dominant use case.

### D7. Headless v1, view deferred

The plugin view (embedded-terminal split with a project/agent rail) is deferred from this change. Substrate is shipped, but design questions remain: TUI library choice, rail rendering details, focus key bindings (only `Esc` is reserved by argus), how the rail re-renders on `task.created` / binding events, etc.

**Why:** The headless surface is self-contained and useful in isolation. An agent can already do all of ludwig's coordination work via MCP tools; the view is a UX layer on top. Splitting view from headless lets ludwig ship in two cleaner increments.

**Alternatives considered:**

- **Ship view in v1.** Rejected: design questions still unsettled; risks blocking the headless work.
- **Ship a stub view that just shows "ludwig running" inside the plugin-view surface.** Rejected: a meaningful view requires the rail + terminal embedding; a stub adds substrate registration code that we'd rewrite anyway.

### D8. Five-table SQLite schema

Tables (full schema in `internal/db/schema.go`):

```
orchestrators (id, name, created_at)
roles         (id, orchestrator_id, name, kind, argus_project, mission, constraints, created_at)
bindings      (id, role_id, argus_task_id, worktree_path, started_at, ended_at, end_reason)
messages      (id, from_role_id, to_role_id, body, in_reply_to, sent_at, read_at,
               delivery_mode, delivered_at)
role_status   (role_id PK, status, updated_at)
event_cursor  (id=1 singleton, last_seen_event_id)
config        (key PK, value)
```

WAL mode for concurrent reader access. Schema migrations in code, versioned by an `application_id` PRAGMA so we can detect mismatches.

`delivery_mode` on `messages` records how this message was delivered (`idle_submit` / `busy_buffer` / `pending`) so the operational log can answer "did this message auto-submit?" without re-deriving. `delivered_at` is when the inject POST returned.

### D9. MCP tool registration + callback HTTP listener

Ludwig hosts its own HTTP listener on `127.0.0.1:7744` (configurable). On daemon startup it POSTs five tool registrations to argus's `/api/mcp/tools` endpoint, each with a `callback_url` like `http://127.0.0.1:7744/mcp/ludwig_send` and a randomly-generated `auth_header` shared secret. The secret lives in process memory; restarts mint a new secret and re-register.

Heartbeat re-registration runs every 5 minutes (substrate idle window is 10). Heartbeat is a re-POST of the same registration – the substrate refreshes the `LastSeenAt` row in argus's MCP registry.

**Why:** Substrate cleanup on revoke/idle is automatic; ludwig doesn't have to think about it. Re-registration is cheap and idempotent.

### D10. Conservative idle gate (2-second debounce)

The `session.idle` event semantics are not yet pinned (substrate question in flight). Ludwig treats a task as idle only after `session.idle` has been the active state for ≥2 seconds. This handles brief generation pauses without spuriously firing as idle.

**Tunable:** when the coordinator clarifies `session.idle` semantics, drop or raise the debounce. If `session.idle` is fine-grained ("drainable now"), debounce can drop to 0. If `session.idle` is coarse ("agent done generating"), it can rise.

## Risks / Trade-offs

- **`session.idle` semantics unknown.** → Conservative 2-second debounce; tune when substrate question is answered. Worst case in interim: ludwig under-auto-submits, leaving more messages in busy-buffer mode (still functional, just slower processing).
- **User-in-progress typing collision in busy case.** → Documented behavior; user can backspace/edit. Future option: explicit `urgent=true` flag on send that bypasses the busy path.
- **Auto-adoption misses if coordinator forgets `meta:ludwig.role=worker`.** → Stricter rule (require both parent link AND meta) is deliberate – better to miss adopt-able tasks than to spuriously adopt unrelated ones. Surface via daemon log so the user can spot the misconfiguration.
- **Token rotation has no graceful path.** → If the user revokes the scope token, ludwig's HTTP calls 401 and the daemon logs + exits. The user re-mints and restarts. No hot-reload of token in v1.
- **MCP callback secret in process memory.** → Restart resets the secret; argus's tool registrations carry the auth header so the old secret stops working as soon as the new daemon re-registers. Brief window during shutdown where a leftover argus callback could 401; cosmetic.
- **Cross-repo orchestrators with different argus projects.** → Role's `argus_project` is set when first bound. If a role's project ever changes (e.g., monorepo split), ludwig will spawn the next incarnation in the old project. Manual fix: edit `roles.argus_project`. Out of scope for v1.
- **No backup/recovery for ludwig's SQLite.** → Single-user, local-only. The user can `cp ~/.ludwig/state.sqlite` themselves. Out of scope for v1.

## Migration Plan

Greenfield. Bootstrap steps for first install:

1. Build `ludwig` binary: `make build` produces `./bin/ludwig`.
2. Install to PATH: `cp ./bin/ludwig ~/bin/` (manual; `ludwig install` deferred).
3. Mint scope token: `argus token mint --scope ludwig > ~/.ludwig/api-token`.
4. Initialize ludwig state: `ludwig start` creates `~/.ludwig/state.sqlite` on first boot.
5. Verify registration: `argus token list` shows the ludwig scope; ludwig logs five MCP tool registrations on startup.

Rollback: revoke the scope token via `argus token revoke`. Argus cascades the revocation by dropping every MCP tool and (future) settings section ludwig owns. Delete `~/.ludwig/` to wipe state.

## Open Questions

- **`session.idle` semantics.** Pending coordinator reply to substrate question (sent message `1779615721295314000`). Resolves the right value for the idle-debounce constant in `internal/idle/`.
- **`ludwig_inbox` default behavior.** Returns unread by default; should it also accept a `since` cursor for "last 24h" type queries? V1: unread-only; add cursor support if usage warrants.
- **`ludwig_send` to=`user`.** Coordinator → user routing: for v1, "user" is a pseudo-recipient – the message lands in the DB but isn't injected anywhere (no human PTY to inject into). When the view ships, "user" messages render in the coordinator pane. Confirm the v1 stub behavior is acceptable.
- **Status enum.** v1 uses `idle` / `working` / `blocked` / `done`. Expand?

## Discovery findings

- **PLAN.md** (substrate plan) is **not** committed to argus's master. Lives at `~/.argus/worktrees/ARGUS/build-plan-ludwig-orchestrator/PLAN.md`. The "Worked example: a hypothetical 'ludwig' plugin" section is the original ludwig spec.
- **`docs/plugins.md`** (argus repo) is the authoritative plugin contract. Known divergences appendix documents seven shifts between plan and ship, three of which were resolved during this brainstorm (substrate gap 1 fix shipped; gap 2 resolved as doc-only fix; gap 3 pivoted to plugin-views and shipped).
- **Argus already exposes PTY output streaming via `GET /api/tasks/{id}/stream` (SSE).** Plugin-callable today; the web UI uses this for xterm.js. Ludwig will use it when the view ships.
- **Argus reserves only `Esc`** as a within-view keystroke. Everything else (including `Ctrl+C`, `Tab`, function keys) is forwarded to the plugin view. Important for view-layer design when it ships.
- **The existing `/orchestrate` / `/check-messages` / `/ask-orchestrator` CCC skills** are the precedent for role-as-identity. They use a separate orchestrator inbox (CCC) that ludwig effectively replaces with a more integrated substrate-aware version.

## Acceptance criteria

Per-section behavioral criteria, will become OpenSpec scenarios in the delta spec.

### D1 (Role-as-identity)

- it should preserve role identity (name, mission, constraints, decisions, messages) across argus task lifecycle events: archive of the bound task ends the binding but does not delete the role.
- it should allow a single role to be bound to a fresh argus task multiple times in sequence, with each incarnation recording its own (started_at, ended_at) row.
- it should allow an orchestrator's roles to live in different argus projects.

### D2 (Five-tool MCP surface)

- it should expose exactly five MCP tools under the `ludwig_` prefix when the daemon is running and registered with argus.
- it should accept `cwd` as the first input of every tool.
- it should reject tool calls whose `cwd` does not map to a known argus task (404 with explanatory error message).

### D3 (Auto-injection)

- it should auto-submit (`\n`-terminated) when the recipient role's bound task has been in `session.idle` state for ≥2 seconds.
- it should leave the message body in the input buffer (no `\n`) when the recipient task is not idle.
- it should record `delivery_mode` on the message row (`idle_submit` / `busy_buffer` / `pending`).
- it should format the injected body as `[ludwig from <sender-role>] <body>` so the recipient agent can identify the source.

### D4 (Auto-adoption)

- it should auto-adopt a new argus task as a worker role of orchestrator X when:
  - the new task has a `link.created` event whose parent is a task bound to orchestrator X's coordinator role, AND
  - the new task has `meta:ludwig.role=worker`.
- it should NOT auto-adopt when either condition is missing.
- it should read `meta:ludwig.mission` and `meta:ludwig.constraints` if present and populate them on the new role row.

### D5 (Freelance join)

- it should create a freelance role + binding when `ludwig_join` is called with an explicit orchestrator name, role_name, kind=freelance, and (optionally) mission/constraints/status.
- it should reject a freelance join attempt that names an orchestrator that does not exist.
- it should reject a freelance join attempt whose (orchestrator, role_name) already exists with a different `kind`.

### D6 (Cross-repo)

- it should record `argus_project` per role at first binding and preserve it across incarnations.
- it should not require the orchestrator to be bound to any single argus project.

### D9 (MCP registration)

- it should POST registrations for all five tools to argus's `/api/mcp/tools` on daemon startup.
- it should re-POST each registration on a 5-minute heartbeat to stay alive in argus's MCP idle sweep (10-minute default).
- it should unregister all tools on graceful daemon shutdown.

### D10 (Idle debounce)

- it should treat a task as idle only when `session.idle` has been the active state for ≥2 seconds.
- it should not treat a task as idle if a `session.started` or `session.exited` event has fired more recently than the latest `session.idle` event.
