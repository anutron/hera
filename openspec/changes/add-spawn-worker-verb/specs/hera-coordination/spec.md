# Delta: add-spawn-worker-verb

## MODIFIED Requirements

### Requirement: Seven MCP tools exposed under the `hera_` prefix (supersedes v1 "Six")

The system SHALL register seven MCP tools with argus when the daemon starts: `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`, and `hera_spawn_worker`. The v1 "exactly six" decision is superseded for v1.x; the seventh verb is sanctioned for born-bound worker spawning. Each tool MUST be force-prefixed `hera_` per the substrate's tool-name enforcement. Each tool's input schema MUST declare `cwd` as a required input parameter.

#### Scenario: Seven MCP tools registered on startup

- **WHEN** the hera daemon completes startup successfully
- **THEN** the MCP registry MUST show seven tools scoped to `hera`, with names `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`, and `hera_spawn_worker`

## ADDED Requirements

### Requirement: Coordinator-initiated atomic worker spawn

The system SHALL provide a `hera_spawn_worker` MCP tool that allows a coordinator agent to create a new worker task and bind it to its orchestrator in one atomic operation. The calling task MUST hold a live coordinator binding; any other role kind MUST be rejected with an explanatory error.

#### Scenario: Happy path – worker spawned and born bound

- **WHEN** a coordinator calls `hera_spawn_worker(cwd, prompt="<worker instructions>")` with a valid coordinator binding
- **THEN** hera MUST create a new argus task in the coordinator's `argus_project` with the prompt prefixed by an orientation sentence naming the coordinator
- **AND** hera MUST insert a `worker` role under the calling coordinator's orchestrator with a name derived from the prompt (or `role_name` if supplied)
- **AND** hera MUST insert a live binding tying the new argus task to the new worker role
- **AND** hera MUST return `{ orchestrator, role_name, kind: "worker", mission, binding_id, argus_task_id, prompt_auto_submitted }`

#### Scenario: Auto-submit – prompt runs without manual Enter

- **WHEN** `hera_spawn_worker` creates the argus task successfully
- **THEN** hera MUST attempt `POST /api/tasks/{id}/input` with body `\r` (CR, byte 0x0D) to auto-run the prompt
- **AND** the response MUST include `prompt_auto_submitted: true` when the POST succeeds, `false` when it fails
- **AND** a POST failure MUST NOT cause `hera_spawn_worker` to return an error; the worker is already bound

#### Scenario: Caller is not a coordinator – rejected

- **WHEN** a worker or freelance role calls `hera_spawn_worker`
- **THEN** hera MUST return `isError: true` with a message explaining that only coordinators may spawn workers

#### Scenario: Prompt is empty – rejected

- **WHEN** `hera_spawn_worker` is called with an empty or whitespace-only `prompt`
- **THEN** hera MUST return `isError: true` with a message explaining that `prompt` is required

#### Scenario: Project override

- **WHEN** `hera_spawn_worker` is called with a non-empty `project` field
- **THEN** the argus task MUST be created in the specified project rather than the coordinator's default `argus_project`

#### Scenario: Role name derived from prompt when not supplied

- **WHEN** `hera_spawn_worker` is called without a `role_name`
- **THEN** the worker role name MUST be derived from the first 40 characters of `prompt` via slug normalization and uniqued within the orchestrator's existing non-archived roles

#### Scenario: Explicit role name used when supplied

- **WHEN** `hera_spawn_worker` is called with a non-empty `role_name`
- **THEN** that name MUST be used as the base for uniqueness checking with suffix `-2`/`-3`/… appended if a sibling role with that name already exists

#### Scenario: GetTask failure – binding inserted with empty worktree path

- **WHEN** `hera_spawn_worker` successfully creates the argus task but `GET /api/tasks/{id}` fails
- **THEN** the worker role and binding MUST still be inserted with an empty `worktree_path` and the spawn MUST complete successfully
