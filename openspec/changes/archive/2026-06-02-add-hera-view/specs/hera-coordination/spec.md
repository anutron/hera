## ADDED Requirements

### Requirement: Orchestrators and roles carry `archived_at` columns

The system SHALL add a nullable `archived_at` column (RFC3339 string) to both the `orchestrators` and `roles` tables. A NULL value MUST mean "active"; a non-NULL value MUST mean "archived at that timestamp". The migration MUST be additive and MUST NOT touch existing rows (they inherit NULL). An index MUST be present on each `archived_at` column to support `WHERE archived_at IS NULL` predicates used by the default list path.

#### Scenario: Migration runs on existing DB

- **WHEN** a daemon with the new migration is started against an existing hera DB whose `orchestrators` and `roles` tables predate the column
- **THEN** the migration MUST add `archived_at TEXT` (nullable) to both tables AND existing rows MUST have `archived_at IS NULL`

#### Scenario: Indexes present after migration

- **WHEN** the migration completes
- **THEN** `orchestrators` and `roles` MUST each have an index on the `archived_at` column

### Requirement: List* DAO methods default to active-only

The system SHALL, by default, filter `archived_at IS NOT NULL` rows out of every `List*` DAO method on `Orchestrators` and `Roles`. Callers MUST be able to opt in to inclusive listing via an explicit `IncludeArchived bool` parameter (or equivalent `ListInclusive` method). Internal lookups by primary key (e.g., `Get(id)`) MUST NOT filter on `archived_at` (archived rows are still resolvable when explicitly named).

#### Scenario: ListActive returns non-archived only

- **WHEN** the orchestrators table has rows with mixed `archived_at` values and the daemon calls the default `ListOrchestrators()` (no inclusive flag)
- **THEN** the result MUST contain only rows where `archived_at IS NULL`

#### Scenario: ListInclusive returns all rows

- **WHEN** the orchestrators table has rows with mixed `archived_at` values and the daemon calls `ListOrchestrators(IncludeArchived: true)` (or equivalent inclusive method)
- **THEN** the result MUST contain every row regardless of `archived_at` value

#### Scenario: Get by id resolves archived row

- **WHEN** an orchestrator row has `archived_at` set and the daemon calls `GetOrchestratorByID(id)`
- **THEN** the row MUST be returned (the archived flag MUST NOT cause a not-found)

### Requirement: Orchestrators may be renamed

The system SHALL support renaming an orchestrator via a DAO `RenameOrchestrator(id, newName)` method. The new name MUST be unique across non-archived orchestrators. The rename MUST NOT modify any other column on the orchestrator row, MUST NOT touch any role row, and MUST NOT affect any argus task name or worktree.

#### Scenario: Rename orchestrator updates only the name column

- **WHEN** `RenameOrchestrator(id=42, newName="bar")` is called and no other non-archived orchestrator is named `bar`
- **THEN** orchestrator id 42's `name` MUST be `bar` AND no other column on that row MUST be touched AND no role row MUST be modified AND no argus HTTP call MUST be issued

#### Scenario: Rename to existing non-archived name rejected

- **WHEN** `RenameOrchestrator(id=42, newName="foo")` is called and another non-archived orchestrator already has `name="foo"`
- **THEN** the method MUST return an error AND the orchestrator id 42's `name` column MUST be unchanged

#### Scenario: Rename to name of archived orchestrator allowed

- **WHEN** `RenameOrchestrator(id=42, newName="foo")` is called and an orchestrator named `foo` exists but has `archived_at IS NOT NULL`
- **THEN** the rename MUST succeed; the archived orchestrator's name MUST be unchanged (collision-free because the new name is in active scope and the existing one is archived)

### Requirement: Roles may be renamed within their orchestrator

The system SHALL support renaming a role via a DAO `RenameRole(id, newName)` method. The new name MUST be unique within the role's orchestrator across non-archived roles. The rename MUST NOT modify any other column, MUST NOT touch the orchestrator row, and MUST NOT affect the bound argus task's name or its worktree.

#### Scenario: Rename role updates only the name column

- **WHEN** `RenameRole(id=7, newName="lead")` is called and no other non-archived role under the same orchestrator is named `lead`
- **THEN** role id 7's `name` MUST be `lead` AND no other column MUST be touched AND no argus HTTP call MUST be issued

#### Scenario: Rename role to existing non-archived sibling name rejected

- **WHEN** `RenameRole(id=7, newName="coord")` is called and another non-archived role under the same orchestrator has `name="coord"`
- **THEN** the method MUST return an error AND role id 7's `name` column MUST be unchanged

#### Scenario: Same role name allowed across different orchestrators

- **WHEN** `RenameRole(id=7, newName="coord")` is called under orchestrator `foo` AND another orchestrator `bar` has a non-archived role named `coord`
- **THEN** the rename MUST succeed (uniqueness is scoped to the role's own orchestrator)

### Requirement: Orchestrators and roles may be archived

The system SHALL support setting `archived_at` on an orchestrator via `ArchiveOrchestrator(id)` and on a role via `ArchiveRole(id)`. Archive MUST be a soft delete: the row, its mission, its constraints, its argus_project (for roles), and all related historical rows (messages, role_status, prior bindings) MUST survive. Conversely, `UnarchiveOrchestrator(id)` and `UnarchiveRole(id)` MUST clear `archived_at` to NULL. Archive timestamps MUST be RFC3339 UTC.

#### Scenario: Archive role preserves identity columns

- **WHEN** a role with `mission="ship F"`, `constraints="ship by friday"`, `argus_project="foo-frontend"` is archived
- **THEN** the role row MUST have `archived_at` set to the current RFC3339 timestamp AND `mission`, `constraints`, `argus_project` MUST be unchanged AND all messages addressed to or from the role MUST remain in the `messages` table

#### Scenario: Unarchive role clears archived_at

- **WHEN** an archived role with `archived_at="2026-05-26T00:00:00Z"` is passed to `UnarchiveRole(id)`
- **THEN** the role's `archived_at` MUST be NULL AND all other columns MUST be unchanged

#### Scenario: Archive orchestrator does not auto-archive roles

- **WHEN** `ArchiveOrchestrator(id)` is called against an orchestrator with active roles
- **THEN** the orchestrator's `archived_at` MUST be set AND the roles' `archived_at` columns MUST be unchanged (the higher-level cascade is the caller's responsibility — see the hera-view rail-operations spec)

### Requirement: Resurrect — fresh task in role's argus_project rebinds an archived role

The system SHALL, when a new argus task is created in an archived role's stored `argus_project` AND that task calls `hera_join(cwd)`, treat the call as a rebind: clear the role's `archived_at` to NULL, create a new binding row linking the task to the role, AND mirror `meta:hera.role=<kind>` to the new task's metadata. The role's `mission`, `constraints`, and accumulated message history MUST survive the resurrect intact. If multiple archived roles in the same orchestrator share the same `argus_project`, hera MUST prefer the role whose most recent prior binding ended most recently; ties MUST resolve to the role with the lowest `id`.

#### Scenario: Bare hera_join in archived role's argus_project resurrects

- **WHEN** orchestrator `foo` has an archived coord role with `argus_project="foo-frontend"` AND a fresh argus task in project `foo-frontend` calls `hera_join(cwd=$PWD)` with no other arguments
- **THEN** hera MUST clear the role's `archived_at` to NULL, insert a new binding row tying the calling task to the role, AND PUT `{key:"role", value:"coordinator"}` to the bound task's `/api/tasks/{id}/meta` endpoint

#### Scenario: Resurrect preserves mission and constraints

- **WHEN** an archived role with `mission="ship F"` and `constraints="ship by friday"` is resurrected by a fresh `hera_join` in its `argus_project`
- **THEN** the role row's `mission` and `constraints` columns MUST remain `"ship F"` and `"ship by friday"` AND the response to `hera_join` MUST surface these values to the caller

#### Scenario: Multiple archived candidates resolve by recency

- **WHEN** orchestrator `foo` has two archived roles `coord` and `lead`, both with `argus_project="foo-frontend"`, AND `coord`'s most recent prior binding ended later than `lead`'s, AND a fresh task in `foo-frontend` calls `hera_join(cwd=$PWD)` with no other arguments
- **THEN** hera MUST rebind to `coord` (most-recent-ended wins)

## MODIFIED Requirements

### Requirement: New orchestrator bootstrap via `hera_new_orchestrator`

The system SHALL provide `hera_new_orchestrator(cwd, name, coordinator_role_name, [mission], [constraints])` as the canonical "be an orchestrator" entry point. The call MUST create the orchestrator row (idempotent on name among non-archived orchestrators), the coordinator role under it (idempotent on (orchestrator, role_name) when the kind matches), a binding row tying the calling argus task to that coordinator role, AND mirror `meta:hera.role=coordinator` to the bound argus task. The call MUST reject if the calling argus task already has a live binding to any role. An archived orchestrator with the same name MUST NOT block creation of a fresh non-archived orchestrator with that name; the archived row remains addressable for resurrect via its argus_project.

#### Scenario: Fresh orchestrator and coordinator created

- **WHEN** `hera_new_orchestrator(cwd=$PWD, name="foo", coordinator_role_name="coord", mission="ship F", constraints="land by friday")` is called from an unbound argus task
- **THEN** hera MUST create orchestrator `foo`, create a coordinator role `coord` with the given mission and constraints, insert a live binding to the calling argus task, AND PUT `{key: "role", value: "coordinator"}` to the bound task's `/api/tasks/{id}/meta` endpoint

#### Scenario: Existing orchestrator with no live coordinator binding resumed

- **WHEN** `hera_new_orchestrator` is called with a `name` that already exists (non-archived) AND the matching coordinator role has no live binding
- **THEN** hera MUST reuse the existing orchestrator + role rows AND create a new binding tying the calling task to that role; the response payload MUST report `created: false`

#### Scenario: Coordinator already live elsewhere

- **WHEN** `hera_new_orchestrator` is called naming an existing non-archived orchestrator + coordinator role whose binding is currently live in another argus task
- **THEN** hera MUST return `isError: true` directing the operator to resume from that worktree via `hera_join`; no new binding MUST be created

#### Scenario: Calling task already bound

- **WHEN** `hera_new_orchestrator` is called from an argus task that already has any live hera binding
- **THEN** hera MUST return `isError: true` directing the operator to resume the existing role via `hera_join(cwd)` instead

#### Scenario: Archived orchestrator with same name does not block creation

- **WHEN** `hera_new_orchestrator(name="foo", ...)` is called from an unbound task AND an orchestrator named `foo` exists with `archived_at IS NOT NULL`
- **THEN** hera MUST create a fresh non-archived orchestrator named `foo`; the archived row MUST be left unchanged and MUST remain available for resurrect via its argus_project
