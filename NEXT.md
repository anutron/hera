# Next – hera 1.0 and beyond

Read this first if you're starting a fresh session on hera. It captures what shipped in 1.0, the v1.x backlog, and the operating model.

## Current state (2026-06-06)

**v1.0 shipped.** The full coordination + TUI stack is live on `main`.

What's in 1.0:

- **Coordination layer** – six MCP tools (`hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`), role-as-identity model, idle-gated injection bus, auto-adopt, doorbell re-nudge with `read_at` receipt.
- **hera-substrate-link** – argus REST port discovery via daemon socket RPC; pid-mtime watcher for restart detection; degraded-state MCP gate; force re-register on recovery.
- **hera-settings** – `idle_debounce_seconds` and `auto_inject_enabled` operator knobs, registered as an argus settings section, hot-reload, persisted in `config` SQLite table.
- **hera-view** – argus plugin view registered with hotkey `Ctrl+H`. Three-region TUI: left rail + HERA pane (coordinator PTY) + AGENT pane (agent PTY). Three body modes (coordinator / agent / freelance). Comprehensive keyset: `j/k` nav, `Enter` into pane, `Ctrl-→/←` focus ladder, `Ctrl-Q` to rail, `Ctrl-Z` fullscreen, `n` new coordinator, `w` spawn worker, `J` adopt freelancer, `r` rename, `a` archive/unarchive, `P` pin, `s/S` status step, `/` search, `l` listall, `^d` delete, `^r` prune, `^p` open PR, `?` help, `Esc` back to argus. PTY forwarding is verbatim (raw bytes at the WebSocket boundary) for terminal fidelity. Freelance section groups unmanaged argus tasks by repo. Archive section at rail bottom. Live rail refresh within ~100 ms of any DAO write.
- **hera-install** – `setup.sh` manages the full install including opt-in macOS LaunchAgent (starts at login, auto-restarts on crash). `--yes` flag for non-interactive installs.
- **QA regression (BUG-001 through BUG-037)** – comprehensive bug-bash wave covering background, focus, icon alignment, archive coherence, input latency, rail truthfulness, pane rendering, modal behavior, and keyset correctness.

**add-multi-binding** – code shipped on `main` (per-orchestrator binding allows one argus task to hold multiple concurrent hera roles). OpenSpec archive pending (iris publish + task close).

## Locked design decisions (do not re-litigate)

These were settled across the v1 build and v1.0 QA wave and are already reflected in base specs.

- **Six MCP tools, no more in v1:** `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`. Every one takes `cwd` as required.
- **Role-as-identity model.** Orchestrators have no `argus_project` column; roles do, write-once. Roles outlive argus task lifecycles via the bindings table.
- **`hera_new_orchestrator` is the canonical "be an orchestrator" entry point.** `hera_join(kind=coordinator)` was removed; join only accepts worker / freelance.
- **No coord → user routing.** Coordinator senders of `hera_send` without an explicit `to` are rejected.
- **Two-second idle debounce.** Auto-injection fires only when `session.idle` has been active for ≥ 2 seconds. Tunable via `Config.IdleDebounce` and the argus settings UI.
- **Meta mirror is best-effort.** Failure does not undo local state; `meta_mirrored: false` is documented in the spec.
- **Plugin view hotkey is `Ctrl+H`.** Registered as `ctrl+h` in `internal/daemon/run.go`.

## v1.x backlog

These are actively tracked — in rough priority order.

### Re-attach detached agents (top v1.x item)

After a hera daemon restart, or after a worker task is archived and resurected, the agent pane can show a dead session. `Enter` / `Cmd-→` in the rail should revive a dead agent by re-attaching (spawning a fresh argus task in the same role's project and re-binding). Clean session-reattach after a restart is the hardest part: the session-vs-task identity gap (one task can have many sessions; reattach needs to pick the right one) is tracked in `memory/session-vs-task-identity-gap.md`.

### `hera_spawn_worker` MCP verb

A coordinator today spawns a worker via raw `task_create` + the worker self-joins via `hera_join`. This creates a transient "freelancer-then-joins" window and relies on the worker calling `hera_join` itself. The fix: a 7th MCP verb `hera_spawn_worker(cwd, prompt, [role_name], [mission])` that wraps `ops.SpawnWorker` (already exists in `internal/view/ops/spawn_worker.go`, reachable via the `w` rail key) — the worker is born bound, never a freelancer, and its prompt can just call `hera_inbox` to read its mission. This also sidesteps BUG-012.

### Status-step latency

`s` / `S` incur ~0.5 s round-trip to argus. Optimistic-update candidate: stamp the new status locally before the call completes, roll back on error.

### Remove the outer plugin-view border and "Hera" title

The view currently registers with `viewTitle = "Hera"` in `internal/daemon/run.go`. Argus draws a title bar around the plugin view using that title. An empty title is cleaner but argus rejects it with HTTP 400 and crash-loops the daemon (BUG-034 regression risk). Fix requires argus to allow an empty plugin-view title — file a substrate ticket before attempting.

### Coord-metadata auto-summary

The coordinator Details pane (always visible in coordinator mode) live-refreshes agent count, activity, and status. The description/goal/scope auto-summary is a placeholder. Spec: `openspec/specs/hera-view/spec.md` under the coord-details-pane change.

### `hera_spawn_worker` + Coord archive/mark-done MCP verbs

Coordinators currently have to drop to argus's `task_archive` to close out agents. A set of coord-side verbs (`hera_archive_role`, `hera_mark_done`, and an undo path) would make the lifecycle manageable from the coordination layer without an argus workaround.

### BUG-012 – rail doesn't promote a self-joined worker out of Freelance live

A task that calls `hera_join` as a worker after it was created stays in the Freelance section until a full reload (>1 minute, well past the ~2 s argus-state cache window). The new-binding broadcast → rail reclassification path isn't firing. Adding `hera_spawn_worker` sidesteps this for coord-spawned workers; the self-join pattern still needs the live reclassification fix.

### Hera-aware skills

When a sub-agent is spawned via `/fixit` or similar, its prompt must explicitly tell it to call `hera_join`. A hera-aware integration would have skills auto-detect they're in a hera-coordinated project and self-join without prompt plumbing. Three paths (a) project CLAUDE.md snippet, (b) modify individual skills, (c) thin hera-prefixed wrapper skills. Approach (a) is lowest-touch and doesn't touch upstream skills.

### Reliable-messaging redesign (email model)

The minimal doorbell is in 1.0: `DeliveryWatcher` re-nudges idle-submit messages with `read_at` null. The full v1.x design is the email model: stop injecting message bodies into the PTY; inject only a doorbell `"N unread – call hera_inbox"`; the agent pulls the payload via `hera_inbox`; `read_at` is the delivery receipt; re-nudge is idempotent. Depends on the agent contract (on doorbell → call `hera_inbox`), which was added to the hera skill in 1.0.

### Install: Linux/systemd parity + programmatic install/uninstall

`setup.sh` is macOS-only (non-Darwin skip). Adding a `linux-systemd` flow is the next reasonable extension. Programmatic `hera install` / `hera uninstall` CLI subcommands would let scripts manage the LaunchAgent without running `setup.sh` end-to-end.

### Substrate/argus-side open items

These are filed against argus or iris, not hera directly:

- **BUG-009 (argus rerender-kick):** Entering or resizing a task view makes argus stop + `--resume` the session, duplicating pane content. Fix: gate the kick on `needs-input` only. Owner: drn / argus.
- **argus settings-callback auth_header:** argus's TUI callback proxy drops the registered `auth_header`, so every settings-save 401s against hera's `/mcp/settings_save`. Operators can work around by writing to the `config` SQLite table directly. Owner: argus / substrate.
- **iris gaps:** `iris_gh_pr_view` errors on a removed `checks` field; `iris_reload` has a branch-guard on non-default branches; `iris_push` refuses the default branch; no tag-delete verb; `iris_tag` tags stale tip without fetching first. Owner: iris.
- **Host-ops argus plugin:** A future argus plugin for trusted host-side ops (git merge, build, push) from a sandboxed session. Substrate-scope, not hera-scope.

## Dogfood operating model

Once hera is running:

1. **Bootstrap an orchestrator.** From any argus task with MCP access to hera, call:

   ```
   hera_new_orchestrator(cwd=$PWD, name="<project-name>", coordinator_role_name="coord", mission="...")
   ```

   This creates the orchestrator + a coordinator role + a binding tying the calling argus task to that role.

2. **Spawn workers via inbox-dispatch.** The canonical pattern:

   1. **Coord pre-sends the full build prompt** via `hera_send(to="<role-name>", body="<entire worker prompt>")`. Since the role has no live binding yet, hera queues the message per the "messages queued when recipient has no live binding" requirement.
   2. **Coord spawns a thin argus task** via `mcp__argus__task_create` with a minimal prompt: `"You are <role-name>. Call hera_join(kind=worker, orchestrator='<coord's orchestrator>', role_name='<role-name>') then hera_inbox(cwd=$PWD) to read your build instructions."` Argus task body stays small.
   3. **Worker joins**, claims the binding for that role, and reads the queued message via `hera_inbox` – that's the full build prompt. Workers acknowledge via `hera_mark_read` and execute. Status updates flow back via `hera_send` (default-routes to coord).

   Alternatively, press `w` in the hera-view rail to spawn a worker directly from the UI — it creates the argus task, inserts the role, and binds it programmatically without the worker calling `hera_join`.

3. **Inter-agent messages.** `hera_send` from worker default-routes to coordinator. From coordinator, supply explicit `to=<worker-role-name>`.

4. **Resume across incarnations.** When a worker task is archived, its binding ends but the role survives. Re-incarnate by spawning a new argus task in the role's `argus_project`, then `hera_join(cwd)` to claim it.

5. **Coord discipline for spawning workers.** Always pass `meta:hera.role=worker` and `meta:hera.mission="..."` so hera auto-adopts the new task as a worker under the calling coord's orchestrator. Without the meta, the worker has no hera identity until it explicitly `hera_join`s.

## Where everything lives

| What | File |
|---|---|
| What shipped in v1 (coordination) | `openspec/specs/hera-coordination/spec.md` |
| What shipped in v1 (TUI) | `openspec/specs/hera-view/spec.md` |
| What shipped in v1 (substrate-link) | `openspec/specs/hera-substrate-link/spec.md` |
| What shipped in v1 (install) | `openspec/specs/hera-install/spec.md` |
| Install + usage | `README.md` |
| One-shot setup | `setup.sh` |
| Items deferred to v1.1+ with rationale | `FOLLOWUPS.md` |
