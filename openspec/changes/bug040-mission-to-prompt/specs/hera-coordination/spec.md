# Delta: bug040-mission-to-prompt

## MODIFIED Requirements

### Requirement: Role identity outlives argus task lifecycle

The system SHALL persist role identity, `prompt`, and accumulated history (messages, role status) across argus task lifecycle events. Archiving an argus task that incarnates a role MUST end that role's current binding without deleting the role row or its associated messages. The role's `prompt` (the only free-form field) MUST survive archive/resurrect intact.

#### Scenario: Coordinator task archived, role and prompt survive

- **WHEN** a coordinator role with `prompt="ship the thing"` is bound to argus task `T1` and `T1` is archived
- **THEN** hera MUST set the binding's `ended_at` and `end_reason` columns AND leave the role row with `prompt="ship the thing"` intact

### Requirement: hera_new_orchestrator accepts prompt (not mission/constraints)

The system SHALL provide `hera_new_orchestrator(cwd, name, coordinator_role_name, [prompt])` as the canonical "be an orchestrator" entry point. The tool MUST accept a single optional `prompt` parameter (free-form prose) instead of the previous `mission` and `constraints` pair. `constraints` MUST NOT be accepted. The call MUST create the orchestrator row, the coordinator role with the given `prompt`, a binding row tying the calling argus task to that coordinator role, AND mirror `meta:hera.role=coordinator` to the bound argus task.

#### Scenario: hera_new_orchestrator with prompt

- **WHEN** `hera_new_orchestrator(cwd=$PWD, name="foo", coordinator_role_name="coord", prompt="ship F by friday")` is called from an unbound argus task
- **THEN** hera MUST create orchestrator `foo`, create a coordinator role `coord` with `prompt="ship F by friday"`, insert a live binding to the calling argus task, AND PUT `{key: "role", value: "coordinator"}` to the bound task's `/api/tasks/{id}/meta` endpoint

#### Scenario: hera_new_orchestrator response includes prompt

- **WHEN** `hera_new_orchestrator` succeeds
- **THEN** the response MUST include `{ orchestrator, role_name, kind: "coordinator", prompt, binding_id, argus_task_id, created }`

### Requirement: hera_join accepts prompt (not mission/constraints)

The system SHALL allow an existing argus task in any project to attach itself to an existing orchestrator post-hoc by calling `hera_join` with `orchestrator=<name>`, `role_name=<self-named>`, `kind="worker"` or `kind="freelance"`, and optional `prompt`, `status`. Hera MUST create the role row + binding row atomically AND mirror `meta:hera.role=<kind>` to the bound argus task. The tool MUST NOT accept `mission` or `constraints` parameters; any call with those parameters MUST ignore them (or the schema MUST omit them so callers never send them).

#### Scenario: hera_join attach with prompt

- **WHEN** `hera_join(cwd=$PWD, orchestrator="foo", role_name="refactor-sidebar", kind="freelance", prompt="...", status="working")` is invoked from a worktree whose argus task has no prior hera binding
- **THEN** hera MUST create a freelance role under `foo`, insert a binding row tying the calling task to it, populate `prompt` from the call arg, set role_status to `working`, mirror `meta:hera.role=freelance` to the bound argus task, AND return the role identity in the tool response

#### Scenario: hera_join identity response includes prompt

- **WHEN** `hera_join(cwd)` is called in claim mode (re-incarnation)
- **THEN** the response MUST include `{ orchestrator, role_name, kind, prompt, status, unread_message_count, binding_id, argus_task_id }`

### Requirement: hera_spawn_worker uses prompt only (no mission)

The system SHALL provide `hera_spawn_worker` WITHOUT a `mission` parameter. The tool's `prompt` parameter is the worker's full task instructions; the role row's `prompt` column is populated from that same value. There is no separate mission field.

#### Scenario: Spawn worker – role prompt set from task prompt

- **WHEN** `hera_spawn_worker(cwd, prompt="migrate the schema")` is called by a coordinator
- **THEN** the new worker role's `prompt` column MUST equal `"migrate the schema"` (the verbatim operator-supplied prompt, not the orientation-prefixed task prompt)

### Requirement: Role model has a single free-form field named prompt

The system SHALL store at most one free-form text field per role: `prompt`. The previous `mission` and `constraints` columns MUST NOT exist. A DB migration (0007) MUST rename `mission` → `prompt` and DROP `constraints` from the roles table, preserving existing `mission` values in the new `prompt` column. The migration MUST be idempotent on a fresh database (run after the schema migrations that originally created `mission`/`constraints`).

#### Scenario: Existing mission data preserved after migration

- **WHEN** migration 0007 runs against a database with existing rows having `mission="build the substrate"` and `constraints="no force-push"`
- **THEN** those rows MUST have `prompt="build the substrate"` after migration, AND the `constraints` column MUST no longer exist

#### Scenario: Fresh DB has only prompt column

- **WHEN** a fresh hera database is initialized and all migrations run
- **THEN** the `roles` table MUST have a `prompt` column and MUST NOT have `mission` or `constraints` columns

### Requirement: Auto-adopt copies prompt from task meta

The system SHALL read `meta:hera.prompt` from the new task's metadata at adoption time and populate the role row's `prompt` column from that value. `meta:hera.mission` and `meta:hera.constraints` are no longer recognized. The `prompt` key MUST be optional; absence MUST result in an empty-string `prompt` column.

#### Scenario: Prompt meta present

- **WHEN** an auto-adopted task has `meta:hera.prompt="implement schema migration"`
- **THEN** the created role row's `prompt` column MUST contain `"implement schema migration"` verbatim

#### Scenario: Prompt meta absent

- **WHEN** an auto-adopted task has `meta:hera.role=worker` but no `meta:hera.prompt`
- **THEN** the created role row's `prompt` column MUST be an empty string (not NULL)

### Requirement: Details pane shows Prompt only

The coordinator Details pane MUST display a single "Prompt:" field showing the coordinator role's `prompt` value. The "Constraints:" line MUST NOT be rendered. If `prompt` is empty the field MUST render `(none)` in the dimmed style.

#### Scenario: Details pane renders Prompt

- **WHEN** a coordinator with `prompt="ship the thing"` is selected in the rail
- **THEN** the Details pane MUST display `Prompt:` followed by the wrapped prompt text, and MUST NOT show a `Constraints:` label

### Requirement: Archive preserves prompt

The system SHALL preserve the `prompt` column (and `argus_project`) unchanged when a role is archived.

#### Scenario: Archive preserves prompt value

- **WHEN** a role with `prompt="ship F"` and `argus_project="foo-frontend"` is archived
- **THEN** the role row MUST have `archived_at` set to the current RFC3339 timestamp AND `prompt` AND `argus_project` MUST be unchanged

### Requirement: Resurrect preserves prompt

The system SHALL preserve the `prompt` value across resurrect: when an archived role is resurrected via `hera_join`, its `prompt` MUST remain unchanged in the role row AND appear in the response.

#### Scenario: Resurrect preserves prompt value

- **WHEN** an archived role with `prompt="ship F"` is resurrected by a fresh `hera_join` in its `argus_project`
- **THEN** the role row's `prompt` column MUST remain `"ship F"` AND the response to `hera_join` MUST surface this value to the caller
