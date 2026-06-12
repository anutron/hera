## Why

An agent calling `hera_spawn_worker(project="argus", ...)` gets back an opaque `HTTP 500: project "argus" not found in config` from argus, with no way to discover the valid project names. The real name was `ARGUS`; the agent only learned that by falling back to `ls ~/.argus/worktrees/`. Hera's daemon already knows the configured argus project list (it populates the project selector in the new-coordinator / new-worker modals), but never exposes it to agents.

Two gaps:

1. `hera_spawn_worker` forwards an unknown project straight to argus, so the error is argus's, opaque, and offers no alternatives.
2. There is no read-only way for an agent to ask hera "what projects can I spawn into?" before spawning.

## What Changes

- `hera_spawn_worker` validates the resolved `project` against hera's known argus project list UP FRONT (before POSTing to argus). An unknown project returns a hera-owned error that LISTS the valid project names, e.g. `project "argus" not found; valid projects: ARGUS, Hera, Iris`. The validation is best-effort: if the project list cannot be fetched (transient argus unavailability), the handler falls through to the existing argus CreateTask path rather than hard-blocking.
- A new read-only MCP tool `hera_projects` lists every configured argus project with its name and, where configured, its default branch and default backend. No side effects, no role resolution required.
- A single client method, `argus.Client.ListProjectsFull` (GET `/api/projects/full`), becomes the one source of truth for project discovery: `hera_projects`, the spawn-worker validation, and the existing `ListProjects` name list (which feeds the modals) all derive from it.

`hera_new_orchestrator` has no explicit `project` parameter — it bootstraps an orchestrator by name and roles record their `argus_project` at first binding — so it needs no equivalent validation. Only `hera_spawn_worker` takes a `project`.

## Capabilities

### New Capabilities

_(none)_

### Modified Capabilities

- `hera-coordination`: Adds the `hera_projects` read-only discovery tool and the `hera_spawn_worker` up-front project validation with a self-correcting error.

## Impact

- `internal/argus/projects.go`: New file — `Project` type and `ListProjectsFull` (GET `/api/projects/full`).
- `internal/argus/tasks.go`: `ListProjects` refactored to derive names from `ListProjectsFull` so all discovery flows through one endpoint.
- `internal/mcp/handler_projects.go`: New file — `ProjectsHandler` implementing `hera_projects`.
- `internal/mcp/handler_spawn_worker.go`: Add up-front project validation with an enriched error.
- `internal/daemon/run.go`: Register `hera_projects` handler + tool definition.
- No schema changes.

## Notes / pre-existing drift

The base `hera-coordination` spec's "Seven MCP tools" requirement enumerates seven tools, but the daemon already registers nine (it also registers `hera_tree_updates` and `hera_get_messages`, which lack spec coverage). This change adds `hera_projects` as a tenth. Reconciling that count requirement and back-filling the two undocumented tools is pre-existing drift, out of scope here, and flagged for a follow-up.
