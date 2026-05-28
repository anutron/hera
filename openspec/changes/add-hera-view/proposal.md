## Why

Hera 1.0 has shipped the coordination substrate — orchestrators, role identity, message bus, idle-gated auto-inject, settings, install, argus reconnect — but the operator surface is still "another argus task running Claude with the hera tools open." Watching multiple agents work simultaneously, switching focus between coordinator and worker conversations, and managing project lifecycle (create / rename / archive / resurrect) all happen through awkward chains of MCP calls, manual argus task navigation, and ad-hoc keystroke routing.

`hera-view` is the argus plugin view that closes this gap: a single screen where every project's coord and every agent are simultaneously visible, instantly switchable, and lifecycle-managed without leaving the view. It is the last v1.x roadmap item and the one that makes hera's coordination story legible to a human operator.

## What Changes

- **Add `hera-view` capability** — a registered argus plugin view that hera's daemon serves over a WebSocket on its existing MCP HTTP listener (`:7744`).
- **Dual-mode body layout** with argus-style top + bottom chrome bars:
  - Left rail: all orchestrators (projects) with their **worker** roles nested underneath; a single global, collapsible **Freelance** section below all projects collecting every freelance agent across orchestrators; archived section hidden by default.
  - **Project mode** (coord/worker selected): three columns — middle pane = the project's coordinator PTY, right pane = the selected agent PTY.
  - **Freelance mode** (freelance agent selected): two elements — rail + a single full-width agent pane (no coord pane); the focus ladder collapses to RAIL ↔ AGENT.
  - Top bar: literal text `HERA` (placeholder for future status content).
  - Bottom bar: context-aware key-binding hints per focus mode and column mode.
- **Pre-load all task PTY state on startup** — snapshot fetch + live SSE per coord/agent across every project. Switching agents in the rail is a buffer swap, not a network round-trip. Matches argus's instant rail-traversal feel.
- **Three-state focus model** — `RAIL`, `COORD`, `AGENT`. Cmd/Ctrl-←/→ shift focus left/right along the rail→coord→agent ladder. `Enter` from rail jumps directly to the agent pane (skips coord). `Ctrl-Q` returns to rail from anywhere. Argus reserves only Esc.
- **Focus indicator** — colored border around the focused element (tview-native).
- **Six rail-only operations** with bottom-bar discoverability:
  - **`n` new** — create a new project: prompt for name + coord mission, hera creates the orchestrator + spawns the coord argus task with a `hera_new_orchestrator` bootstrap prompt.
  - **`r` rename** — rename the selected orchestrator or role.
  - **`^d` del** — confirm-then-delete: archive the role(s) in hera + delete the underlying argus worktree(s). Cascades from coord to all agents in the project. Role data survives (archived flag); worktree removed.
  - **`a` archive** — toggle archived state on the selected orchestrator/role; archived items move below an Archive section. Argus task gets archived; worktree stays on disk.
  - **`l` listall** — toggle visibility of the Archive section. Hitting Enter on an archived coord resurrects it by spawning a fresh argus task in the role's stored `argus_project`; the new task `hera_join`s and inherits the preserved role identity (name / mission / constraints).
  - **`?` help** — modal showing the full bindings reference.
- **Add `archived_at` columns** to `orchestrators` and `roles` tables (via a new SQLite migration).
- **Modify hera-coordination spec** to define rename + archive + resurrect semantics on orchestrators and roles (these mutations are not in the base spec today).
- **Hera daemon** registers the plugin view at startup via `POST /api/plugins/views` and heartbeats every 5 min (mirroring the existing MCP-tool registration pattern).

## Capabilities

### New Capabilities

- `hera-view`: the argus plugin view — rendering, layout, focus and key routing, the six rail-operations, lifecycle (registration, WebSocket server, per-connection tview app, snapshot+SSE management, dynamic rail updates).

### Modified Capabilities

- `hera-coordination`: adds requirements for rename, archive, and resurrect semantics on orchestrators and roles; documents the `archived_at` columns and the role-identity-survives-archive guarantee.

## Impact

**Affected code (hera repo):**

- `internal/view/` — new package: tview application, custom `tcell.Screen` backed by the plugin-view WebSocket, layout composition, focus + key handler, the six operations.
- `internal/argus/` — small additions: a typed client for `POST /api/plugins/views` registration + heartbeat; the existing PTY snapshot (`GET /api/tasks/{id}/output`) + SSE (`GET /api/tasks/{id}/stream`) endpoints are already represented by event-stream code and need light wrapping.
- `internal/db/` — migration `0003_archived_at.sql` adding `archived_at` to orchestrators and roles; DAO updates for archive / unarchive / rename.
- `internal/daemon/run.go` — start the plugin-view registrar + WebSocket server alongside the existing MCP registrar.
- `cmd/hera/` — no new CLI verb; the view is reached entirely via argus's hotkey.

**Dependencies:**

- `github.com/rivo/tview` (and `github.com/gdamore/tcell/v2`) — matches argus's stack.
- `github.com/charmbracelet/lipgloss` — for top/bottom bar styling (matches argus's stack).
- `github.com/coder/websocket` — argus already uses this; we use it on the server side for the plugin-view callback URL.

**Spec deltas:**

- `openspec/changes/add-hera-view/specs/hera-view/spec.md` — all new requirements (ADDED).
- `openspec/changes/add-hera-view/specs/hera-coordination/spec.md` — MODIFIED requirements for rename, archive, resurrect; possibly ADDED requirements for the same.

**Substrate concerns flagged (not blocking design, but for awareness):**

- Argus's `task_archive` MCP tool is the right call for `a archive` — already exists.
- Argus's worktree removal for `^d del` may need a new substrate API. The current task_archive doesn't delete the worktree directory. If argus has no "remove worktree" verb today, hera-view either calls `git worktree remove` directly (in the daemon's privileged process) or escalates a substrate ticket. The design will define the chosen path.
- The settings-callback `auth_header` drop bug (filed in FOLLOWUPS) does not affect this change — plugin-view WebSocket auth is separate from settings-form callback auth.
