**Proposal:** see `proposal.md`.

## Context

The hera daemon already runs as a long-lived background process under launchd, owns an SQLite DB of orchestrators / roles / bindings / messages / config, connects to argus via HTTP + SSE, and exposes MCP tools on `127.0.0.1:7744`. What it does not yet do is render — every interaction with hera today is through MCP calls made from inside argus tasks. The operator's mental model of "who is doing what right now" has to be reconstructed every time by sending `hera_inbox` and `hera_status` calls.

Argus already has a plugin-view substrate (verified by reading argus source today): plugins register at `POST /api/plugins/views` with a `callback_url`; argus dials that URL as a WebSocket on hotkey press; the plugin owns the entire rendered surface and the entire keyboard except `Esc`. That gives us a clean place to put the operator UI without competing with argus's own task list or other plugins.

The shape — three columns + chrome — was settled during the v1 brainstorm and confirmed today. The TUI library (`tview`/`tcell`/`lipgloss`), the PTY-proxy approach (pre-load everything, snapshot + SSE per task), the focus model (three states + arrow ladder), and the six rail-operations (`n`/`r`/`^d`/`a`/`l`/`?`) were chosen in this brainstorm.

## Goals / Non-Goals

**Goals:**

- Make every orchestrator and every agent simultaneously visible and instantly switchable from a single screen.
- Match argus's rail-traversal feel: zero perceptible latency when moving across agents or across project boundaries.
- Surface project + agent lifecycle (create, rename, archive, delete, resurrect) as rail-level operations with discoverable bottom-bar shortcuts.
- Preserve role-as-identity: archiving or deleting a worktree never destroys the role's stored mission / constraints / `argus_project` — those survive for resurrect.
- Live-update the rail when bindings change (new worker adopted, task archived externally) without operator action.
- Keep the implementation in-process with the existing hera daemon — no new binaries, no new daemons.

**Non-Goals:**

- A richer dashboard surface (decision feed, question banner, conversation threads). Today's PTY panes are byte-for-byte mirrors and nothing more. Deferred to a post-1.0 capability.
- Custom rendering inside the PTY panes (syntax highlighting hera-side, message-summarization overlays). The panes are dumb passthroughs.
- A web UI or any non-TUI surface. hera-view exists only inside argus.
- Multi-operator concurrent access. Single-instance assumed; if argus opens a second WebSocket the older one is closed.
- Multi-operator-aware resize coordination. Hera resizes source PTYs to its pane allocation (see D7); a second operator viewing the same task in argus's native view will compete on PTY size (last-writer-wins). Acceptable under v1's single-operator-single-host assumption.
- Replicating argus's full task management surface. We do project + agent lifecycle for hera-managed tasks only; other argus tasks remain invisible to hera-view.

## Decisions

### D1 — TUI library: tview + tcell + lipgloss

Matches argus's own stack. The critical reuse target is argus's `streampane.StreamPane` widget, which renders an ANSI byte stream from a channel into a tview Box region with proper cell-based screen handling. That widget is exactly what each PTY pane needs to be. Reusing the pattern (and likely vendoring or re-implementing the same code shape) cuts the most uncertain part of the implementation from "design a virtual terminal grid" to "wire up channels into a known-good widget."

Alternatives: bubbletea is more 2026-idiomatic for greenfield Go TUIs and has cleaner state-management via Update/View, but it has no built-in equivalent of StreamPane — we'd reimplement the parse-ANSI-into-cell-grid logic ourselves (~150-300 lines, with edge cases). Raw ANSI was rejected as gratuitous DIY.

### D2 — Plugin view server lives in the hera daemon process

The daemon already owns the DB, argus connection, and MCP listener. Adding a `/view` WebSocket route to the existing `:7744` listener is a natural extension. The view application starts when a WebSocket connection is accepted and ends when it closes. State across reconnections is preserved by the daemon (DB, SSE subscriptions, PTY ring buffers); the view application itself is stateless across WebSocket sessions.

Alternative: a separate `hera-view` binary that the daemon spawns on demand. Rejected — extra process management, slower startup, no clear benefit.

### D3 — Pre-load all task PTY state at daemon startup

Every orchestrator's every role (including archived ones once `listall` is toggled) has a long-lived snapshot + SSE pipeline. On daemon startup the view subsystem walks live bindings, fetches `GET /api/tasks/{id}/output` (snapshot), and opens `GET /api/tasks/{id}/stream?since=<X-Output-Total>` (SSE) for each. Bytes flow into a per-task in-memory ring buffer (cap ~256 KiB to match argus's own ring). Switching agents in the rail is just "the right pane's StreamPane subscribes to a different ring."

Memory ceiling for a typical session: 5-15 tasks × 256 KiB = 1.3–3.8 MB. Network: 5-15 idle SSE connections to localhost. Trivial cost.

Alternative: on-demand subscription (only currently-visible agent gets an SSE). Rejected — introduces a perceptible "loading…" moment on every rail navigation, which would break the argus-parity feel that's the whole point.

### D4 — Three-state focus model with arrow-ladder + ctrl-Q escape

Focus is one of `RAIL`, `COORD`, or `AGENT`. The bottom bar always reflects which.

| From → To       | Key       |
|-----------------|-----------|
| RAIL → COORD    | Cmd/Ctrl-→ |
| COORD → AGENT   | Cmd/Ctrl-→ |
| AGENT → COORD   | Cmd/Ctrl-← |
| COORD → RAIL    | Cmd/Ctrl-← |
| RAIL → AGENT    | Enter (skips coord) |
| any → RAIL      | Ctrl-Q |
| in RAIL: navigate up/down | `j`, `k`, `↑`, `↓` |
| in COORD/AGENT pane: type | (any key not bound above forwards to the bound task's PTY input endpoint) |

Focus indicator: a colored border (lipgloss-styled tview Box border) around the focused element. tview's native `SetBorderColor` driven by a focus tracker.

Rail navigation rolls across project boundaries seamlessly — if you `j` past the last agent of project A, you land on the first agent of project B. Each rail move updates both panes: middle = the new agent's project's coord PTY, right = the new agent's PTY. Both updates are instant because of D3.

Mutation operations (`n`/`r`/`^d`/`a`/`l`) are **RAIL-focus-only** to keep pane focus reserved for unambiguous PTY typing. From a pane, the operator hits `Ctrl-Q` to return to rail before acting. This trades one keystroke for zero chance of `r` typing-into-coord accidentally firing a rename modal.

Help (`?`) is RAIL-focus-only too for the same reason; from a pane, `?` types a literal `?` into the PTY.

### D5 — Rail operations, in detail

**`n` new project**

1. Modal: prompt for orchestrator name (required) and coord mission (optional). Validate name is unique among non-archived orchestrators.
2. Hera spawns an argus task via `POST /api/tasks` (argus REST API) in a project named after the orchestrator (creating the argus project if necessary), with a generated prompt that calls `hera_new_orchestrator(cwd=$PWD, name=<chosen>, coord_role_name="coord", mission=<chosen>)` as the first action.
3. When the task starts and the bootstrap call lands, hera's existing handler creates the orchestrator + role + binding atomically.
4. The view's rail subscription (D6) sees the new orchestrator + role + binding rows and refreshes; the new project appears with its coord live.

**`r` rename**

Modal: prompt for new name; validate uniqueness (orchestrators globally; roles within their orchestrator). On confirm, `UPDATE orchestrators SET name=? WHERE id=?` or `UPDATE roles SET name=? WHERE id=?` in hera's DB. No argus side effects (argus task names are independent). New name appears in the rail on next refresh tick.

**`^d` del**

Confirm modal ("Delete <name> and its worktree?"). On confirm:

- For a role: end the role's live binding (if any) with `end_reason=user_deleted`, mark `archived_at=now()` on the role, then `git worktree remove --force <worktree_path>` against the role's binding's `worktree_path`. The hera daemon runs this directly — privileged, not sandboxed. The role row persists for archive-visibility but won't appear in the active rail.
- For an orchestrator (coord deletion): same as above, but cascades — end every live binding under the orchestrator, archive every role, remove every worktree, archive the orchestrator. Confirmation modal lists what's about to disappear.

If the worktree path is empty or the directory doesn't exist, the `git worktree remove` step is a soft no-op (log and continue).

**`a` archive**

Toggle. For a role: `UPDATE roles SET archived_at=COALESCE(?, NULL)` and call `mcp__argus__task_archive` (or its HTTP equivalent: `POST /api/tasks/{id}/archive`) on the binding's argus_task_id. Worktree stays. For an orchestrator: same toggle applied recursively (orchestrator archive ⇒ every role archived; orchestrator unarchive does NOT auto-unarchive roles — those have to be unarchived individually).

Archived items move to a collapsible "Archive" section at the bottom of the rail. The section is hidden by default; `l` toggles it.

**`l` listall**

Pure view-state toggle: show / hide the Archive section. No DB writes. The toggle's persistence is in-memory for the session only — next view session starts hidden.

When Archive is visible and the operator highlights an archived coord (orchestrator) and presses `Enter`, hera **resurrects** the role:

1. Modal: confirm "Resurrect <project>?"
2. Hera unarchives the orchestrator + the coord role (`archived_at = NULL`).
3. Hera spawns a new argus task in the role's `argus_project` (which is the per-role write-once field captured at first creation) with a prompt that calls `hera_join(cwd=$PWD)` to claim the dormant role's binding-slot. The new task's worktree is brand new; the role's stored mission and constraints are inherited.
4. View refreshes; the orchestrator moves back to the active section with a fresh binding.

**`?` help**

Modal overlay (tview Modal primitive) showing the full bindings reference grouped by focus state. Closed by `Esc`-equivalent (since real Esc is argus-reserved, use `q` to close help). Static content; no DB read.

### D6 — Dynamic rail updates from in-process DB events

The view subsystem subscribes to a new lightweight in-process broadcaster the daemon exposes — a `chan db.BindingChangedEvent` (or similar) fed by the existing DAOs whenever bindings, roles, or orchestrators are inserted / updated. The rail refreshes its tree on each event (debounced ~100ms to coalesce bursts).

This is internal-only — no new external API surface. The DAOs already do every relevant write; we wrap them with a tiny pub/sub. This is also useful beyond hera-view: future telemetry / health-check work can subscribe to the same stream.

Alternative: poll the DB on a timer. Rejected — laggy UX (visible delay when a worker is adopted), wasted CPU when idle.

### D7 — Resize policy

When argus sends a `{type:"resize", cols, rows}` envelope, the view's tview Application receives a screen-size change. The tview Flex layout recalculates the rail width (fixed ~22 chars), top bar (1 row), bottom bar (1 row), and divides the remaining horizontal space evenly between coord pane and agent pane. Each pane's terminalpane re-renders its current buffer at the new size.

**For each task bound to a coord or agent pane, the daemon ALSO issues `POST /api/tasks/{id}/size` with the pane's new allocation** so the source PTY emits content at the pane's column count rather than at whatever width argus originally allocated. This was reversed from the original design after live testing showed the no-resize policy produces unusable artifacts: a worker PTY at 189 cols rendered into a 145-col hera pane wraps cursor-positioned content at the wrong column, producing long horizontal-bar runs and vertical text fragments along the right edge.

Concerns that originally argued against resizing — and why they're addressed:

1. *"Resizing under another operator looking at the same task in argus's native view is hostile."* — Argus's native view ALSO resizes the PTY to match the visible pane. The two views compete for size, last-writer-wins. In the single-operator-single-host model assumed for v1, this is no worse than what argus already does.
2. *"Forcing a re-wrap on every hera-view resize causes flicker."* — Argus's resize endpoint includes a `maybeKickRerender` predicate gated on (delta >= 15 cols, agent IsIdle, once-per-cols cache). Repeated resizes to the same width are no-ops; mid-tool-call agents are not interrupted; first resize at a new width transparently re-emits scrollback via `--session-id`. The flicker concern is mitigated by argus's existing predicate.

Hera's resize call is debounced inside the view layer by a `lastDesired` cache per pane plus a `ProxyManager` dedupe per task, so a flapping layout doesn't generate a burst of HTTP traffic.

### D8 — Plugin view registration + heartbeat

On daemon startup, after the existing MCP-tool registration, the daemon also registers the plugin view via `POST /api/plugins/views {title:"Hera", hotkey:"<TBD>", callback_url:"ws://127.0.0.1:7744/view"}`. The chosen hotkey is `Ctrl-H` (default suggestion; the registration accepts argus's standard hotkey grammar). The registration response includes an `id` we cache.

Re-registration every 5 minutes mirrors the existing MCP-tool heartbeat — same Ticker, same registrar shape. On daemon shutdown, the view registration is deleted via `DELETE /api/plugins/views/{id}`.

### D9 — WebSocket server + custom `tcell.Screen`

The daemon's existing HTTP listener (`:7744`) gets a new route `GET /view` that accepts a WebSocket upgrade. Per connection:

1. Accept the upgrade; spawn a per-connection goroutine.
2. Construct a custom `tcell.Screen` whose `Show()` / `Sync()` methods emit a single full-surface ANSI byte buffer over the WebSocket as a binary frame.
3. Receive WebSocket binary frames → translate to `tcell.EventKey` events delivered to the Screen's event queue.
4. Receive WebSocket text frames → JSON-decode the control envelope → resize/focus/blur the tview Application.
5. Construct a tview Application bound to the custom Screen; build the layout; run the event loop.
6. On WebSocket close: stop the tview Application, terminate the goroutine.

Custom `tcell.Screen` implementation: tcell's `Screen` interface is well-defined; a WebSocket-backed implementation is the same shape as the SSH-based ones that exist in the Charmbracelet ecosystem (`wish`, etc.). Plan to crib from those.

Auth: argus already terminates plugin-view-callback auth at its dial layer — the WebSocket arrives with whatever credential argus has registered. Hera's server doesn't need to re-auth at the WebSocket level for v1.0 (single-operator, single-host assumption). The settings-callback `auth_header` substrate bug filed in FOLLOWUPS does NOT apply here; that's a separate code path in argus's TUI form proxy.

### D10 — Data-model changes (migration `0003_archived_at.sql`)

```sql
ALTER TABLE orchestrators ADD COLUMN archived_at TEXT;  -- nullable RFC3339
ALTER TABLE roles         ADD COLUMN archived_at TEXT;  -- nullable RFC3339

CREATE INDEX orchestrators_by_archived ON orchestrators(archived_at);
CREATE INDEX roles_by_archived         ON roles(archived_at);
```

Two new DAO methods on `Orchestrators` and `Roles`: `Archive(id, at)`, `Unarchive(id)`, plus a `Rename(id, newName)`. Existing `List*` calls add an `IncludeArchived bool` parameter (default false) so the bulk of existing code paths remain "active orchestrators / roles only."

### D11 — Freelance agents: global rail section + dual-mode body layout

A **freelancer** is a live argus task hera has never bound — a vanilla agent created directly in argus that makes no hera calls. The original framing modelled freelance as a hera role kind (`hera_join(..., kind="freelance")`); dogfooding clarified the real goal: the operator wants hera's rail to surface **all** active argus agents — including ones hera doesn't manage — so they never leave hera to notice an agent needs attention. So freelancers are sourced from argus reality, not hera's role graph:

**Rail.** The freelancer set is computed from the argus state cache: every non-archived argus task whose id is absent from the hera bindings table (`Bindings.AllArgusTaskIDs`, live or ended) is a freelancer. These render in a "Freelance" section below all project rows and above the Archive separator, introduced by a "Freelance" separator shown only when ≥1 freelancer exists. **Within the section, freelancers are grouped by argus project/repo** — the same way argus's own rail groups tasks — under per-repo headers (chevron + name + live count) sorted by project name. Repo groups default to **expanded** (surfacing freelancers is the whole point) and each toggles on Space independently. Each freelance row renders argus-reported state via the same icon rules as managed rows and shows argus's own elapsed string. Archived argus tasks stay out of the section unless `l` reveals the Archive view.

Implementation: `railList` gains a `freelance []*freelanceProject` collection (each `{Project, Tasks []*roleEntry}`), a `freelanceCollapsed map[string]bool` keyed by project (default expanded), and `railRowFreelanceSep` / `railRowFreelanceProj` row kinds. The `ArgusStateCache` retains full per-task metadata (name/project/elapsed) and exposes `List()`; `managerPaneSource` surfaces it via the optional `FreelanceProvider` capability. `App.buildFreelance` partitions the live task list by project, excluding hera-bound and archived tasks; `populateRail` feeds it via `SetFreelance`. `buildRows` emits the separator + per-repo headers + task rows after active orchestrators; the Stage-I dynamic refresh rebuilds it like any other rail mutation. Selecting a freelance row drives the same dual-mode + focus behavior below as the original design intended.

**Body layout — two modes.** The selection's kind picks the column composition:

- **Project mode** (coordinator or worker row selected): the unchanged three-column `rail + coord + agent` body.
- **Freelance mode** (freelance row selected): a two-element `rail + agent` body where the agent pane takes the entire remaining width. The coord pane is removed from the Flex (not merely hidden), its proxy subscription/bridge is torn down, and `CoordTaskID()` returns `""` so no keystroke can misroute to a coordinator.

`refreshBody` chooses the composition from the current selection; `applyRailSelection` switches modes and binds the full-width agent pane to the freelance role's argus task. This is a deliberate UX statement: a freelance agent has no coordinator pairing worth showing, so it gets the whole canvas.

**Focus.** The three-state machine learns a `coordPresent` flag set by the App on mode switch. In freelance mode the COORD state is skipped: Cmd/Ctrl-→ from RAIL advances straight to AGENT, Cmd/Ctrl-← from AGENT retreats straight to RAIL, and Enter from RAIL lands on AGENT as before. The bottom bar drops the `Ctrl-→ coord` hint in freelance mode.

Alternative considered: keep freelance nested under their orchestrator and only change the layout on selection. Rejected — the operator's stated mental model is "projects, then a separate pool of freelancers"; leaving them nested under a coordinator they don't report to is the confusion this change removes.

## Risks / Trade-offs

- **`git worktree remove --force` from the daemon is potentially destructive.** → Confirm modal lists everything that will disappear and requires explicit `y` keystroke. The destructive operation is gated behind RAIL focus + the confirm modal — no accidental single-keystroke deletion. We log every `git worktree remove` invocation with the worktree path. If a future bug attempts a wrong-path delete, the log gives audit trail.

- **Pre-loading SSE for every task creates a lot of long-lived connections.** → For typical session sizes (5-15 tasks), this is fine — connections are localhost, idle, and well under any practical limit. If the operator runs a session with 50+ tasks, we'd see real cost; but argus's task model isn't designed for that scale either, so we'd be hitting argus's limits first.

- **Custom `tcell.Screen` backed by WebSocket frames is the highest-risk piece of code in this change.** → Plan to crib from charm/wish's SSH-backed Screen which has the same shape. Add a unit test that runs the tview app against a fake WebSocket and asserts the output bytes are well-formed ANSI. If we hit unsolvable edge cases, fall back to a virtual PTY pair + bridge approach (more layers, more debuggable).

- **Hera resizes source PTYs; argus's native view may resize them too.** → In single-operator-single-host (v1's assumption), last-writer-wins is acceptable: whichever view was most recently focused dictates the PTY size, and argus's `maybeKickRerender` predicate caches the last size per task so back-and-forth produces at most one transparent `--session-id` rerender per focus switch. If multi-operator becomes a real scenario, the follow-up is to add a UI affordance (hera signals to the operator that it is driving the PTY size) and possibly a `prefer-passive` mode that disables the resize call.

- **`n new` requires hera to call argus's REST `POST /api/tasks` directly.** → Hera already has an argus REST client (`internal/argus/client.go`); add the `CreateTask` method. The auth token is hera's existing scope token. If argus's `task_create` HTTP route requires a different shape than the MCP tool, file a substrate-clarity ticket; otherwise this is a thin wrapper.

- **Rename of a role doesn't propagate to argus task names.** → Intentional. Argus task names are about the argus-side identity (worktree slug, branch name); hera role names are about the operator's mental model. They are independent and that's correct. Document in the help modal.

- **Concurrent WebSocket connections (operator opens hera-view in argus twice somehow).** → Last writer wins: when a new connection arrives, close the older one. Argus's plugin-view UI is single-instance so this should never happen in practice, but defensive cleanup avoids zombie sessions.

## Migration Plan

This is greenfield (no existing hera-view to upgrade), so the only migration is the `archived_at` SQLite migration which is additive + nullable. Existing rows get NULL `archived_at`; existing queries unchanged. No data backfill needed. Migration runs at daemon startup via the existing `internal/db/schema.go` migrator.

Rollback: revert the daemon binary. Migration `0003_archived_at.sql` adds nullable columns — the prior daemon ignores them. No rollback SQL required.

## Open Questions

These are surfaced for Plannotator review. Each has a tentative default I'd ship with unless flagged.

- **Default hotkey for the plugin view.** Tentative: `Ctrl-H`. Risk: collides with bash's Ctrl-H (backspace synonym) when the operator is editing argus's own task list. Alternative: `Ctrl-X H`, a chord. Or pick something else entirely if argus has a convention.
- **Help modal close key.** `Esc` is reserved by argus (closes the whole plugin view). Tentative: `q`. Alternative: `?` again as a toggle, or `Enter`.
- **Help modal width.** Tentative: fixed at 80×24 inside the surface, centered. Plannotator-able if it should be percentage-based instead.
- **What `HERA` lives in the top bar.** Tentative for v1: literal text `HERA` left-aligned, current focused-thing path right-aligned (e.g., `HERA                    hera-1.0 / coord`). Alternative: just `HERA`, period.
- **Bottom-bar wrapping when many shortcuts.** RAIL focus has 10 shortcuts; at 80 cols they may not fit. Tentative: truncate-with-ellipsis. Alternative: two-line bottom bar.
- **`?` help in pane modes.** Today decided: pane mode types literal `?` into the PTY (no help). But the operator might still want to see help from inside a pane. Alternative: ctrl-`?` from any mode shows help. Cheap addition.
- **Initial selection on first open.** Tentative: first non-archived agent of the oldest non-archived orchestrator. If no agents exist anywhere: rail focus, no panes rendered (placeholder text in panes).
- **Confirm-delete UX.** Tentative: a tview Modal asking "Delete X? (y/N)" — `y` confirms, anything else cancels. Alternative: type the project/role name to confirm (heavy but safer for cascade-delete-of-coord).

## Alternatives considered

Major alternatives are listed in each Decision above. Two architecture-level alternatives worth recording separately:

- **A: hera-view as a separate binary instead of in-daemon.** Rejected. Requires IPC (or duplicating the SQLite + argus connections), introduces process-management complexity, and offers no clear win — the daemon already runs and already has everything we need.
- **B: hera-view as a web UI served at `:7744/view`.** Rejected for v1. The whole substrate story is argus-as-host; introducing a separate browser context contradicts that. A web UI may make sense post-1.0 as a sibling surface (the "hera-dashboard" hypothetical) but it's not the same change.

## Discovery findings

Spent today reading argus source to verify the plugin-view contract. Key findings, now load-bearing for this design:

- `argus/internal/api/plugin_views.go`: registration is `POST /api/plugins/views {title,hotkey,callback_url}`; auth-header derived from registering token. Scope-token-registered views are visible only to that scope's plugins. Idempotency: re-registering the same (scope, title) is a 409 — hera's heartbeat must use the cached id for re-confirmation, not blindly repost. (We'll match how the MCP-tool registrar handles this.)
- `argus/internal/tui/views/connector.go`: WebSocket protocol is binary frames both ways with text-frame control envelopes (`resize`, `focus`, `blur`). Binary plugin→argus = ANSI bytes for the surface. Binary argus→plugin = keystrokes. `Esc` is intercepted at argus's keystroke pump and never reaches the plugin.
- `argus/internal/tui/plugin_views.go`: argus's plugin-view mount uses `streampane.StreamPane` for the embedded rendering. That's the widget pattern we're copying inside our own surface.
- `argus/internal/db/tasks.go`: archive emits `task.archived` SSE events from the partial-column `SetArchived` path only — NOT from the `Update` path used by the HTTP and MCP archive entrypoints (today's discovered bug, separately tracked). For `a archive` in hera-view we'll call argus's HTTP endpoint and accept the same event-drop pattern; the dynamic rail update doesn't depend on argus events (it uses hera's internal pub/sub from D6).
- `argus/internal/api/handlers.go`: `/api/tasks/{id}/stream?since=N` is SSE keyed on the X-Output-Total cursor returned by `/api/tasks/{id}/output`. Snapshot-then-stream is the canonical pattern; we follow it for D3.

## Acceptance criteria

Captured per design section. These map directly to scenarios in the hera-view delta spec.

**D1 (TUI library) — pure technology choice, no behavioral acceptance criteria.**

**D2 (Server location):**

- It should serve the plugin-view WebSocket from the same listener as the MCP HTTP server.
- It should not require a separate process or binary to be installed.

**D3 (Pre-load PTY state):**

- It should open a snapshot fetch + SSE subscription for every live binding at startup.
- It should swap pane buffers when the rail selection changes without a new network call.
- It should bound per-task PTY ring buffer at ~256 KiB.

**D4 (Focus model + key routing):**

- It should start in RAIL focus on first open.
- It should advance focus along the RAIL→COORD→AGENT ladder on Cmd/Ctrl-→.
- It should retreat focus along AGENT→COORD→RAIL on Cmd/Ctrl-←.
- It should jump directly from RAIL to AGENT on Enter.
- It should return to RAIL focus on Ctrl-Q from any state.
- It should forward all non-binding keystrokes in COORD focus to the bound coord task's `POST /api/tasks/{id}/input` endpoint.
- It should forward all non-binding keystrokes in AGENT focus to the bound agent task's `POST /api/tasks/{id}/input` endpoint.
- It should ignore mutation keys (`n`,`r`,`^d`,`a`,`l`,`?`) when not in RAIL focus and treat them as ordinary characters forwarded to the bound PTY.
- It should render a colored border around the focused element.

**D5 (Rail operations):**

- It should open a confirmation or input modal before any mutation (n/r/^d/a/resurrect).
- After `n` confirm, it should create an orchestrator + coord role + binding by spawning an argus task that calls `hera_new_orchestrator`.
- After `r` confirm, it should update the chosen orchestrator's or role's `name` column and reflect the new name in the rail on next refresh.
- After `^d` confirm on a role, it should end the role's binding, mark the role archived, and `git worktree remove --force` the binding's worktree path.
- After `^d` confirm on an orchestrator, it should cascade the above to every role under the orchestrator.
- After `a` on an orchestrator/role, it should set `archived_at` and call argus's `POST /api/tasks/{id}/archive`.
- It should move archived items into an Archive section in the rail.
- `l` should toggle visibility of the Archive section.
- On Enter against an archived orchestrator with the Archive section visible, it should unarchive and spawn a fresh argus task that calls `hera_join(cwd)` to claim the dormant role.
- `?` should display the full bindings modal.

**D6 (Dynamic rail updates):**

- It should refresh the rail within ~100 ms of any DAO write to orchestrators / roles / bindings.
- It should not poll the DB on a timer.

**D7 (Resize):**

- It should re-layout when argus sends a resize envelope.
- It should not POST to the source task's resize endpoint when its own viewport changes.

**D8 (Registration):**

- It should register the plugin view at daemon startup and heartbeat every 5 minutes.
- It should unregister on daemon shutdown.

**D9 (WebSocket server):**

- It should accept WebSocket upgrades at `/view`.
- It should close the prior WebSocket connection when a new one arrives.

**D10 (Migration):**

- It should add nullable `archived_at` columns to `orchestrators` and `roles` without breaking existing rows.
- Existing `List*` calls should default to active-only behavior.

**D11 (Freelance rail + dual-mode layout):**

- It should render a single collapsible Freelance section below all projects, collapsed by default with a `▸` chevron.
- It should list every live freelance agent across all orchestrators in that section and render no freelance agent under its orchestrator.
- It should keep worker agents nested under their orchestrators.
- It should toggle the Freelance section open/closed on Space when its header is selected.
- It should omit the Freelance section header when no live freelance agent exists.
- It should render archived freelance agents only inside the Archive section, never in the live Freelance section.
- When a freelance agent is selected, it should compose the body as rail + a single full-width agent pane with no coord pane.
- When a coordinator or worker row is selected, it should restore the three-column rail + coord + agent layout.
- In freelance mode, Cmd/Ctrl-→ from RAIL should move focus directly to AGENT and Cmd/Ctrl-← from AGENT should return to RAIL, never entering COORD.
- In freelance mode, it should release the coord subscription and forward no keystrokes to any coordinator task.

## Design self-review (per brainstorm skill)

- Placeholder scan: no TBDs except the Open Questions section, which is intentional.
- Internal consistency: rail-only mutation keys (D4) match the operation descriptions (D5). Pre-load (D3) matches dynamic-update mechanism (D6) and resize policy (D7).
- Scope check: large change, but cohesive — one capability + one delta. Stages well into vertical slices for tasks.md.
- Ambiguity check: rename-of-role-vs-argus-task-name was ambiguous; explicit decision recorded ("rename does not touch argus task names"). Resurrect-with-or-without-mission-preservation was ambiguous; explicit decision recorded ("role's mission and constraints inherited; new argus task gets a fresh worktree").
- Acceptance criteria coverage: every behavioral section has criteria. D1 is pure technology — no behavioral criteria, intentional.
