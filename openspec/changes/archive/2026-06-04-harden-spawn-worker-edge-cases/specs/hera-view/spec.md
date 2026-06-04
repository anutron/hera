## MODIFIED Requirements

### Requirement: `w` spawns a worker under the selected coordinator

The system SHALL, when the operator presses `w` while focus is `RAIL`, open a single-field input modal prompting for the new worker's **Prompt**. On confirm with a non-empty prompt, the system SHALL spawn a worker agent under the coordinator implied by the current selection and attach it programmatically, WITHOUT requiring the spawned worker's agent to call any hera MCP tool for the attachment.

Selection resolves to a target coordinator as follows: a coordinator row (root orchestrator header OR a sub-coordinator role row) targets that coordinator; an agent/worker row whose role belongs to a coordinator targets that agent's coordinator (the orchestrator of the agent's role). The selected agent/worker row's own liveness is irrelevant to resolution: an archived or dead agent row still resolves to its (valid) coordinator and spawns. A freelance row, a separator/expando row, or any selection not attached to a coordinator MUST NOT spawn anything — it MUST surface a dismissible "not applicable" notice (never a silent no-op).

On confirm the system SHALL, in order: (1) resolve the target orchestrator and the coordinator role's `argus_project`; (2) derive a worker role name from the prompt and make it unique among the orchestrator's non-archived roles; (3) create an argus task via `POST /api/tasks` in that `argus_project` whose body carries the worker's prompt, AND mirror `meta:hera.role=worker` to the created task; (4) read the created task's `worktree_path` via `GET /api/tasks/{id}`; (5) insert a worker role (`kind=worker`, the resolved `argus_project`, `mission` set to the operator's prompt, the derived name) and a binding tying the role to the created task id and carrying the resolved `worktree_path`. The role and binding inserts MUST emit on the rail broadcaster so the rail repopulates, rendering the new worker nested under its coordinator within approximately 100 ms, and the system MUST auto-select the new worker row while leaving focus in `RAIL`. Auto-select is best-effort and broadcaster-driven: the new row is selected on the next `populateRail` in which it exists; if the row does not appear within a bounded number of repopulates, the pending selection MUST be abandoned and the abandonment logged (the cursor simply does not move), never retried unboundedly.

The created task's prompt SHALL be a short hera orientation prefix (naming the coordinator and noting the worker may report progress via `hera_send`) followed by the operator's prompt text verbatim. An empty (or whitespace-only) prompt MUST be rejected: no argus task MUST be created and no role or binding row MUST be inserted, AND the rejection MUST be operator-visible — a dismissible notice (never a silent modal close).

The spawn's blocking work (argus calls and DB inserts) MUST run off the tview event loop per the rail-mutation contract; a failure MUST surface as an error modal via the event-loop queue without freezing the UI. The argus task is created via the existing scope-token `POST /api/tasks`, so the worker branches off the coordinator's argus-project default ref; no base branch is selected.

The spawn is resilient to two partial-failure modes after the argus task has been created:

- If `GET /api/tasks/{id}` (step 4) fails, the system MUST still insert the worker role and binding (with an empty `worktree_path`) and log the failure; the spawn MUST NOT be aborted. The worker is reachable and managed; worktree-dependent operations (`^d`, `^p`, resize) soft-skip an empty path.
- If the role or binding insert (step 5) fails after the argus task was created, the system MUST NOT delete the created argus task. It MUST log the orphaned task id and surface the error; the live argus task survives and appears in the rail's Freelance section (recoverable), rather than being destroyed by a fallible rollback.

#### Scenario: `w` in RAIL opens the spawn-worker modal on a coordinator

- **WHEN** focus is `RAIL`, a coordinator row (root or sub) is selected, and the operator presses `w`
- **THEN** the view MUST open a single-field input modal prompting for the worker's prompt

#### Scenario: `w` resolves an agent selection to its coordinator

- **WHEN** focus is `RAIL`, an agent/worker row under coordinator `foo` is selected, the operator presses `w`, and confirms the modal with prompt `implement X`
- **THEN** the spawned worker role MUST be created under orchestrator `foo` (the selected agent's coordinator)

#### Scenario: `w` resolves an archived or dead agent row to its coordinator

- **WHEN** focus is `RAIL`, an archived (or dead) agent/worker row under coordinator `foo` is selected, the operator presses `w`, and confirms with a non-empty prompt
- **THEN** the worker MUST be spawned under coordinator `foo` (the selected row's archived/dead state MUST NOT block resolution to its still-valid coordinator)

#### Scenario: `w` on a non-coordinator selection gives feedback

- **WHEN** focus is `RAIL`, a freelance row OR a separator/expando row is selected, and the operator presses `w`
- **THEN** a dismissible "not applicable" notice MUST appear AND no argus or DB call MUST be issued

#### Scenario: Empty prompt is rejected with a visible notice

- **WHEN** the operator confirms the spawn-worker modal with an empty (or whitespace-only) prompt
- **THEN** a dismissible "prompt is required" notice MUST appear (NOT a silent modal close) AND no argus task MUST be created AND no role or binding row MUST be inserted

#### Scenario: Confirm spawns a task in the coordinator's project with worker meta

- **WHEN** the operator confirms the spawn-worker modal against coordinator `foo` whose coord role's `argus_project` is `foo-frontend`, with prompt `build the sidebar`
- **THEN** the daemon MUST issue a `POST /api/tasks` with project `foo-frontend` AND MUST mirror `meta:hera.role=worker` to the created task

#### Scenario: Role and binding are inserted programmatically with the worktree path

- **WHEN** the spawn-worker confirm creates argus task `T9` whose `GET /api/tasks/T9` reports `worktree_path=/Users/x/.argus/worktrees/foo-frontend/build-the-sidebar`
- **THEN** hera MUST insert a worker role under coordinator `foo`'s orchestrator AND a binding tying that role to `T9` with `worktree_path=/Users/x/.argus/worktrees/foo-frontend/build-the-sidebar` — without the worker's agent calling any hera MCP tool

#### Scenario: GetTask failure still binds with an empty worktree path

- **WHEN** the spawn-worker confirm successfully creates the argus task but the subsequent `GET /api/tasks/{id}` fails
- **THEN** hera MUST still insert the worker role and a binding with an empty `worktree_path` AND log the failure AND MUST NOT abort the spawn

#### Scenario: Insert failure after task creation does not delete the argus task

- **WHEN** the spawn-worker confirm successfully creates the argus task but the subsequent role or binding insert fails
- **THEN** hera MUST NOT issue a `DELETE` for the created argus task AND MUST log the orphaned task id AND surface the error (the orphaned task survives as a freelancer)

#### Scenario: Role name is derived from the prompt and uniqued

- **WHEN** the operator spawns a worker under an orchestrator that already has a non-archived role named `build-the-sidebar`, with a prompt that also derives to `build-the-sidebar`
- **THEN** the new role's name MUST be a suffixed variant (e.g. `build-the-sidebar-2`) so it does not collide with the existing non-archived sibling

#### Scenario: Worker prompt carries an orientation prefix

- **WHEN** the operator confirms the spawn-worker modal with prompt `migrate the schema`
- **THEN** the created argus task's prompt MUST be a hera orientation prefix (naming the coordinator) followed by `migrate the schema`

#### Scenario: New worker renders nested and is auto-selected

- **WHEN** the spawn-worker confirm completes successfully
- **THEN** the rail MUST render the new worker as a child row under its coordinator within approximately 100 ms AND the rail selection MUST move to the new worker row AND focus MUST remain `RAIL`

#### Scenario: Auto-select is abandoned if the new row never appears

- **WHEN** a worker spawn queues an auto-select but the new row never appears in the rail across a bounded number of `populateRail` repopulates
- **THEN** the pending auto-select MUST be abandoned and the abandonment logged, AND the rail MUST NOT keep retrying the selection unboundedly

#### Scenario: Spawn failure surfaces without freezing

- **WHEN** the spawn-worker background work returns an error (e.g. the argus `POST /api/tasks` fails)
- **THEN** an error modal MUST appear via the event-loop queue AND the UI MUST NOT have been blocked

### Requirement: Non-applicable mutation keys give visible feedback

The system SHALL give visible feedback (a dismissible message naming the key and why it does not apply) whenever a RAIL-focus mutation key is pressed on a selection it cannot act on — e.g. `s`/`S` or `a` on a separator/expando row, `s`/`S` on an orchestrator header with no coord role, `^p` on a selection with no resolvable worktree, `r`/`^d` on a freelance row (freelancers are argus tasks with no hera role to rename or delete), or `w` on a selection that cannot resolve a target coordinator (a freelance row, a separator/expando row, an orchestrator header with no coord role, a sub-coordinator row with no child orchestrator, or a leaf role row with no owning coordinator). A RAIL-focus mutation key MUST NOT be a silent no-op.

#### Scenario: s on a non-addressable row gives feedback

- **WHEN** focus is `RAIL` on a non-addressable row (e.g. the Archive separator) and the operator presses `s`
- **THEN** a dismissible message MUST appear indicating the key does not apply to that row AND no argus or DB call MUST be issued

#### Scenario: a on a non-addressable row gives feedback

- **WHEN** focus is `RAIL` on a non-addressable row and the operator presses `a`
- **THEN** a dismissible message MUST appear indicating the key does not apply to that row AND no argus or DB call MUST be issued

#### Scenario: ^d on a freelancer gives feedback instead of a dead-end error

- **WHEN** focus is `RAIL` on a freelance row and the operator presses `^d`
- **THEN** a dismissible message MUST explain that delete does not apply to a freelance row AND no destructive operation MUST be issued

#### Scenario: w on a coordinator-shaped row with no resolvable target gives feedback

- **WHEN** focus is `RAIL` on a coordinator-shaped row that cannot resolve a target coordinator (an orchestrator header with no coord role, OR a sub-coordinator row with no child orchestrator) and the operator presses `w`
- **THEN** a dismissible "not applicable" notice MUST appear AND no argus or DB call MUST be issued
