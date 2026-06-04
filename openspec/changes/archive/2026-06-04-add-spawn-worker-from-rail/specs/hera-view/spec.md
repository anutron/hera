## ADDED Requirements

### Requirement: `w` spawns a worker under the selected coordinator

The system SHALL, when the operator presses `w` while focus is `RAIL`, open a single-field input modal prompting for the new worker's **Prompt**. On confirm with a non-empty prompt, the system SHALL spawn a worker agent under the coordinator implied by the current selection and attach it programmatically, WITHOUT requiring the spawned worker's agent to call any hera MCP tool for the attachment.

Selection resolves to a target coordinator as follows: a coordinator row (root orchestrator header OR a sub-coordinator role row) targets that coordinator; an agent/worker row whose role belongs to a coordinator targets that agent's coordinator (the orchestrator of the agent's role). A freelance row, a separator/expando row, or any selection not attached to a coordinator MUST NOT spawn anything — it MUST surface a dismissible "not applicable" notice (never a silent no-op).

On confirm the system SHALL, in order: (1) resolve the target orchestrator and the coordinator role's `argus_project`; (2) derive a worker role name from the prompt and make it unique among the orchestrator's non-archived roles; (3) create an argus task via `POST /api/tasks` in that `argus_project` whose body carries the worker's prompt, AND mirror `meta:hera.role=worker` to the created task; (4) read the created task's `worktree_path` via `GET /api/tasks/{id}`; (5) insert a worker role (`kind=worker`, the resolved `argus_project`, `mission` set to the operator's prompt, the derived name) and a binding tying the role to the created task id and carrying the resolved `worktree_path`. The role and binding inserts MUST emit on the rail broadcaster so the rail repopulates, rendering the new worker nested under its coordinator within approximately 100 ms, and the system MUST auto-select the new worker row while leaving focus in `RAIL`.

The created task's prompt SHALL be a short hera orientation prefix (naming the coordinator and noting the worker may report progress via `hera_send`) followed by the operator's prompt text verbatim. An empty prompt MUST be rejected with a validation error and MUST spawn no task.

The spawn's blocking work (argus calls and DB inserts) MUST run off the tview event loop per the rail-mutation contract; a failure MUST surface as an error modal via the event-loop queue without freezing the UI. The argus task is created via the existing scope-token `POST /api/tasks`, so the worker branches off the coordinator's argus-project default ref; no base branch is selected.

#### Scenario: `w` in RAIL opens the spawn-worker modal on a coordinator

- **WHEN** focus is `RAIL`, a coordinator row (root or sub) is selected, and the operator presses `w`
- **THEN** the view MUST open a single-field input modal prompting for the worker's prompt

#### Scenario: `w` resolves an agent selection to its coordinator

- **WHEN** focus is `RAIL`, an agent/worker row under coordinator `foo` is selected, the operator presses `w`, and confirms the modal with prompt `implement X`
- **THEN** the spawned worker role MUST be created under orchestrator `foo` (the selected agent's coordinator)

#### Scenario: `w` on a non-coordinator selection gives feedback

- **WHEN** focus is `RAIL`, a freelance row OR a separator/expando row is selected, and the operator presses `w`
- **THEN** a dismissible "not applicable" notice MUST appear AND no argus or DB call MUST be issued

#### Scenario: Empty prompt is rejected

- **WHEN** the operator confirms the spawn-worker modal with an empty (or whitespace-only) prompt
- **THEN** the modal MUST surface a validation error AND no argus task MUST be created AND no role or binding row MUST be inserted

#### Scenario: Confirm spawns a task in the coordinator's project with worker meta

- **WHEN** the operator confirms the spawn-worker modal against coordinator `foo` whose coord role's `argus_project` is `foo-frontend`, with prompt `build the sidebar`
- **THEN** the daemon MUST issue a `POST /api/tasks` with project `foo-frontend` AND MUST mirror `meta:hera.role=worker` to the created task

#### Scenario: Role and binding are inserted programmatically with the worktree path

- **WHEN** the spawn-worker confirm creates argus task `T9` whose `GET /api/tasks/T9` reports `worktree_path=/Users/x/.argus/worktrees/foo-frontend/build-the-sidebar`
- **THEN** hera MUST insert a worker role under coordinator `foo`'s orchestrator AND a binding tying that role to `T9` with `worktree_path=/Users/x/.argus/worktrees/foo-frontend/build-the-sidebar` — without the worker's agent calling any hera MCP tool

#### Scenario: Role name is derived from the prompt and uniqued

- **WHEN** the operator spawns a worker under an orchestrator that already has a non-archived role named `build-the-sidebar`, with a prompt that also derives to `build-the-sidebar`
- **THEN** the new role's name MUST be a suffixed variant (e.g. `build-the-sidebar-2`) so it does not collide with the existing non-archived sibling

#### Scenario: Worker prompt carries an orientation prefix

- **WHEN** the operator confirms the spawn-worker modal with prompt `migrate the schema`
- **THEN** the created argus task's prompt MUST be a hera orientation prefix (naming the coordinator) followed by `migrate the schema`

#### Scenario: New worker renders nested and is auto-selected

- **WHEN** the spawn-worker confirm completes successfully
- **THEN** the rail MUST render the new worker as a child row under its coordinator within approximately 100 ms AND the rail selection MUST move to the new worker row AND focus MUST remain `RAIL`

#### Scenario: Spawn failure surfaces without freezing

- **WHEN** the spawn-worker background work returns an error (e.g. the argus `POST /api/tasks` fails)
- **THEN** an error modal MUST appear via the event-loop queue AND the UI MUST NOT have been blocked

## MODIFIED Requirements

### Requirement: Mutation keys are RAIL-focus-only

The system SHALL recognize the RAIL-only key set (`n`, `w`, `r`, `a`, `l`, `?`, `s`, `S`, `^d`, `^r`, `^p`) ONLY when focus is `RAIL`. When focus is `COORD` or `AGENT`, every one of these keys — including the destructive/external verbs `^d`, `^r`, and `^p` — MUST be treated as ordinary input and forwarded to the bound task's PTY (per the keystroke-forwarding requirement): a printable key forwards its byte, and `^d`/`^r`/`^p` forward their control bytes (Ctrl-D=0x04, Ctrl-R=0x12, Ctrl-P=0x10) so an agent gets EOF / reverse-search / history-prev normally. None of these keys fires a mutation or is intercepted while focus is in a pane.

#### Scenario: `n` in RAIL focus opens new-project modal

- **WHEN** focus is `RAIL` and the operator presses `n`
- **THEN** the view MUST open the new-project input modal

#### Scenario: `w` in RAIL focus opens spawn-worker modal

- **WHEN** focus is `RAIL` and the operator presses `w`
- **THEN** the view MUST open the spawn-worker input modal (and MUST NOT forward the byte `w` to any task)

#### Scenario: `n` in COORD focus types into the PTY

- **WHEN** focus is `COORD` and the operator presses `n`
- **THEN** the daemon MUST POST the byte `n` to the COORD task's input endpoint AND MUST NOT open the new-project modal

#### Scenario: `w` in AGENT focus types into the PTY

- **WHEN** focus is `AGENT` and the operator presses `w`
- **THEN** the daemon MUST POST the byte `w` to the AGENT task's input endpoint AND MUST NOT open the spawn-worker modal

#### Scenario: `r` in AGENT focus types into the PTY

- **WHEN** focus is `AGENT` and the operator presses `r`
- **THEN** the daemon MUST POST the byte `r` to the AGENT task's input endpoint AND MUST NOT open the rename modal

#### Scenario: `?` in AGENT focus types into the PTY

- **WHEN** focus is `AGENT` and the operator presses `?`
- **THEN** the daemon MUST POST the byte `?` to the AGENT task's input endpoint AND MUST NOT open the help modal

#### Scenario: `^d` in AGENT focus forwards Ctrl-D to the PTY

- **WHEN** focus is `AGENT` and the operator presses `^d`
- **THEN** the daemon MUST forward the control byte Ctrl-D (`0x04`) to the AGENT task's input endpoint AND MUST NOT open the delete confirm modal

### Requirement: Every mutation is gated behind a confirmation or input modal

The system SHALL display a confirmation or input modal for every destructive, creative, or external rail-level mutation (`n`, `w`, `r`, `^d`, `^r` prune, `^p` open-PR, and resurrect-on-Enter against an archived coord). The DB writes, worktree/branch destruction, and external HTTP calls MUST NOT occur unless the operator explicitly confirms the modal. Reversible single-key toggles — `a` archive/unarchive and `l` listall — MUST NOT require a modal (`a` is reversible by pressing `a` again; `l` is a pure view-state toggle).

#### Scenario: `^d` shows confirmation before deleting

- **WHEN** the operator presses `^d` in `RAIL` against a role
- **THEN** a confirmation modal MUST appear naming the role and (if applicable) its worktree path AND MUST require an explicit confirm keystroke before any DB write or `git worktree remove` invocation

#### Scenario: `w` shows an input modal before spawning

- **WHEN** the operator presses `w` in `RAIL` against a coordinator
- **THEN** an input modal MUST appear AND no argus task, role, or binding MUST be created until the operator confirms it with a non-empty prompt

#### Scenario: `l` listall does not require a modal

- **WHEN** the operator presses `l` in `RAIL`
- **THEN** the Archive section visibility MUST toggle immediately with no modal
