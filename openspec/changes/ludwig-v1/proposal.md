## Why

Argus orchestrates many parallel tasks but treats each task as an ephemeral execution unit – when a worktree gets archived, its identity, inbox, and institutional memory go with it. The team has been hand-rolling that continuity layer on top of argus for months (the `/orchestrate`, `/check-messages`, `/ask-orchestrator` skills, the substrate-buildout project itself). Argus's just-landed plugin contract (drn/argus PRs 1-9) gives an external process the surface to do this properly. Ludwig is that process: a daemon that owns *role-as-identity* coordination on top of argus.

The forcing function is concrete: when a coordinator wraps up a feature in week 1 and the user comes back in week 3 to add F4/F5/F6, the coordinator's worktree is gone but the institutional knowledge of the project should not be. Ludwig keeps the roles, decisions, questions, and threads alive across argus task lifetimes.

## What Changes

This is a greenfield repo (anutron/ludwig). Every item below is an addition.

- Add Go daemon binary `ludwig` (Cobra-based CLI) registering as an argus plugin via `argus token mint --scope ludwig`.
- Add **role-as-identity data model**: orchestrators own roles; roles have durable identity; bindings record each (role, argus-task) incarnation with start/end timestamps. Argus tasks come and go; roles persist.
- Add five MCP tools, all force-prefixed `ludwig_`:
  - `ludwig_join` (cwd, [orchestrator, role_name, kind, mission, constraints]) – on agent boot, claim or create a role for the current cwd.
  - `ludwig_send` (cwd, body, [to], [in_reply_to]) – send a message; routes via role identity; default destination is the orchestrator's coordinator.
  - `ludwig_inbox` (cwd) – list unread messages addressed to the caller's role.
  - `ludwig_mark_read` (cwd, message_ids) – mark messages read.
  - `ludwig_status` (cwd, status) – set the caller role's status (`idle` / `working` / `blocked` / `done`).
- Add **message-bus delivery** with idle-gated auto-injection:
  - When the recipient role's bound argus task is idle (per `session.idle`), ludwig injects `<body>\n` into the recipient's PTY via `POST /api/tasks/{id}/input` – the trailing newline auto-submits the message as if the user typed it.
  - When the recipient is not idle, ludwig injects `<body>` without `\n` – the user submits when ready.
- Add **auto-adoption** of coordinator-spawned worker tasks: ludwig subscribes to argus's event stream; when a `task.created` + `link.created` event names a parent bound to a ludwig coordinator role AND the new task has `meta:ludwig.role=worker`, ludwig adopts it as a worker role and records the binding. Adoption also reads `meta:ludwig.mission` and `meta:ludwig.constraints` if present.
- Add **freelance join** path: an existing argus task in any project / any repo can call `ludwig_join` with an explicit orchestrator name and `kind="freelance"` to attach itself to a coordination group post-hoc, supplying its own mission/constraints/status.
- Add **task metadata writes** under the `ludwig` namespace (auto-derived from the scope token after substrate gap 1 fix): `meta:ludwig.role` and `meta:ludwig.thread_status` mirror role state for downstream consumers.
- Add CLI verbs: `ludwig start`, `ludwig stop`, `ludwig status`, `ludwig resume <orchestrator>:<role>`, `ludwig list`.
- Add local SQLite state at `~/.ludwig/state.sqlite` with WAL mode and in-code schema migrations.
- Add ludwig HTTP listener (default `127.0.0.1:7744`) hosting MCP tool callbacks for argus to POST into; auth via a per-callback shared secret ludwig generates at MCP-registration time.

**Deferred from this change (will land in follow-up changes):**

- The plugin view (embedded-terminal split with a project/agent rail). Substrate (drn/argus PR 9) is shipped but the view's TUI library, rail rendering details, and per-pane focus behavior are not yet pinned. Tracked separately.
- The settings section UI (form for cadence configuration). The substrate supports form sections; the actual fields need a brief design pass.
- `ludwig install` (launchd plist for auto-start on login). Depends on the view + settings being settled.

## Capabilities

### New Capabilities

- `ludwig-coordination`: The complete v1 surface – role-as-identity data model, five-tool MCP message bus, auto-adoption from argus events, idle-gated auto-injection, headless daemon lifecycle, freelance join, and metadata mirroring under the `ludwig` namespace.

### Modified Capabilities

None (greenfield project).

## Impact

- **New repo:** anutron/ludwig (already provisioned). Working branch: `argus/ludwig-argus-coordinator` (argus task worktree).
- **External dependency:** argus daemon at `http://127.0.0.1:7743` with plugin substrate (drn/argus PRs 1-9) landed. Ludwig sends `Authorization: Bearer <scope-token>` and `X-Argus-Plugin-Version: 1` on every request.
- **Filesystem footprint:**
  - `~/.ludwig/api-token` (scope token, minted out-of-band via `argus token mint --scope ludwig`; ludwig reads, never writes).
  - `~/.ludwig/state.sqlite` (ludwig's data store).
  - `~/.ludwig/ludwig.log` (operational log).
- **No external network egress.** Ludwig only talks to `127.0.0.1:7743`. No telemetry, no remote logging.
- **Single-tenant.** One ludwig daemon per user, managing one argus daemon. Multi-host out of scope for v1.
- **No production-mode auth.** The MCP callback shared secret is in-process state; if attackers can already write to `127.0.0.1:7744`, they've already compromised the host.
