# Next — picking up after hera v1

Read this first if you're starting a fresh session on hera. The committed files capture most of the design and history; this page collects the things that lived only in the v1-build conversation, plus the operating model for the dogfood loop.

## Current state (2026-05-25)

- **v1 shipped.** Branch `argus/ludwig-argus-coordinator` was fast-forwarded into `main` at `36a4e64`, then one more commit on top: `f186007 Update stale .gitignore entry`. Local and origin `main` are in sync.
- **Repo renamed** ludwig → hera on disk (`~/Development/Personal/hera`) and on GitHub (`anutron/hera`). The argus-owned branch slug `argus/ludwig-argus-coordinator` and the argus worktree path `~/.argus/worktrees/Ludwig/ludwig-argus-coordinator` stay as-is — they're argus's task slug from when this work spawned.
- **`./bin/hera` is built** and copied to `~/bin/hera` via `setup.sh` (idempotent — re-run any time).
- **`~/.hera/` is set up** with mode 0700, scope token at `api-token` with mode 0600.
- **LaunchAgent install** is now managed by `setup.sh` step 5 — see the `add-launchagent-install` change folder / archived spec. The previous "installed by vanilla Claude outside this repo" plumbing is superseded.
- **OpenSpec base spec** at `openspec/specs/hera-coordination/spec.md` covers everything that shipped. The hera-v1 change folder is archived at `openspec/changes/archive/2026-05-25-hera-v1/`.

## Locked design decisions (do not re-litigate)

These were settled across the v1 build, morning review, spec-audit, and ralph-review passes. They're already reflected in the base spec, but listed here so the next session doesn't waste cycles re-thinking them.

- **Six MCP tools**, no more, no less in v1: `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`. Every one takes `cwd` as a required input.
- **Role-as-identity model.** Orchestrators have no `argus_project` column; roles do, write-once. Roles outlive argus task lifecycles via the bindings table.
- **`hera_new_orchestrator` is the canonical "be an orchestrator" entry point.** `hera_join(kind=coordinator)` was a stopgap that's been removed; the join verb only accepts worker / freelance now.
- **No coord → user routing.** Coordinators talk to the human through their own Claude pane. Coordinator senders of `hera_send` without an explicit `to` are rejected.
- **Two-second idle debounce.** Auto-injection (body + `\n`) fires only when `session.idle` has been the active state for ≥2 seconds. Tunable via `Config.IdleDebounce` but not yet via the settings UI (that's hera-settings).
- **Meta mirror is best-effort.** `meta:hera.role=<kind>` on binding create and `meta:hera.thread_status=<status>` on `hera_status` — failure does not undo the local state. Spec amended to reflect this explicitly.
- **Build order for follow-ups: settings → view.** Settings first (smallest substrate footprint), view second (biggest UX payoff). Install (`add-launchagent-install`) shipped via setup.sh.

## What to build next: `hera-settings`

The first follow-up. Substrate is the settings-section-registration surface (`POST /api/plugins/settings/sections` with `type: "form"`).

**Two fields locked** — do not re-design:

- **`idle_debounce_seconds`** (int, min 0, max 60, default 2): the debounce window before `session.idle` counts as "idle enough to auto-submit." Maps to `Config.IdleDebounce`.
- **`auto_inject_enabled`** (bool, default true): master switch for inject-on-idle. When false, every message lands in busy-buffer mode regardless of idle state. Useful for ops who decide auto-submit is too aggressive.

**Not in scope** for hera-settings (deliberately):

- No active-orchestrators list (that's the plugin view's job)
- No default-orchestrator field (orchestrator names are always project names, user types)
- No user-message-surface field (user-routing was killed)

**Implementation sketch:**

- New package `internal/settings/` registering one form section with argus on daemon startup
- Callback POST handler at `/settings/save` on hera's existing MCP HTTP listener (port 7744)
- On save, persist values to the `config` table in SQLite
- Daemon reads `config` on startup to populate `cfg.IdleDebounce` etc.
- Tool registration code already heartbeats every 5 minutes; reuse that pattern for the settings section.

**Brainstorm prompt for the next session:** "Scaffold the hera-settings OpenSpec change folder. Two fields locked (idle_debounce_seconds + auto_inject_enabled), form section, persist via the existing config table. Build order says this lands before hera-view."

## What needs design BEFORE `hera-view`

The plugin view is the biggest UX payoff but has open design questions that the v1 brainstorm sketched but didn't pin. Don't try to scaffold it without resolving these first.

**Shape (decided, but worth verifying with fresh eyes):**

- Three columns: narrow left rail + two equal-width panes
- Left rail = projects (orchestrators) with their agents (non-coordinator roles) underneath. Coordinator is not navigable; it's implicit per project
- Middle pane = the coordinator's Claude PTY for the selected project
- Right pane = the selected agent's Claude PTY
- Navigation: ctrl-Q (or j/k) moves through agents in the rail; crossing project boundary switches projects
- Focus: cmd/ctrl-← shifts to coord pane; cmd/ctrl-→ shifts to agent pane

**Open questions:**

- **TUI library.** bubbletea, tview, lipgloss, or raw ANSI? Greenfield project; my brainstorm-time lean was bubbletea (most idiomatic for greenfield Go TUIs in 2026 + plays well with the WebSocket-fed ANSI byte stream). Worth re-considering.
- **Embedded-terminal proxy pattern.** Argus exposes `GET /api/tasks/{id}/stream` (SSE) for PTY output and `POST /api/tasks/{id}/input` for keystrokes. The substrate's plugin-view contract is WebSocket-based: hera streams ANSI bytes for its entire surface, argus routes keystrokes back. So hera must: (a) open SSE to each visible task's output, (b) splice the bytes into the right pane region of its own surface, (c) forward keystrokes from the focused pane back to that task's input endpoint. Cost: real, but tractable. Confirm with the coordinator agent before committing.
- **Reserved keys.** Argus reserves only `Esc` (exits the plugin view). Everything else — ctrl-c, tab, function keys, modifier combos — forwards to hera. So hera owns the entire keyboard while focused; can pick its own bindings freely.
- **What appears in each pane other than the embedded PTY?** Per Aaron's brainstorm decision: "each pane is just claude." No custom rendering, no header bar, no feed. Just the live terminal of the bound task. (A richer dashboard surface — decisions, questions, threads — was discussed but deferred until after the embedded-terminal MVP works.)

**Substrate behaviors required (verified by coordinator's smoke harness):**

- Plugin views CRUD: `POST/GET/DELETE /api/plugins/views`
- WebSocket contract on the registered callback URL
- PTY output SSE: `GET /api/tasks/{id}/stream`
- PTY input POST: `POST /api/tasks/{id}/input`
- All work end-to-end against the current argus binary.

**Brainstorm prompt for hera-view:** "Brainstorm hera-view. Shape is roughly settled (rail + two embedded Claude PTYs). Open questions: TUI library choice, PTY proxy implementation pattern, key bindings. Need a design pass before scaffolding the change folder."

## `hera-install` (shipped)

The `add-launchagent-install` change folder added a per-user macOS LaunchAgent install path to `setup.sh` (step 5) plus a `--uninstall-launchagent` flag. Lifecycle is driven by setup.sh — no `hera install` / `hera uninstall` CLI subcommands.

Future work that would justify reopening this:

- **Linux/systemd parity.** The current implementation is macOS-only with a non-Darwin skip. Adding a `linux-systemd` requirement to the `hera-install` capability is the next reasonable extension.
- **Programmatic install verbs.** If users ever need to install/uninstall the LaunchAgent from a script without running setup.sh end-to-end, a `hera install` / `hera uninstall` pair of subcommands would be the right shape.

## Dogfood operating model

Once hera is running:

1. **Bootstrap an orchestrator.** From any argus task with MCP access to hera, call:

   ```
   hera_new_orchestrator(cwd=$PWD, name="<project-name>", coordinator_role_name="coord", mission="...")
   ```

   This creates the orchestrator + a coordinator role + a binding tying the calling argus task to that role.

2. **Spawn workers.** The coordinator agent uses argus's existing `mcp__argus__task_create` with `meta:hera.role=worker` + `meta:hera.mission="..."`. Hera auto-adopts.

3. **Inter-agent messages.** `hera_send` from worker default-routes to coordinator. From coordinator, supply explicit `to=<worker-role-name>`.

4. **Resume across incarnations.** When a worker task is archived, its binding ends but the role survives. Re-incarnate by spawning a new argus task in the role's `argus_project`, then `hera_join(cwd)` to claim it.

## Where everything lives

| What | File |
|---|---|
| What shipped in v1 | `openspec/specs/hera-coordination/spec.md` (base spec) |
| Why the v1 design landed this way | `openspec/changes/archive/2026-05-25-hera-v1/design.md` |
| The OpenSpec implementation plan v1 followed | `openspec/changes/archive/2026-05-25-hera-v1/tasks.md` |
| Day-by-day narrative + morning review + audit pass | `OVERNIGHT_LOG.md` |
| Items deferred to v1.1 with rationale + escalation triggers | `FOLLOWUPS.md` |
| Install + usage | `README.md` |
| One-shot setup | `setup.sh` |

## Suggested first prompt for the next session

Start the new session in `~/Development/Personal/hera` on a fresh branch (e.g., `argus/hera-settings` if argus-managed, or `hera-settings` if you're on a vanilla Claude topic branch):

> Read NEXT.md, OVERNIGHT_LOG.md, and FOLLOWUPS.md in this repo. Then scaffold the OpenSpec change folder for hera-settings. The two fields are locked (idle_debounce_seconds + auto_inject_enabled, form section type). Drive the brainstorm to capture the substrate-registration shape and the save callback, then write the change folder + delta + tasks. After approval, execute.

## Open thread with the argus coordinator agent

The coordinator agent (argus task id `1779491424986011000`) holds substrate context — every PR, every divergence from PLAN.md, smoke-harness results. It's reachable from any argus task via `mcp__argus__task_message_send`. If hera-settings or hera-view hit a substrate gap, send a question there before scoping a workaround.

The last message exchange was the dogfood-kickoff readiness check (acked); the coordinator's next expectation is a check-in when hera's first orchestrator is alive or when we hit a substrate behavior that needs input.
