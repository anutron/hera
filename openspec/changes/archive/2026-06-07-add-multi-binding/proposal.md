## Why

Hera 1.x binds each argus task to exactly **one** hera role: the `bindings` table has a partial unique index on `argus_task_id WHERE ended_at IS NULL`, and every attach handler (`hera_join`, `hera_new_orchestrator`) rejects with an error if the calling task already has a live binding. The same is enforced for `worktree_path`.

That assumption blocks the nested-orchestration pattern we need next: an argus task that is a worker in orchestrator A wants to also be a coordinator in orchestrator B (its own sub-team). Today that requires forfeiting the A-binding, which destroys the A-relationship — messages from A can no longer auto-route to a coordinator, the worker can no longer reach back to A's coord, and A loses its handle on the worker.

The concrete trigger is the Sherlock 4.0 rebuild: the top-level coord (`sherlock-mvp/coord`) spawns wave workers like `1a-add-knowledge-svc`; each wave worker must in turn coordinate its own implementer team under a fresh orchestrator. That requires a single argus task to hold N bindings at once, one per orchestrator.

## What Changes

- **Bindings keyed by `(argus_task_id, orchestrator_id)` instead of `argus_task_id`** — a task may have at most one live binding **per orchestrator**, but may have multiple live bindings across different orchestrators. Same for `worktree_path`. Role-side uniqueness (one live binding per role) stays unchanged — a role is still incarnated at most once.

- **New schema migration `0004_bindings_per_orchestrator`** — adds a non-null `orchestrator_id` column to the `bindings` table (backfilled from each binding's role at migration time), drops the orchestrator-agnostic partial unique indexes on `argus_task_id` and `worktree_path`, and replaces them with orchestrator-scoped partial unique indexes.

- **`hera_new_orchestrator` rejection narrowed** — instead of "this task is already bound", reject only when the calling task already has a live binding **to the orchestrator being created**.

- **`hera_join` attach rejection narrowed** — same: reject only when the calling task already has a live binding **to the orchestrator being joined**.

- **Bare `hera_join(cwd)` becomes binding-count-aware** — if the calling task has exactly one live binding, return it (today's behavior). If two or more, return an error listing the available orchestrators and require the caller to specify which to claim via `hera_join(cwd, orchestrator=X)`. With explicit `orchestrator` and no other attach args, hera resolves the binding for that orchestrator.

- **`hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status` take an optional `orchestrator` parameter** — disambiguates the caller's binding when the task has 2+ live bindings. Default to the caller's only binding when there is exactly one (single-binding callers see no API change). Cross-orchestrator messaging remains forbidden: a `to:` is still resolved within the resolved caller's orchestrator.

- **Auto-adoption disambiguates parent orchestrator** — when `link.created` fires for a new task and the parent task has multiple live bindings, hera picks the (single) coordinator binding among the parent's bindings; logs and skips if the parent has zero or two-plus coordinator bindings.

- **`task.archived` ends every live binding for the archived task** — not just the (formerly single) binding.

- **Backwards compatibility** — every existing single-binding flow continues to work unchanged: tool calls without `orchestrator` resolve via "exactly one binding" defaulting; the migration backfills `orchestrator_id` from each existing binding's role; the role-side uniqueness invariant is unchanged.

## Capabilities

### Modified Capabilities

- `hera-coordination`: replaces the one-binding-per-task invariant with one-binding-per-(task, orchestrator); rewrites the `hera_new_orchestrator` and `hera_join` rejection rules; adds the optional `orchestrator` parameter to `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`; updates auto-adopt parent disambiguation and archive-cleanup semantics.

## Impact

- **Schema migration**: additive column + index swap. Reversible by re-adding the old global unique indexes (which would fail if multi-binding rows already exist — operators with multi-binding rows must end the extra bindings before downgrading).
- **Daemon callers**: `Bindings.GetLiveByTaskID` continues to work for the common single-binding case but gains a sibling `ListLiveByTaskID` for the multi-binding case and `GetLiveByTaskAndOrchestrator(task, orch)` for the orchestrator-scoped lookup that handlers now use. Existing call sites are migrated.
- **Tool schema changes**: every tool gains an optional `orchestrator` field; absent-and-ambiguous calls return informative errors listing available orchestrators.
- **Tests**: every existing single-binding test continues to pass; new multi-binding tests cover the join/send/inbox/status disambiguation and the adoption/archive cleanup edges.
