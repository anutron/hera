# Design: hera project discovery

## Context

Project discovery already exists inside hera but is not exposed to agents. `internal/view/mutations.go` calls `svc.ListProjects(ctx) []string` to populate the new-coord and new-worker modal selectors; that resolves to `argus.Client.ListProjects` (GET `/api/projects`, names only). Argus also serves GET `/api/projects/full`, which returns each project's `name`, `path`, `branch`, `backend`, and sandbox overrides (verified in argus `internal/api/handlers.go` `projectJSON` / `handleListProjectsFull`). Iris already consumes `/api/projects/full` for name+path. Both `/api/projects` and `/api/projects/full` are readable by any authenticated scope token, so hera's scope token works.

## Decisions

### D1 — One source of truth: `ListProjectsFull`

Add `argus.Client.ListProjectsFull(ctx) ([]Project, error)` hitting GET `/api/projects/full`, where `Project{Name, Branch, Backend}`. Refactor the existing `ListProjects` (names) to derive its result from `ListProjectsFull` so every project-discovery path — modals, `hera_projects`, spawn validation — flows through one endpoint and one parse. Path and sandbox fields are intentionally omitted from hera's `Project`: hera exposes only what composes with `hera_spawn_worker` (project/branch/backend) and avoids surfacing absolute filesystem paths.

### D2 — Up-front, best-effort validation in `hera_spawn_worker`

After the project is resolved (input override, else the coordinator's own `argus_project`), validate it against `ListProjectsFull` BEFORE calling argus `CreateTask`. An unknown project returns a hera-owned error listing the valid names. If the list fetch itself errors (argus momentarily unreachable), the handler logs a warning and falls through to `CreateTask` rather than hard-blocking — a discovery hiccup must not make spawning strictly worse than today, and `CreateTask` surfaces its own error if argus is truly down. Validation runs only after the existing coordinator-kind gate, so the error ordering (non-coordinator rejected first) is unchanged.

### D3 — `hera_projects` is read-only and caller-agnostic

`hera_projects` performs no role resolution and has no side effects. Unlike the role-scoped tools, it does not require a resolvable `cwd`→task mapping: project discovery is global, and the whole point is to call it before you are sure which project you belong to. `cwd` is accepted in the schema (harness uniformity, every tool is invoked with `$PWD`) but unused. The handler runs the standard `LinkGate` preamble so a down/recovering argus link yields a clean, consistent error instead of a raw HTTP failure.

### D4 — Output shape

`hera_projects` returns `{projects: [{name, branch?, backend?}], count}`. Empty `branch`/`backend` mean the project inherits argus's global defaults; they are omitted from the JSON (`omitempty`) so the payload reads cleanly. `count` mirrors the convention used by `hera_inbox`.

## Acceptance criteria

- `hera_spawn_worker` with a project not in the configured list returns `isError: true` whose text names the bad project AND lists the valid names.
- `hera_spawn_worker` with a known project (or when the list fetch errors) proceeds to create the task as before.
- `hera_projects` returns every configured project with name + configured branch/backend, sets `count`, and makes no writes.
- The daemon registers `hera_projects` as an MCP tool on startup.

## Out of scope

- Reconciling the stale "Seven MCP tools" count requirement and back-filling specs for the already-shipped `hera_tree_updates` / `hera_get_messages` (pre-existing drift).
- Wiring the modals to display per-project default backend (the data is now available via `ListProjectsFull`; the UI change is a separate follow-up).
