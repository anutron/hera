## ADDED Requirements

### Requirement: Project discovery via `hera_projects`

The system SHALL register a read-only MCP tool `hera_projects` that lists every configured argus project. For each project the tool MUST return its `name` and, where the project has them configured, its default `branch` and default `backend`. Projects that inherit argus's global default branch/backend MAY omit those fields. The tool MUST perform no side effects and MUST NOT require a resolvable caller role: project discovery is global, so `hera_projects` MUST succeed for any caller regardless of whether the calling `cwd` maps to a tracked argus task. The tool SHALL source its data from the same configured-project list that backs `hera_spawn_worker` project validation (argus `GET /api/projects/full`).

#### Scenario: hera_projects lists configured projects with defaults

- **WHEN** argus has projects `ARGUS` (branch `main`, backend `claude`) and `Hera` (no configured branch/backend) AND `hera_projects` is called
- **THEN** the tool MUST return a success payload listing both projects, with `ARGUS` carrying `branch="main"` and `backend="claude"` AND `Hera` present by name, AND a `count` of 2

#### Scenario: hera_projects requires no caller role

- **WHEN** `hera_projects` is called from a `cwd` that does not map to any tracked argus task
- **THEN** the tool MUST still return the configured project list (it MUST NOT reject the call for an unresolvable caller role)

#### Scenario: hera_projects gated when argus link is unhealthy

- **WHEN** `hera_projects` is called AND hera's argus link state is down or recovering
- **THEN** the tool MUST return `isError: true` with the standard link-gate message rather than a raw transport error

#### Scenario: hera_projects registered as an MCP tool on startup

- **WHEN** the hera daemon completes startup successfully
- **THEN** the MCP tool registry MUST include a tool named `hera_projects` scoped to `hera`

### Requirement: `hera_spawn_worker` rejects unknown projects with a self-correcting error

The system SHALL validate the resolved `project` for a `hera_spawn_worker` call against hera's known argus project list BEFORE creating the argus task. The resolved project is the explicit `project` input when supplied, otherwise the calling coordinator's own `argus_project`. When the resolved project is not among the configured argus projects, the tool MUST return `isError: true` with a message that names the rejected project AND lists the valid project names, so the caller can self-correct without inspecting the filesystem. This validation MUST run after the existing coordinator-kind check (a non-coordinator caller is still rejected first).

The validation is best-effort with respect to discovery availability: if hera cannot fetch the configured project list (e.g. transient argus unavailability), the handler MUST NOT hard-block the spawn — it MUST fall through to the argus task-creation path, which surfaces its own error if argus is genuinely unavailable.

#### Scenario: Unknown project lists valid project names

- **WHEN** a coordinator calls `hera_spawn_worker(project="argus", prompt=...)` AND the configured argus projects are `ARGUS`, `Hera`, `Iris` (no project named `argus`)
- **THEN** the tool MUST return `isError: true` AND the message MUST contain the rejected name `argus` AND the valid names `ARGUS`, `Hera`, `Iris`
- **AND** no argus task MUST be created

#### Scenario: Known project passes validation and spawns

- **WHEN** a coordinator calls `hera_spawn_worker(project="ARGUS", prompt=...)` AND `ARGUS` is among the configured argus projects
- **THEN** the validation MUST pass AND the worker task MUST be created as normal

#### Scenario: Discovery failure does not block spawning

- **WHEN** a coordinator calls `hera_spawn_worker` with a resolved project AND the configured-project list cannot be fetched
- **THEN** the handler MUST NOT reject the call on validation grounds; it MUST proceed to create the argus task
