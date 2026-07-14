# Binding identity resolves consistently across a reused worktree path (BUG-059)

## Why

A born-bound worker (materialized from a plan-DAG node) could get permanently stuck: it could neither claim its binding nor attach a new one, and the two failures contradicted each other.

- `hera_join(cwd, orchestrator=X)` in claim mode reported **no binding exists**.
- `hera_join(cwd, orchestrator=X, role_name=…, kind=…)` in attach mode failed with **`UNIQUE constraint failed: bindings.worktree_path, orchestrator_id`** — a binding already exists.

Both statements were about the same `(worktree_path, orchestrator_id)` pair. The root cause is that identity is resolved through two different keys:

- Claim/attach lookups key on **`argus_task_id`**, resolved from `cwd` via `TaskForCwd`.
- The binding INSERT's uniqueness is enforced on **`worktree_path`**.

`worktree_path` is not a stable unique key across a task's full lifecycle. Argus reuses a worktree directory when a task **name / branch** is reused after the prior task moved to `in_review` / `complete` / archived without its worktree being cleared. Live evidence: two argus tasks share `/Users/aaron/.argus/worktrees/Sketch/5a-verify` — a stale `in_review`/archived task (`1782544662696519000`, orchestrator `restore-fork-variants`) and the live `in_progress` worker (`1784016337631741000`, orchestrator `sketch-blueprint-comments-apply`). `TaskForCwd` returned the first match, so a cwd could resolve to the **stale** task id: the task-keyed claim then missed the live binding that the worktree-keyed uniqueness nonetheless rejected on attach.

The human operating the parent coordinator explicitly wants a **supported repair path** — not "spawn a replacement worker and cancel the plan node," which discards the stuck worker's live session and context.

This is the third recurrence of the plan-node/binding race family (early materialization, fixed via transactional `hera_plan`; the one-directional binding-lookup failure; now the shared-worktree collision), so the fix matches that precedent's rigor: a deterministic invariant plus regression tests, not a point patch.

## What Changes

- **cwd resolution disambiguates a shared worktree instead of returning the first match.** With multiple tasks at one `worktree_path`, `TaskForCwd` drops archived tasks and prefers the single `in_progress` task (the running session making the call); it refuses (surfaces an ambiguity error) rather than guess when two live tasks are equally plausible.
- **Identity lookups fall back from task-keyed to worktree-keyed.** Claim, `CallerRole`, and the attach/bootstrap guards try the exact `argus_task_id` first and, on a miss, resolve by `(worktree_path, orchestrator_id)` — the same key the DB uniqueness is defined on. This makes claim succeed exactly when attach would collide, so the two paths can no longer disagree. Orchestrator scoping keeps the fallback from picking up a stale binding under a different orchestrator.
- **Attach and bootstrap return an actionable message on a worktree collision** ("this worktree already holds a live binding … claim it, or hera_rebind if delivery is broken") instead of leaking a raw `UNIQUE constraint failed`.
- **New `hera_rebind` MCP verb** — the supported repair path. It reconciles a stuck/ambiguous binding to the caller's real live argus task so both lookup paths agree, WITHOUT tearing down the argus session (only the binding row is refreshed; the role, and its prompt/messages/status keyed on `role_id`, survive). It refuses when the state is genuinely ambiguous.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-coordination`: adds a cross-cutting identity-resolution invariant (cwd disambiguation + task-then-worktree fallback so claim and attach agree across a reused worktree path) and a new `hera_rebind` binding-repair tool.

## Impact

- `internal/db/bindings.go` — new `GetLiveByWorktreeAndOrchestrator` and `ListLiveByWorktree` (worktree-keyed twins of the task-keyed lookups).
- `internal/mcp/resolve.go` — `TaskForCwd` disambiguates shared-worktree matches; new `CwdAmbiguousError`; new `LiveBindingForOrch` / `LiveBindingsForTask` (task-then-worktree fallback) used by `CallerRole`.
- `internal/mcp/handler_join.go` — claim resolves through the fallback; attach guards on the worktree binding and returns a friendly message.
- `internal/mcp/handler_new_orchestrator.go` — bootstrap guards on the worktree binding too.
- `internal/mcp/handler_rebind.go` — new `hera_rebind` handler.
- `internal/daemon/run.go` — register `hera_rebind` + its tool definition.
- `internal/daemon/run_test.go` — tool-registration contract updated to include `hera_rebind`.
- Tests: `internal/db/bindings_worktree_test.go`, `internal/mcp/binding_collision_test.go`, `internal/mcp/handler_rebind_test.go`.
