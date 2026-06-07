## Context

Hera's bindings table today encodes a strict invariant: at most one live binding per argus task. The invariant is enforced both at the application layer (every attach handler does `Bindings.GetLiveByTaskID(task)` and rejects on hit) and at the storage layer (partial unique index `bindings_live_unique_task ON bindings(argus_task_id) WHERE ended_at IS NULL`).

The invariant is too tight. Nested orchestration — a worker in orch A spawning its own orch B as coord — requires the same argus task to simultaneously hold a worker binding in A and a coordinator binding in B. The roles are distinct entities under distinct orchestrators; nothing about the task itself precludes incarnating both.

This change relaxes the invariant from "one binding per task" to "one binding per (task, orchestrator)". Two bindings in the same orchestrator on the same task would still make no sense (a task can't be both worker and coord of the same project), so we keep uniqueness there.

## Goals / Non-Goals

### Goals

1. A single argus task can hold N live bindings, one per orchestrator.
2. Existing single-binding flows are unchanged — no caller is forced to start passing `orchestrator` if their task only has one binding.
3. The schema migration is one-step, additive (new column + index swap), and back-fills correctly from existing data.
4. Cross-orchestrator message routing remains forbidden — the existing "messages stay within the sender's orchestrator" guarantee is preserved.

### Non-Goals

- No new tools. We keep the existing six MCP tools; just extend their input schemas.
- No change to role-side uniqueness — a role still has at most one live binding.
- No change to settings, idle gating, message delivery modes, or the event-cursor mechanics.
- Multi-binding hera-view: out of scope. The view will continue to show the first binding it finds today; a follow-up change will surface multi-binding state in the UI.
- A "claim ALL my bindings at once" tool — not needed; bare `hera_join` already serves the common case and the multi-binding case is rare enough to specify the orchestrator explicitly.

## Decisions

### D1: Add `orchestrator_id` to the bindings table (not just denormalize-on-read)

Storing `orchestrator_id` directly on the binding row lets us push the uniqueness invariant down to the DB layer as a partial unique index on `(argus_task_id, orchestrator_id) WHERE ended_at IS NULL`. The alternative — leave the column off and rely on application-level pre-checks — was rejected because every existing attach handler has a TOCTOU race window (pre-check, then INSERT) that a DB-level partial unique index closes deterministically (the INSERT fails). Migration 0002 added the same defense-in-depth invariant for the old "one binding per task" rule; we keep the pattern.

The column is `NOT NULL` in spirit but added as nullable then backfilled in a single migration step (SQLite doesn't easily make a new column `NOT NULL` without table recreation; we instead enforce non-null at the DAO insert).

### D2: Migration drops the orchestrator-agnostic UNIQUE indexes, keeps the non-unique by-task and by-worktree indexes

Migration 0002 created:

```sql
CREATE UNIQUE INDEX bindings_live_unique_task ON bindings(argus_task_id) WHERE ended_at IS NULL;
CREATE UNIQUE INDEX bindings_live_unique_role ON bindings(role_id) WHERE ended_at IS NULL;
CREATE UNIQUE INDEX bindings_live_unique_worktree ON bindings(worktree_path) WHERE ended_at IS NULL;
```

We drop the `_task` and `_worktree` unique indexes (they would now be wrong — they'd reject the second binding for the same task) and add:

```sql
CREATE UNIQUE INDEX bindings_live_unique_task_orch
    ON bindings(argus_task_id, orchestrator_id) WHERE ended_at IS NULL;
CREATE UNIQUE INDEX bindings_live_unique_worktree_orch
    ON bindings(worktree_path, orchestrator_id) WHERE ended_at IS NULL;
```

We keep `bindings_live_unique_role` exactly as-is — a role still incarnates at most once. We also keep the non-unique helper indexes `bindings_by_task` and `bindings_by_worktree` for the "any binding for this task?" lookups used by the auto-adopt and resync paths.

### D3: `Bindings` DAO additions

```go
type CreateBindingInput struct {
    RoleID         int64
    OrchestratorID int64  // NEW: required, derived by handlers from the role
    ArgusTaskID    string
    WorktreePath   string
}

// NEW: orchestrator-scoped single-binding lookup (the primary handler path).
func (b *BindingsDAO) GetLiveByTaskAndOrchestrator(ctx context.Context, taskID string, orchID int64) (*Binding, error)

// NEW: list all live bindings for a task (the multi-binding case).
func (b *BindingsDAO) ListLiveByTaskID(ctx context.Context, taskID string) ([]*Binding, error)

// KEPT: GetLiveByTaskID returns the single binding if the task has exactly one,
// ErrAmbiguous if 2+, ErrNotFound if 0. Callers that already only want the
// single-binding case continue to use it; the new ErrAmbiguous lets them surface
// a clean error rather than silently pick.
func (b *BindingsDAO) GetLiveByTaskID(ctx context.Context, taskID string) (*Binding, error)
```

`ErrAmbiguous` is a new sentinel returned in addition to `ErrNotFound`. Existing tests that depend on `GetLiveByTaskID` returning a single binding are unaffected because every test scenario uses a single-binding setup; the new sentinel surfaces only when multi-binding is set up.

### D4: Resolver gains an `orchestrator` hint

```go
// CallerRole(cwd, orchestrator) — orchestrator may be empty.
//   - empty + exactly one binding: returns that binding's role (back-compat)
//   - empty + zero bindings: ErrNoBinding
//   - empty + 2+ bindings: ErrAmbiguousBinding with list of orchestrators
//   - non-empty: looks up the binding scoped to that orchestrator; ErrNotFound if none.
func (r *Resolver) CallerRole(ctx context.Context, cwd, orchestrator string) (*argus.Task, *db.Role, *db.Binding, error)
```

Every handler that previously called `CallerRole(ctx, cwd)` is updated to pass through the optional `orchestrator` input. The error returns are mapped to MCP error responses with operator-friendly hints (the ambiguity error literally lists the orchestrator names so the operator can pick).

### D5: `hera_new_orchestrator` rejection rule

Old rule: reject if the calling task has any live binding.

New rule: reject if the calling task already has a live binding to the orchestrator named in the call. That means:

- A task with no bindings can call `hera_new_orchestrator("foo", ...)` — same as before.
- A task that's a worker in `foo` cannot call `hera_new_orchestrator("foo", ...)` again — would race against its existing binding under the same orchestrator.
- A task that's a worker in `foo` CAN call `hera_new_orchestrator("bar", ...)` — adds a binding to `bar` alongside its existing `foo` binding. This is the new capability.

The existing "coordinator role already live elsewhere" check is unchanged — it's keyed on the role's live binding, not on the calling task's bindings.

### D6: `hera_join` attach rejection rule

Old rule: reject if the calling task has any live binding.

New rule: reject if the calling task already has a live binding to the orchestrator named in the call. Same rationale as D5. The "role exists with a different kind" check is keyed on `(orchestrator, role_name)` and is unchanged.

### D7: Bare `hera_join` re-incarnation when 2+ bindings

```
hera_join(cwd)
  bindings count = 0 -> error: "not bound; use attach signature or hera_new_orchestrator"
  bindings count = 1 -> return the binding's role (today's behavior)
  bindings count = 2+ -> error listing each binding's (orchestrator, role_name, kind); operator picks one via hera_join(cwd, orchestrator=X)

hera_join(cwd, orchestrator=X)
  with no role_name + no kind -> claim mode: return the binding for orch X, or error if none.
  with role_name + kind        -> attach mode: subject to D6 rejection rule.
```

Detection between claim mode and attach mode is unchanged from today (look for `role_name` or `kind` to decide).

### D8: `hera_send` ambiguity resolution

```
hera_send(cwd, body, [to=, in_reply_to=, orchestrator=])
  resolve caller role via CallerRole(cwd, orchestrator):
    ambiguous (2+ bindings, no orchestrator) -> error listing orchestrators
    resolved single role -> existing send/routing logic with that role as the sender
  recipient (to=) is looked up in the sender's resolved orchestrator (unchanged)
  default route (no to=) routes to the coordinator of the sender's resolved orchestrator (unchanged)
```

Coordinator-must-supply-to and recipient-must-exist-in-same-orchestrator rules are unchanged.

### D9: `hera_inbox`, `hera_mark_read`, `hera_status` ambiguity resolution

Same pattern: each takes an optional `orchestrator` string; passed to `CallerRole`. Inbox returns messages for the resolved role; `mark_read` updates rows owned by that role; `status` writes the resolved role's status (and mirrors `meta:hera.thread_status` to the bound argus task — that's a per-task meta key, NOT per-orchestrator-per-task, so two bindings on the same task both writing `thread_status` would overwrite each other. For v1.2 we accept that the last writer wins; a follow-up could namespace the meta key by orchestrator).

### D10: Auto-adopt parent disambiguation

The `link.created` handler today uses `GetLiveByTaskID(parent)` to fetch the parent's binding and checks if the parent's role is a coordinator. With multi-binding, the parent task may have 2+ live bindings. Rule:

1. List all live bindings for the parent.
2. Resolve each binding's role; filter to roles of kind `coordinator`.
3. If exactly one coordinator binding: adopt the child under that orchestrator.
4. If zero coordinator bindings: skip (today's behavior — parent isn't a coordinator).
5. If 2+ coordinator bindings: log a WARN and skip the adoption. The operator must explicitly attach the child via `hera_join` to disambiguate. (A future enhancement could read `meta:hera.parent_orchestrator` from the child to disambiguate; for v1.2 we keep it simple.)

### D11: `task.archived` ends every live binding

Today the handler ends the (assumed single) binding. With multi-binding, the handler must end every live binding for the archived task. Concretely: replace the single-row `End(bindingID)` with a loop over `ListLiveByTaskID(taskID)` ending each. End-reason stays `argus_archived` for every row.

## Risks / Trade-offs

- **Meta:hera.thread_status overwrites on multi-binding tasks** (D9). Acceptable — the meta key is observability for the argus task list view; the source of truth is the per-role `role_status` row. Mitigation: document; a follow-up can namespace.
- **Multi-coordinator-on-same-task adoption skip** (D10). Acceptable — the pattern is extreme and operator-recoverable via `hera_join`. Mitigation: log a clear WARN naming both orchestrators.
- **Migration backfill correctness**. Mitigation: backfill uses `SELECT orchestrator_id FROM roles WHERE roles.id = bindings.role_id`; the role row is guaranteed to exist by the FK; the test suite explicitly exercises the migration against pre-multi-binding data.
- **Downgrade hazard**. If the new schema is rolled back (re-add the old single-binding unique indexes), any rows with 2+ live bindings for the same task would fail the constraint. Mitigation: document as a known limitation; the migration is functionally reversible only on databases that never accumulated multi-binding rows. The risk is low — the rollback path was never a hard guarantee for hera migrations.

## Migration Plan

1. **Schema migration 0004** runs once on daemon start. The migration:
   1. Adds `bindings.orchestrator_id INTEGER`.
   2. Backfills `UPDATE bindings SET orchestrator_id = (SELECT orchestrator_id FROM roles WHERE roles.id = bindings.role_id)`.
   3. Drops the `bindings_live_unique_task` and `bindings_live_unique_worktree` indexes.
   4. Creates the orchestrator-scoped replacements.
2. **DAO + handler changes** land in the same commit so behavior matches the schema.
3. **Tests** run against an in-memory DB seeded with single-binding rows and exercised through the migration; verify orchestrator_id is non-NULL on every row.

## Open Questions

None outstanding. The brainstorm rules above resolve every fork the implementation has to make.
