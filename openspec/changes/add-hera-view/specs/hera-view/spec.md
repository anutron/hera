## ADDED Requirements

### Requirement: Plugin view registered with argus on daemon startup

The system SHALL register a single argus plugin view when the hera daemon starts, via `POST /api/plugins/views`. The registration MUST include a non-empty `title`, a `hotkey` (default tentative `Ctrl-H`; final choice flagged as an open question), and a `callback_url` pointing at hera's in-process WebSocket route (`ws://127.0.0.1:7744/view`). The registration MUST use hera's existing scope token. The registration MUST be heartbeated every 5 minutes (matching the existing MCP-tool registrar shape) to stay within argus's idle sweep. On graceful shutdown (SIGINT/SIGTERM), hera MUST delete the registration via `DELETE /api/plugins/views/{id}` before exiting.

#### Scenario: Plugin view registered on startup

- **WHEN** the hera daemon completes startup successfully
- **THEN** an HTTP GET against argus's plugin-views registry MUST show exactly one plugin view owned by hera with `callback_url` pointing at `/view` on hera's own HTTP listener

#### Scenario: Plugin view heartbeated

- **WHEN** the hera daemon has been running for more than 5 minutes
- **THEN** hera MUST have re-POSTed the plugin-view registration at least once since startup

#### Scenario: Plugin view unregistered on shutdown

- **WHEN** the hera daemon receives SIGTERM and shuts down cleanly
- **THEN** hera MUST issue a DELETE for the plugin-view registration before the process exits

### Requirement: View server lives in the daemon process

The system SHALL serve the plugin-view WebSocket from the same HTTP listener as the existing MCP callback (`127.0.0.1:7744`). The view MUST NOT require a separate process or binary; the daemon owns the route directly.

#### Scenario: View served on the existing MCP listener

- **WHEN** an HTTP client opens a WebSocket against `ws://127.0.0.1:7744/view`
- **THEN** the hera daemon process MUST handle the upgrade in-process; no separate `hera-view` binary or subprocess MUST be required

### Requirement: WebSocket upgrade at /view with last-writer-wins reconnection

The system SHALL accept WebSocket upgrades at the `GET /view` route. The protocol mirrors argus's plugin-view contract: binary frames carry ANSI bytes (server → client) and keystroke bytes (client → server); text frames carry JSON control envelopes for `resize`, `focus`, and `blur`. When a new WebSocket connection arrives while a prior one is still open, the prior connection MUST be closed; only one active session per daemon is supported.

#### Scenario: WebSocket upgrade accepted

- **WHEN** an HTTP client sends a valid WebSocket upgrade request to `/view`
- **THEN** the hera daemon MUST complete the upgrade and start a per-connection rendering goroutine

#### Scenario: Second connection closes the first

- **WHEN** a second WebSocket upgrade arrives at `/view` while a prior connection is still open
- **THEN** the daemon MUST close the prior connection before continuing to serve the new one

### Requirement: PTY proxy pre-loads snapshot and SSE per live binding at daemon startup

The system SHALL, at daemon startup (and on subsequent binding-created events), open a snapshot fetch (`GET /api/tasks/{id}/output`) followed by a live SSE subscription (`GET /api/tasks/{id}/stream?since=<X-Output-Total>`) for every live binding. Bytes from each task MUST be appended to a per-task in-memory ring buffer with a cap of approximately 256 KiB. The ring buffer MUST drop the oldest bytes when full. Rail navigation between agents MUST swap pane subscriptions to the relevant ring buffer in-process without triggering a new network round-trip.

#### Scenario: Snapshot and SSE opened per live binding at startup

- **WHEN** the hera daemon completes startup with N live bindings present in the DB
- **THEN** the PTY proxy MUST have issued exactly N snapshot fetches and N SSE subscriptions, one per binding

#### Scenario: Rail navigation swaps without a network call

- **WHEN** the operator moves rail selection from agent A to agent B and both bindings are already pre-loaded
- **THEN** the panes MUST render agent B's coord and agent B's PTY from in-memory ring buffers without issuing any new HTTP request

#### Scenario: Ring buffer bounded at ~256 KiB

- **WHEN** an SSE stream has emitted more than 256 KiB of output for a single task
- **THEN** the ring buffer for that task MUST retain only approximately the most recent 256 KiB; older bytes MUST be dropped

### Requirement: Three-column layout with top and bottom chrome bars

The system SHALL render a three-column body inside top and bottom chrome bars whenever the view application is active. The left column is the navigation rail; the middle column is the COORD pane (PTY of the selected agent's project's coordinator); the right column is the AGENT pane (PTY of the selected agent itself). The top bar SHALL contain literal text `HERA` left-aligned. The bottom bar SHALL contain context-aware key-binding hints per the current focus state.

#### Scenario: Layout has three body columns and two chrome rows

- **WHEN** the view application is running and the layout has rendered
- **THEN** the surface MUST be composed of a 1-row top bar, a 3-column body (rail + coord + agent), and a 1-row bottom bar

#### Scenario: Rail traversal updates both panes

- **WHEN** rail selection moves to an agent whose project's coord differs from the previous selection's project
- **THEN** the COORD pane MUST switch to the new project's coord binding ring buffer AND the AGENT pane MUST switch to the new agent's binding ring buffer

### Requirement: Three-state focus model

The system SHALL maintain focus in exactly one of three states at any given time: `RAIL`, `COORD`, or `AGENT`. On first open, focus MUST start in `RAIL`. The focused element MUST be visually indicated by a colored border (e.g., via tview's `SetBorderColor`).

#### Scenario: First-open focus is RAIL

- **WHEN** a new WebSocket connection is established and the view application starts
- **THEN** focus MUST be in the `RAIL` state

#### Scenario: Focus indicator on focused element

- **WHEN** focus is in any one of `RAIL`, `COORD`, or `AGENT`
- **THEN** the corresponding element MUST render with a colored border distinct from the other two elements' borders

### Requirement: Focus traversal via arrow ladder and Ctrl-Q escape

The system SHALL advance focus along the `RAIL → COORD → AGENT` ladder on Cmd/Ctrl-→ and retreat along the `AGENT → COORD → RAIL` ladder on Cmd/Ctrl-←. From `RAIL` focus, pressing `Enter` MUST jump directly to `AGENT` focus (skipping `COORD`). From any focus state, pressing `Ctrl-Q` MUST return focus to `RAIL`.

#### Scenario: Cmd/Ctrl-right advances RAIL → COORD

- **WHEN** focus is `RAIL` and the operator presses Cmd/Ctrl-→
- **THEN** focus MUST transition to `COORD`

#### Scenario: Cmd/Ctrl-right advances COORD → AGENT

- **WHEN** focus is `COORD` and the operator presses Cmd/Ctrl-→
- **THEN** focus MUST transition to `AGENT`

#### Scenario: Cmd/Ctrl-left retreats AGENT → COORD

- **WHEN** focus is `AGENT` and the operator presses Cmd/Ctrl-←
- **THEN** focus MUST transition to `COORD`

#### Scenario: Cmd/Ctrl-left retreats COORD → RAIL

- **WHEN** focus is `COORD` and the operator presses Cmd/Ctrl-←
- **THEN** focus MUST transition to `RAIL`

#### Scenario: Enter from RAIL jumps to AGENT

- **WHEN** focus is `RAIL` and the operator presses `Enter` against a live (non-archived) agent row
- **THEN** focus MUST transition to `AGENT` (skipping `COORD`)

#### Scenario: Ctrl-Q returns to RAIL from any state

- **WHEN** focus is `COORD` or `AGENT` and the operator presses `Ctrl-Q`
- **THEN** focus MUST transition to `RAIL`

### Requirement: Pane focus forwards keystrokes to the bound task's PTY input

The system SHALL forward keystrokes received while focus is `COORD` or `AGENT` to the bound task's input endpoint (`POST /api/tasks/{id}/input`) verbatim. Keystrokes that match the focus-traversal bindings (Cmd/Ctrl-←/→, Ctrl-Q) MUST be intercepted by the view application and MUST NOT be forwarded.

#### Scenario: Typed key forwarded to COORD task

- **WHEN** focus is `COORD` and the operator types a single character `x`
- **THEN** the daemon MUST issue a `POST /api/tasks/{coord_task_id}/input` carrying the byte `x` AND the byte MUST NOT be rendered locally in the COORD pane (the byte is rendered when the source PTY echoes it back via SSE)

#### Scenario: Focus-traversal key not forwarded

- **WHEN** focus is `AGENT` and the operator presses `Ctrl-Q`
- **THEN** focus MUST transition to `RAIL` AND no `POST /api/tasks/.../input` MUST be issued for that key event

### Requirement: Mutation keys are RAIL-focus-only

The system SHALL recognize the six mutation keys (`n`, `r`, `^d`, `a`, `l`, `?`) ONLY when focus is `RAIL`. When focus is `COORD` or `AGENT`, these characters MUST be treated as ordinary input and forwarded to the bound task's PTY (per the keystroke-forwarding requirement).

#### Scenario: `n` in RAIL focus opens new-project modal

- **WHEN** focus is `RAIL` and the operator presses `n`
- **THEN** the view MUST open the new-project input modal

#### Scenario: `n` in COORD focus types into the PTY

- **WHEN** focus is `COORD` and the operator presses `n`
- **THEN** the daemon MUST POST the byte `n` to the COORD task's input endpoint AND MUST NOT open the new-project modal

#### Scenario: `r` in AGENT focus types into the PTY

- **WHEN** focus is `AGENT` and the operator presses `r`
- **THEN** the daemon MUST POST the byte `r` to the AGENT task's input endpoint AND MUST NOT open the rename modal

#### Scenario: `?` in AGENT focus types into the PTY

- **WHEN** focus is `AGENT` and the operator presses `?`
- **THEN** the daemon MUST POST the byte `?` to the AGENT task's input endpoint AND MUST NOT open the help modal

### Requirement: `n` creates a new orchestrator via spawned argus task

The system SHALL, when the operator confirms the new-project modal with a unique non-empty name and an optional coord mission, create a new argus task via `POST /api/tasks` whose prompt invokes `hera_new_orchestrator(cwd=$PWD, name=<chosen>, coord_role_name="coord", mission=<chosen>)` as its first action. The argus project MUST default to the chosen orchestrator name (creating the argus project if absent). The view MUST NOT directly insert orchestrator / role / binding rows; those rows MUST be created by the existing `hera_new_orchestrator` handler when the spawned task makes its first MCP call.

#### Scenario: New-project confirm spawns argus task

- **WHEN** the operator confirms the new-project modal with name `foo` and mission `ship F`
- **THEN** the daemon MUST issue a `POST /api/tasks` whose prompt contains `hera_new_orchestrator(cwd=$PWD, name="foo", coord_role_name="coord", mission="ship F")`

#### Scenario: New-project confirm with duplicate non-archived name rejected

- **WHEN** the operator confirms the new-project modal with a name that matches an existing non-archived orchestrator
- **THEN** the modal MUST surface a validation error AND no argus task MUST be spawned

### Requirement: `r` renames the selected orchestrator or role

The system SHALL, when the operator confirms the rename modal with a unique non-empty name, update the chosen orchestrator's or role's `name` column in hera's DB. Uniqueness is enforced across non-archived orchestrators (when renaming an orchestrator) or within the orchestrator's non-archived roles (when renaming a role). Rename MUST NOT affect the argus task's name or its worktree; argus task names are independent of hera role names.

#### Scenario: Rename role updates DB and reflects in rail

- **WHEN** the operator confirms the rename modal against role `foo/coord` with the new name `lead`
- **THEN** the `roles` row MUST be updated to `name="lead"` AND the rail MUST reflect the new name on the next refresh tick AND no argus side effects MUST occur

#### Scenario: Rename to duplicate name rejected

- **WHEN** the operator confirms the rename modal with a name that conflicts with another non-archived row at the same scope
- **THEN** the modal MUST surface a validation error AND no DB write MUST occur

### Requirement: `^d` deletes a role or cascade-deletes an orchestrator

The system SHALL, on `^d` confirmation against a role, end the role's live binding (if any) with `end_reason="user_deleted"`, set the role's `archived_at` to the current timestamp, and invoke `git worktree remove --force <worktree_path>` against the binding's worktree path. The role row MUST persist (archived). On `^d` confirmation against an orchestrator, the same operations MUST cascade to every role under the orchestrator, and the orchestrator row's `archived_at` MUST also be set. If a worktree path is empty or the directory does not exist, the `git worktree remove` step MUST be a soft no-op (logged and skipped, not an error).

#### Scenario: Delete role ends binding and removes worktree

- **WHEN** the operator confirms the `^d` modal against role `foo/worker-1` whose live binding has worktree path `/Users/x/.argus/worktrees/foo/worker-1`
- **THEN** hera MUST update the binding's `ended_at` and `end_reason="user_deleted"`, set the role's `archived_at` to the current timestamp, AND execute `git worktree remove --force /Users/x/.argus/worktrees/foo/worker-1`

#### Scenario: Delete orchestrator cascades to all roles

- **WHEN** the operator confirms the `^d` modal against orchestrator `foo` which has roles `coord`, `w1`, `w2`
- **THEN** hera MUST end every live binding under `foo`, set `archived_at` on `foo`, `coord`, `w1`, and `w2`, AND invoke `git worktree remove --force` against each role's binding's worktree path

#### Scenario: Worktree missing is soft no-op

- **WHEN** the operator confirms `^d` against a role whose binding's `worktree_path` is empty OR the directory does not exist on disk
- **THEN** hera MUST skip the `git worktree remove` step, log the skip, AND still mark the role archived AND end the binding

### Requirement: `a` toggles archived state on an orchestrator or role

The system SHALL, on `a` against a non-archived role, set the role's `archived_at` to the current timestamp AND invoke argus's archive endpoint (`POST /api/tasks/{id}/archive`) on the binding's `argus_task_id`. The worktree MUST be preserved. On `a` against a non-archived orchestrator, the same MUST cascade to every role under that orchestrator. On `a` against an already-archived role or orchestrator, `archived_at` MUST be cleared (the role unarchives); unarchiving an orchestrator MUST NOT cascade to roles (roles unarchive individually).

#### Scenario: Archive role calls argus and preserves worktree

- **WHEN** the operator presses `a` against non-archived role `foo/w1` bound to argus task `T1`
- **THEN** hera MUST set the role's `archived_at` to the current timestamp, issue `POST /api/tasks/T1/archive` to argus, AND MUST NOT touch the worktree directory

#### Scenario: Archive orchestrator cascades to roles

- **WHEN** the operator presses `a` against non-archived orchestrator `foo` with roles `coord`, `w1`, `w2`
- **THEN** hera MUST set `archived_at` on `foo`, `coord`, `w1`, and `w2` AND issue an archive call to argus for each role's live binding's argus_task_id

#### Scenario: Unarchive orchestrator does not cascade

- **WHEN** the operator presses `a` against an archived orchestrator `foo` whose roles `coord`, `w1` are also archived
- **THEN** hera MUST clear `archived_at` on `foo` AND MUST leave `archived_at` on `coord` and `w1` set

### Requirement: `l` toggles visibility of archived items in the rail

The system SHALL render archived orchestrators and roles in a collapsible "Archive" section at the bottom of the rail. This section MUST be hidden on first view-application open. The `l` key (when focus is `RAIL`) MUST toggle visibility of the Archive section. Toggle state is in-memory for the WebSocket session; a fresh WebSocket connection MUST start with the Archive section hidden.

#### Scenario: First-open hides archived items

- **WHEN** the view application starts (new WebSocket connection)
- **THEN** archived orchestrators and roles MUST NOT be visible in the rail

#### Scenario: `l` reveals archived items

- **WHEN** focus is `RAIL`, the Archive section is hidden, and the operator presses `l`
- **THEN** the rail MUST render a collapsible Archive section listing all archived orchestrators and roles

#### Scenario: `l` toggles back to hidden

- **WHEN** focus is `RAIL`, the Archive section is visible, and the operator presses `l`
- **THEN** the rail MUST hide the Archive section

### Requirement: Resurrect archived orchestrator on Enter when Archive visible

The system SHALL, when the Archive section is visible and the operator presses `Enter` against an archived coord row, prompt for confirmation ("Resurrect <project>?") and on confirm: clear `archived_at` on the orchestrator and the coord role, then spawn a fresh argus task via `POST /api/tasks` in the role's stored `argus_project` whose prompt invokes `hera_join(cwd=$PWD)`. The new task's worktree is fresh; the role's stored mission and constraints are inherited by the rebinding when `hera_join` resolves the cwd to the dormant binding-slot.

#### Scenario: Resurrect spawns argus task in role's argus_project

- **WHEN** the operator presses `Enter` against the archived coord row of orchestrator `foo` whose coord role has `argus_project="foo-frontend"` AND confirms the modal
- **THEN** hera MUST clear `archived_at` on orchestrator `foo` and the coord role AND MUST issue `POST /api/tasks` to argus's `foo-frontend` project with a prompt containing `hera_join(cwd=$PWD)`

#### Scenario: Resurrect inherits mission and constraints

- **WHEN** the operator resurrects an archived coord role whose `mission="ship F"` and `constraints="ship by friday"`
- **THEN** the dormant role row's `mission` and `constraints` columns MUST remain unchanged (the new task inherits them on `hera_join`)

### Requirement: `?` displays a help modal listing all bindings

The system SHALL display a modal overlay listing all key bindings grouped by focus state when the operator presses `?` while focus is `RAIL`. The modal MUST be dismissable by pressing `q` (since `Esc` is reserved by argus). The modal MUST NOT trigger any DB read or write.

#### Scenario: `?` opens help modal

- **WHEN** focus is `RAIL` and the operator presses `?`
- **THEN** a modal MUST appear listing the focus-traversal keys and the six mutation keys grouped by focus state

#### Scenario: `q` closes help modal

- **WHEN** the help modal is open and the operator presses `q`
- **THEN** the modal MUST close AND focus MUST remain in `RAIL`

### Requirement: Every mutation is gated behind a confirmation or input modal

The system SHALL display a confirmation or input modal for every rail-level mutation (`n`, `r`, `^d`, `a` and resurrect-on-Enter against an archived coord). The DB writes and external HTTP calls MUST NOT occur unless the operator explicitly confirms the modal. A purely-view-state toggle (`l` listall) MUST NOT require a modal.

#### Scenario: `^d` shows confirmation before deleting

- **WHEN** the operator presses `^d` in `RAIL` against a role
- **THEN** a confirmation modal MUST appear naming the role and (if applicable) its worktree path AND MUST require an explicit confirm keystroke before any DB write or `git worktree remove` invocation

#### Scenario: `l` listall does not require a modal

- **WHEN** the operator presses `l` in `RAIL`
- **THEN** the Archive section visibility MUST toggle immediately with no modal

### Requirement: Rail refreshes within ~100 ms of any DAO write

The system SHALL subscribe the rail to an in-process broadcaster fed by the orchestrators / roles / bindings DAOs. Any insert, update, or delete on those tables MUST cause the rail to refresh its rendered tree within approximately 100 ms (debounced to coalesce bursts). The system MUST NOT poll the database on a timer for rail updates.

#### Scenario: Newly-adopted worker appears in rail

- **WHEN** the auto-adopt event handler inserts a new role + binding row for orchestrator `foo`
- **THEN** the rail MUST render the new agent under `foo` within approximately 100 ms

#### Scenario: No polling timer

- **WHEN** the daemon is idle (no DAO writes) for 60 seconds
- **THEN** the rail subsystem MUST NOT issue any DB read for the purpose of rail refresh during that interval

### Requirement: Resize envelope re-lays out the local view and source PTYs

The system SHALL handle a `{type:"resize", cols, rows}` text-frame envelope from argus by recalculating the tview Application's layout (top bar, bottom bar, fixed-width rail, equal-split coord + agent panes). For each task bound to a coord or agent pane, the system SHALL also request that the task's source PTY be resized to match the pane's allocated cols/rows via `POST /api/tasks/{id}/size`. The system MUST dedupe redundant resize requests (same cols/rows as the last value sent for that task).

#### Scenario: Resize re-lays out panes

- **WHEN** argus sends `{type:"resize", cols:120, rows:40}` over the WebSocket
- **THEN** the view MUST re-render the layout with the new dimensions on the next frame

#### Scenario: Initial bind aligns source PTY to pane allocation

- **WHEN** a coord or agent pane is bound to an argus task and rendered for the first time
- **THEN** the daemon MUST issue `POST /api/tasks/{id}/size` with the pane's allocated cols/rows unless that size already matches the task's current PTY size

#### Scenario: Layout change re-aligns source PTY

- **WHEN** the bound pane's allocated cols/rows change (whether from a WebSocket resize envelope or any other layout shift)
- **THEN** the daemon MUST issue `POST /api/tasks/{id}/size` with the new allocation

#### Scenario: Redundant resize is deduped

- **WHEN** the daemon would issue `POST /api/tasks/{id}/size` with cols/rows equal to the last value it sent for that task
- **THEN** the daemon MUST skip the HTTP call
