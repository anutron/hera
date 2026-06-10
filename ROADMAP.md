# Hera roadmap

Reference for what shipped in v1.0, what's next, and what's intentionally deferred.

## Locked design decisions

These were settled across the v1 build and v1.0 QA wave and are already reflected in base specs. Do not re-litigate.

- **Six MCP tools:** `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`. Every one takes `cwd` as required.
- **Role-as-identity model.** Orchestrators have no `argus_project` column; roles do, write-once. Roles outlive argus task lifecycles via the bindings table.
- **`hera_new_orchestrator` is the canonical "be an orchestrator" entry point.** `hera_join(kind=coordinator)` was removed; join only accepts worker / freelance.
- **No coord → user routing.** Coordinator senders of `hera_send` without an explicit `to` are rejected.
- **Two-second idle debounce.** Auto-injection fires only when `session.idle` has been active for ≥ 2 seconds. Tunable via `Config.IdleDebounce` and the argus settings UI.
- **Meta mirror is best-effort.** Failure does not undo local state; `meta_mirrored: false` is documented in the spec.
- **Plugin view hotkey is `Ctrl+H`.** Registered as `ctrl+h` in `internal/daemon/run.go`.

---

## v1.x backlog

Prioritized. Items with "Immediately" escalation are the most actionable next targets.

### New coordinator via `n` has no live Coord pane (BUG-056) — Immediately

Pressing `n`, filling in the modal, and submitting creates the orchestrator entry in hera's DB and appears in the rail. But the Coord pane shows "(no coord selected)" — no live binding from a running argus task to the coordinator role. Root cause: `n` spawns an argus task whose prompt is supposed to call `hera_new_orchestrator`, but if the task doesn't call it (wrong prompt, missing MCP tool access, task exits early), the binding is never established.

**Fix direction:** Make `n` create the coordinator binding programmatically in hera — same pattern as `hera_spawn_worker`. Born-bound, not required to call `hera_new_orchestrator` itself.

### New-coordinator modal: Enter doesn't submit, submit button hidden (BUG-055) — Immediately

The `n` modal shows Name, Project, Branch, Backend, and Prompt fields. The submit button is cut off below the visible modal area. Pressing Enter in any field does nothing; the only way to submit is to Tab past Prompt until focus reaches the hidden button.

**Fix:** (1) Make Enter in any field submit the form. (2) Ensure the submit button is always visible within modal bounds.

### `^d` delete crashes with error modal when worktree already removed (BUG-054) — Immediately

`^d` on a role whose argus worktree has already been cleaned up shows an error modal: `ops.DeleteRole: worktree remove: git worktree remove <path>: exit status 128`. The role should be deleted from hera's DB regardless of whether the git worktree still exists.

**Fix direction:** In `ops.DeleteRole`, check if the worktree path exists and has a `.git` file before running `git worktree remove`. If already gone, skip that step and proceed with DB deletion.

### Cursor offset wrong after reattach until resize (BUG-053) — Immediately

After Enter on a dead agent row triggers reattach, the new session's cursor appears at the wrong position until the terminal is resized. Same root cause as BUG-049: the reattached PTY starts at the default 80×24 before the pane gets a real resize event.

**Fix direction:** Apply the same `QueueUpdateDraw` trigger in the pane-rebind path — when a new PTY proxy connection is bound to an existing `pinnedTerminalPane`, schedule a forced redraw.

### Cmd-→ into dead pane doesn't trigger reattach (BUG-052) — Immediately

`Cmd-→` navigates into a dead agent pane without triggering the restart. `Enter` on the rail triggers the restart correctly.

**Fix direction:** When the focus ladder lands on a dead pane (no live PTY binding), trigger the same restart path that Enter uses.

### Mouse wheel doesn't scroll coord (HERA) pane (BUG-051) — Immediately

Mouse wheel scrolls the agent pane correctly but not the coord (HERA) pane.

**Fix direction:** In `internal/view/app.go`, check the `RouteWheel` hit-test — confirm the coord pane rect is included alongside the agent pane rect.

### Coord should roll finished workers to `in_review` (BUG-050) — soon

When a worker finishes and reports back to the coord, the rail doesn't reflect that the work is done. The argus task stays in its current status; the rail gets cluttered with finished-but-not-acknowledged workers.

**Design decision:** When a worker goes idle after sending a message to coord, hera should automatically step its hera role status to `in_review` — signaling "done, awaiting coord acknowledgement." Open question: trigger via (a) worker explicitly calls `hera_status(status=in_review)`, (b) hera detects idle-after-send, or (c) coord steps a worker's status remotely.

### hera-view blank on first entry until resize (BUG-049) — soon

Opening hera-view renders the pane layout at wrong initial dimensions. Resizing the terminal forces a correct redraw. Root cause: the `argus pluginViewportSize 13x8 race` — argus sends a small initial viewport size before real terminal dimensions arrive.

**Fix direction:** On receiving the first resize event after plugin-view open, force a full re-render even if dimensions haven't changed. Alternatively, defer the first render until a non-trivial viewport size is confirmed.

### Coord archive / mark-done / y-n argus prompt (BUG-048) — soon

Hera role status (`s/S`) writes to `role_status` and mirrors to `task_meta` but does NOT call argus's task lifecycle API. A worker stepped to "done" in hera stays fully live in argus.

**Design decision (2026-06-06):** Hera must NEVER automatically mark an argus task `:checked:`. When the user presses `s` to step a worker to `done`, show a y/n confirmation: "Also mark :checked: in argus? (y/n)". `hera_status(status=done)` via MCP — no prompt, no argus touch (coordinator scripts unaffected). The coord-side `hera_archive_role` / `hera_mark_done` MCP verbs remain deferred — the y/n prompt covers the interactive case.

### `hera_spawn_worker` argus task title uses preamble, not role name (BUG-047) — soon

`hera_spawn_worker` constructs the argus task prompt with the preamble as the first line, so argus derives the task title from that. All workers get near-identical worktree paths (`argus/You-are-a-worker-agent-under`).

**Fix direction:** Pass `role_name` (or `<orchestrator>/<role_name>`) as the argus task `name` field. The hera preamble belongs in the body, not as the task title.

### Re-attach detached agents

After a daemon restart, or after a worker task is archived and resurrected, the agent pane can show a dead session. `Enter` / `Cmd-→` in the rail should revive a dead agent by re-attaching. Session-vs-task identity gap: one task can have many sessions; reattach needs to pick the right one. Tracked in `memory/session-vs-task-identity-gap.md`.

### `hera_spawn_worker` MCP verb

A coordinator today spawns a worker via raw `task_create` + the worker self-joins via `hera_join`. A 7th MCP verb `hera_spawn_worker(cwd, prompt, [role_name])` would wrap `ops.SpawnWorker` (already exists in `internal/view/ops/spawn_worker.go`, reachable via the `w` rail key) — the worker is born bound, never a freelancer, and its prompt can just call `hera_inbox`. Also sidesteps BUG-012 and BUG-047.

### BUG-012 – rail doesn't promote self-joined worker out of Freelance live

A task that calls `hera_join` as a worker after it was created gets a live binding in the DB, but the rail keeps rendering it in the Freelance section (>1 minute) until a full reload. The new-binding broadcast → rail reclassification path isn't firing. Adding `hera_spawn_worker` sidesteps this for coord-spawned workers; the self-join pattern still needs the fix.

### Status-step latency

`s` / `S` incur ~0.5 s round-trip to argus. Optimistic-update candidate: stamp the new status locally before the call completes, roll back on error.

### Coord-metadata auto-summary

The Details pane (coordinator mode) live-refreshes agent count, activity, and status. The description/goal/scope auto-summary is a placeholder. Spec: `openspec/specs/hera-view/spec.md` under the coord-details-pane change.

### Hera-aware skills

Workers spawned via `/fixit` or similar need hand-supplied `hera_join` instructions. Three paths: (a) project CLAUDE.md snippet, (b) modify individual skills, (c) hera-prefixed wrapper skills. Approach (a) is lowest-touch and doesn't touch upstream skills.

**Escalate when:** The repeated "remember to hera_join" instruction becomes a noticeable overhead.

### Reliable-messaging redesign (email model)

The minimal doorbell (re-nudge on unread `read_at`) shipped in 1.0. The full v1.x design stops injecting message bodies into the PTY and instead injects only a doorbell that triggers `hera_inbox`. Cleaner, idempotent, re-nudge-safe. Depends on the agent contract (on doorbell → call `hera_inbox`), which was added to the hera skill in 1.0.

**Escalate when:** Body-injection causes double-delivery or ordering issues under concurrent sends.

### Remove the outer plugin-view border and "Hera" title

`viewTitle = "Hera"` in `internal/daemon/run.go` causes argus to draw a title bar around the plugin view. Empty title = HTTP 400 + crash-loop (BUG-034 regression risk). Requires an argus-side fix before attempting.

**Escalate when:** argus is updated to allow an empty plugin-view title.

### Install: Linux/systemd parity + programmatic install/uninstall

`setup.sh` is macOS-only (non-Darwin skip). Linux/systemd flow and `hera install` / `hera uninstall` CLI subcommands are the natural next extensions.

### bg-agent session recovery

When hera's BounceRecoverer or the reattach restart path resumes a session, it can launch it as a Claude Code background agent (detached). Claude refuses a second concurrent attach, so hera's pane shows "Session <id> is currently running as a background agent (bg). Use `claude agents` to attach, or --fork-session" instead of the live conversation. The coord/worker is alive but unviewable.

- **Key fact:** Claude persists the transcript to disk (`~/.claude/projects/.../<id>.jsonl`), so killing the bg agent process is non-destructive – `claude --resume <id>` reconstitutes it.
- **Design (approved):** argus `POST /api/tasks/{id}/restart` detects a session held by a live bg agent → terminates that process → `claude --resume` in foreground PTY mode. Hera detects the bg banner (fixed string) → treats it as a dead-pane → triggers the existing REATTACHING reattach flow.
- **Also investigate:** whether BounceRecoverer/restart is spawning bg agents in the first place – fixing the spawn flag prevents the orphan.

### Coord plan view (`hera_set_plan` → `meta:hera.plan`)

Coordinators generate stage-status tables ad-hoc in chat; they vanish. Give coords a low-token way to publish a persistent structured plan rendered live in hera-view.

- **Design (approved, option #2 of 3):** New MCP verb `hera_set_plan(cwd, plan_json)` writes a compact JSON blob to the coord's argus task metadata as `meta:hera.plan`. Coord stamps once at session start, updates per stage transition.
- **Render:** the hera Details pane reads `meta:hera.plan` and renders a stage table / dependency view.
- **Plan shape:** `{"stages":[{"id":0,"name":"...","status":"done|in_progress|waiting","notes":"...","depends":[1]}]}`.

### Minimum splash duration for REATTACHING/LAUNCH screens

When reattach/launch is very fast the splash flickers. Hold it a minimum of 1 second.

### Safe bounce mechanism

Before a daemon restart (laptop close, `iris_reload`, crash), snapshot active sessions. After restart, offer to re-inject a resume signal into each previously-active session instead of the user manually typing "resume" into each.

### Pinned section: deduplicate coordinator header

When two agents under the same coordinator are pinned, the coord name repeats. Group them under one header.

### Auto-report up the tree

When a worker reaches a decision point or pauses/stops, it should automatically inform its coord via `hera_send`.

- **Approach (a):** a hera-skill change telling workers to send a status before every `AskUserQuestion` and final summary.
- **Approach (b):** a Claude Code Stop hook in `settings.json` that calls a `hera-report` CLI script using `ARGUS_TASK_ID` → binding lookup → `hera_send`.

### Reattach still requires a keystroke (accepted v1.x)

After 9+ fix attempts, pressing Enter on a dead agent shows the splash + reattaches correctly, but the splash trigger still needs one keystroke. Root cause: tview event-loop draw ordering – focus.ToRAIL's draw races the splash draw. Accepted as good-enough for 1.0; revisit if it becomes annoying.

- **Cleanest remaining option:** don't reset focus at all during reattach; hide the cursor while `reattaching=true` instead.

---

## Architecture — deferred to v1.1+

### Atomic role+binding insert across DAOs

`Roles.Create` and `Bindings.Create` are two separate `sqldb.Exec` calls. If `Bindings.Create` fails after `Roles.Create` succeeds, the orphan role row persists. The fix requires a `db.WithTx` helper. Practical risk is very low in a single-user, single-tenant SQLite system.

**Escalate when:** We see an orphaned-role bug in practice, or we move to a multi-process / multi-host topology.

### Cursor advancement on handler failure

`events.Subscriber.Run` advances `EventCursor` after every event regardless of handler outcome. A transient adoption error silently consumes the event; missed work is not retried. Fixing this requires changing the `Handler` interface to return an error AND deciding the cursor-advancement policy.

**Escalate when:** Hera misses adoptions in production because of transient argus errors, or we want more autonomous recovery.

### ~25 low-priority test-alignment polish items

The spec-audit flagged that several tests exercise the spec-described path but don't assert the spec's exact wording. 17 in `internal/mcp`, 4 in `internal/db`, 3 in `internal/argus`, 2 in `internal/wiring`. High-severity gaps have been fixed; the remainder is incremental tightening.

**Escalate when:** A new behavior lands that touches one of these tests; tighten the assertion as part of that change.

---

## Substrate / argus open items

These belong to argus or iris, not hera.

### BUG-009 – argus rerender-kick

Entering or resizing a task's pane makes argus stop the session and restart it with `--resume`, causing the PTY to contain the session twice. Hera faithfully renders the two-session PTY — the defect is argus-side.

**Fix direction (argus):** Gate the rerender kick on `needs-input` only; don't kick a session that doesn't need re-prompting.

### argus settings-callback drops auth_header

When argus's TUI submits a settings form, its callback proxy POSTs the values to the section's registered `callback_url` with no `Authorization` header — even though the registration carries an `auth_header` field. Every settings-save form submit from the argus UI 401s against hera's `/mcp/settings_save`.

**Workaround:** Operators can write directly to `~/.hera/state.sqlite` `config` table and restart hera. Owner: argus.

### iris gaps

- **Stale-tip tag.** `iris_tag` tags the local `origin/main` without fetching first; can tag a stale pre-merge commit.
- **No tag-delete or force-tag verb.** A bad tag can't be corrected via iris.
- `iris_gh_pr_view` errors on a removed `checks` field.
- `iris_reload` has a branch-guard on non-default branches.
- `iris_push` refuses the default branch.

Owner: iris.

### Host-ops argus plugin

A future argus plugin for trusted host-side ops (git merge, build, push) from a sandboxed session. Substrate-scope, not hera-scope.
