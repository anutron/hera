# hera-view delta: align-modals-with-argus

## ADDED Requirements

### Requirement: Spawn modals select the project with a scrollable type-to-filter list

The system SHALL present the **Project** selector in the new-coordinator (`n`) modal and the new-worker (`w`) modal as a scrollable, type-to-filter option list — NOT a `◄ ►` cycler and NOT a `tview.DropDown`. The list SHALL render a `>` cursor on the highlighted option and an `(N/M)` counter (cursor position within the currently filtered options / count of filtered options). Up and Down MUST move the cursor within the filtered options without submitting the form, scrolling the visible window to keep the cursor in view. Typing a printable rune MUST append to a filter string and narrow the visible options to those whose label contains the filter text (case-insensitive substring), resetting the cursor to the first filtered option; Backspace MUST delete the last filter rune, and clearing the filter MUST restore the full option list. Enter MUST lock the highlighted option and advance focus to the next field (it MUST NOT submit the form); Tab and Backtab MUST advance and retreat focus; Esc MUST cancel the modal. When the configured project list is empty, the selector MUST show a single visible `(no projects configured)` entry that maps to the coordinator's project (the empty-list fallback) on confirm. The selector MUST use the argus theme, consistent with the other modal fields.

The **Backend** selector MUST remain a `◄ backend ►` cycler in both modals (argus's New Task modal uses a cycler for backend); only the Project selector is list-mode.

#### Scenario: Project selector renders as a list, not a cycler

- **WHEN** the new-coordinator or new-worker modal opens with two or more configured projects
- **THEN** the Project field MUST render a scrollable list with a `>` cursor and an `(N/M)` counter AND MUST NOT render a `◄ current (n/m) ►` cycler

#### Scenario: Down moves the cursor without submitting

- **WHEN** the Project list is focused with the cursor on the first option and the operator presses Down
- **THEN** the `>` cursor MUST move to the second option AND the form MUST NOT be submitted

#### Scenario: Typing filters the visible options

- **WHEN** the Project list holds focus over options `foo-frontend`, `foo-backend`, `bar-api` and the operator types `back`
- **THEN** the visible options MUST narrow to `foo-backend` AND the cursor MUST rest on the first filtered option AND the `(N/M)` counter MUST reflect the filtered count

#### Scenario: Clearing the filter restores the full list

- **WHEN** a filter has narrowed the Project list and the operator deletes every filter rune with Backspace
- **THEN** the full option list MUST be restored

#### Scenario: Enter locks the selection and advances focus

- **WHEN** the Project list is focused with the cursor on a chosen option and the operator presses Enter
- **THEN** the chosen option MUST become the selected project AND focus MUST advance to the next field AND the form MUST NOT be submitted

#### Scenario: Backend stays a cycler

- **WHEN** the new-coordinator or new-worker modal opens
- **THEN** the Backend field MUST render as a `◄ backend ►` cycler

#### Scenario: Empty project list degrades to a single fallback entry

- **WHEN** the configured project list is empty and a spawn modal opens
- **THEN** the Project selector MUST show a single `(no projects configured)` entry that maps to the coordinator's project on confirm

### Requirement: Spawn modals are paste-ready

The system SHALL deliver a paste event received while a spawn modal (new-coordinator or new-worker) is open to the focused text field as a single insertion, so the field's contents reflect the entire pasted string in one operation rather than ingesting it rune-by-rune. The Project list widget MUST NOT intercept a paste destined for a focused text field — it consumes input only while it itself holds focus.

This requirement governs hera's modal-side handling only. Whether bracketed-paste markers reach hera over the argus plugin-view transport is out of scope for this requirement; when markers do arrive as a `tcell` paste event, the focused field MUST apply them as one chunk.

#### Scenario: Paste lands in the focused prompt field as one chunk

- **WHEN** a spawn modal is open with the multi-line Prompt field focused and a paste event carrying a multi-character string is delivered to the application
- **THEN** the Prompt field's contents MUST contain the entire pasted string applied in a single operation (no per-rune ingestion)

## MODIFIED Requirements

### Requirement: `w` spawns a worker under the selected coordinator

The system SHALL, when the operator presses `w` while focus is `RAIL`, open an input modal with a **Project** selector, a **Branch** field, a **Backend** selector, and a **Prompt** field. The Project selector SHALL be initialized to the coordinator's own `argus_project` and SHALL be presented as a scrollable, type-to-filter list (per the project-list-select requirement) that lets the operator select any other configured argus project. The Backend selector SHALL be a `◄ backend ►` cycler initialized to the first configured backend (a per-project default backend is not yet plumbed into hera's spawn path — see the design's Cross-repo follow-ups). The Branch field SHALL default empty (an empty Branch uses the effective project's default ref). On confirm with a non-empty prompt, the system SHALL spawn a worker agent under the coordinator implied by the current selection, in the **selected project**, and attach it programmatically, WITHOUT requiring the spawned worker's agent to call any hera MCP tool for the attachment.

Selection resolves to a target coordinator as follows: a coordinator row (root orchestrator header OR a sub-coordinator role row) targets that coordinator; an agent/worker row whose role belongs to a coordinator targets that agent's coordinator (the orchestrator of the agent's role). The selected agent/worker row's own liveness is irrelevant to resolution: an archived or dead agent row still resolves to its (valid) coordinator and spawns. A freelance row, a separator/expando row, or any selection not attached to a coordinator MUST NOT spawn anything — it MUST surface a dismissible "not applicable" notice (never a silent no-op).

The **effective project** is the project chosen in the modal's Project selector; when the operator leaves the selector untouched it is the coordinator role's `argus_project` (the default). When the configured project list is empty, the selector MUST degrade to a visible "(no projects configured)" entry that maps to the coordinator's `argus_project` on confirm.

On confirm the system SHALL, in order: (1) resolve the target orchestrator and the effective project; (2) derive a worker role name from the prompt and make it unique among the orchestrator's non-archived roles; (3) create an argus task via `POST /api/tasks` in the effective project whose body carries the worker's prompt AND the chosen Branch and Backend when set, AND mirror `meta:hera.role=worker` to the created task; (4) read the created task's `worktree_path` via `GET /api/tasks/{id}`; (5) insert a worker role (`kind=worker`, the effective project as its `argus_project`, `mission` set to the operator's prompt, the derived name) and a binding tying the role to the created task id and carrying the resolved `worktree_path`. The role and binding inserts MUST emit on the rail broadcaster so the rail repopulates, rendering the new worker nested under its coordinator within approximately 100 ms, and the system MUST auto-select the new worker row while leaving focus in `RAIL`. Auto-select is best-effort and broadcaster-driven: the new row is selected on the next `populateRail` in which it exists; if the row does not appear within a bounded number of repopulates, the pending selection MUST be abandoned and the abandonment logged (the cursor simply does not move), never retried unboundedly.

The created task's prompt SHALL be a short hera orientation prefix (naming the coordinator and noting the worker may report progress via `hera_send`) followed by the operator's prompt text verbatim. An empty (or whitespace-only) prompt MUST be rejected: no argus task MUST be created and no role or binding row MUST be inserted, AND the rejection MUST be operator-visible — a dismissible notice (never a silent modal close).

The spawn's blocking work (argus calls and DB inserts) MUST run off the tview event loop per the rail-mutation contract; a failure MUST surface as an error modal via the event-loop queue without freezing the UI. The argus task is created via the existing scope-token `POST /api/tasks`; the worker branches off the chosen Branch when set, falling back to the effective project's default ref when the Branch field is empty, and runs under the chosen Backend when set, using the project's default backend when the Backend value is empty.

The spawn is resilient to two partial-failure modes after the argus task has been created:

- If `GET /api/tasks/{id}` (step 4) fails, the system MUST still insert the worker role and binding (with an empty `worktree_path`) and log the failure; the spawn MUST NOT be aborted. The worker is reachable and managed; worktree-dependent operations (`^d`, `^p`, resize) soft-skip an empty path.
- If the role or binding insert (step 5) fails after the argus task was created, the system MUST NOT delete the created argus task. It MUST log the orphaned task id and surface the error; the live argus task survives and appears in the rail's Freelance section (recoverable), rather than being destroyed by a fallible rollback.

#### Scenario: `w` in RAIL opens the spawn-worker modal on a coordinator

- **WHEN** focus is `RAIL`, a coordinator row (root or sub) is selected, and the operator presses `w`
- **THEN** the view MUST open an input modal with a Project selector, a Branch field, a Backend selector, and a Prompt field

#### Scenario: Project selector defaults to the coordinator's project

- **WHEN** focus is `RAIL`, a coordinator whose coord role's `argus_project` is `foo-frontend` is selected, and the operator presses `w`
- **THEN** the modal's Project selector MUST be initialized to `foo-frontend`

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

#### Scenario: Untouched selector spawns in the coordinator's project

- **WHEN** the operator confirms the spawn-worker modal against coordinator `foo` whose coord role's `argus_project` is `foo-frontend` WITHOUT changing the Project selector, with prompt `build the sidebar`
- **THEN** the daemon MUST issue a `POST /api/tasks` with project `foo-frontend` AND MUST mirror `meta:hera.role=worker` to the created task AND the inserted worker role's `argus_project` MUST be `foo-frontend`

#### Scenario: Selecting a project in the list spawns in the chosen project

- **WHEN** the operator confirms the spawn-worker modal against coordinator `foo` (coord project `foo-frontend`) after selecting `foo-backend` in the Project list, with prompt `build the API`
- **THEN** the daemon MUST issue a `POST /api/tasks` with project `foo-backend` AND the inserted worker role's `argus_project` MUST be `foo-backend`

#### Scenario: Empty project list degrades to the coordinator's project

- **WHEN** the configured argus project list is empty and the operator confirms the spawn-worker modal against coordinator `foo` (coord project `foo-frontend`) with a non-empty prompt
- **THEN** the Project selector MUST have shown a "(no projects configured)" entry AND the daemon MUST issue a `POST /api/tasks` with project `foo-frontend`

#### Scenario: Chosen Branch and Backend are forwarded to the spawn

- **WHEN** the operator confirms the spawn-worker modal with a non-empty prompt, the Branch field set to `origin/release` and the Backend cycler set to `codex`
- **THEN** the daemon MUST issue a `POST /api/tasks` carrying branch `origin/release` and backend `codex`

#### Scenario: Empty Branch branches off the project default ref

- **WHEN** the operator confirms the spawn-worker modal with a non-empty prompt and the Branch field left empty
- **THEN** the daemon MUST issue a `POST /api/tasks` whose branch is unset, so the worker branches off the effective project's default ref
