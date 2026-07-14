# Design — BUG-059 binding identity across a reused worktree path

## Root cause (confirmed against live data)

The `bindings` table enforces two live-uniqueness invariants relevant here:

- `bindings_live_unique_task_orch` — `(argus_task_id, orchestrator_id)` unique where `ended_at IS NULL`.
- `bindings_live_unique_worktree_orch` — `(worktree_path, orchestrator_id)` unique where `ended_at IS NULL`.

Claim mode and the attach/bootstrap reject-guards all resolved "which task is this" via `TaskForCwd` → `argus_task_id`, then looked up `GetLiveByTaskAndOrchestrator`. The binding INSERT, however, is constrained by BOTH indexes. When `cwd` resolved to a task id that differs from the one holding the live binding for that `(worktree_path, orchestrator)`, the task-keyed lookup missed the binding while the worktree-keyed index rejected the INSERT — the exact "claim says none / attach says exists" paradox.

`TaskForCwd` returned the FIRST task whose `worktree_path` matched. Argus reuses a worktree directory across a task's lifecycle when a task name/branch is reused, so two tasks legitimately share one `worktree_path`. Live evidence (`~/.argus/data.sql`, read-only):

| argus task id | name | status | archived | orchestrator | hera binding |
|---|---|---|---|---|---|
| 1782544662696519000 | 5a-verify | in_review | 1 | restore-fork-variants (nuked) | binding 443, **ended** (`user_deleted`) |
| 1784016337631741000 | 5a-verify | in_progress | 0 | sketch-blueprint-comments-apply | binding 709, **live** |

The live binding (709) sits at `(worktree=/…/5a-verify, orch=sketch-blueprint-comments-apply)`. A cwd that resolved to `1782544662696519000` (the stale, first-listed task) made claim miss 709 and attach collide with it.

The same first-match hazard is what let the sibling `argus_clipboard_set(cwd=…)` resolve to the stale 17-day-old task — the tell that discovered this bug. cwd-derived identity is shared across tools, so the fix is at the resolver, not one handler.

## Fix: make both keys agree on the same binding

`worktree_path` equals the caller's `cwd` — physical ground truth — and `(worktree_path, orchestrator_id)` is unique among live bindings. So a worktree-keyed lookup deterministically resolves the one binding an attach INSERT would collide with. Two layers:

1. **Resolver disambiguation (`TaskForCwd`).** Among tasks sharing a worktree_path: one match → return it (unchanged, the common case); else drop archived; if one active remains → it; else prefer the single `in_progress` task (the running caller); else `CwdAmbiguousError`. This stops the resolver from silently returning a stale task and is the primary defense for every caller (including `hera_new_orchestrator`, `hera_spawn_worker` via `CallerRole`, and the sibling clipboard tool once ported).

2. **Task-then-worktree fallback (`LiveBindingForOrch` / `LiveBindingsForTask`).** Try the exact `argus_task_id`; on `ErrNotFound`, resolve by `(worktree_path, orchestrator_id)`. Orchestrator scoping makes this safe: a stale binding under a *different* orchestrator that shares the worktree is never returned. The fallback fires only on a task-keyed miss, so it never double-counts. Claim, `CallerRole`, and the attach guard all route through it, so claim now succeeds precisely when attach would have collided.

Attach and bootstrap additionally pre-check the worktree-keyed binding and return an actionable message (claim it, or `hera_rebind` when the existing binding's `argus_task_id` has drifted) instead of the raw constraint error.

### Why not repair `argus_task_id` during claim?

In the incident the live binding row (709) is already correct — only the *lookup* was wrong. Claim must therefore just resolve the existing binding, never rewrite it. Repair is a separate, explicit act (`hera_rebind`) because it can only be done safely once the caller's real live task is unambiguous.

## `hera_rebind` — the supported repair path

`hera_rebind(cwd, orchestrator, [role_name])` reconciles a stuck binding to the caller's real live task without tearing down the session. Bindings are task↔role incarnation links; the role (and its prompt, messages, and status, all keyed on `role_id`) is durable. Ending the stale binding and inserting a clean one under the same role therefore preserves everything the worker cares about while making both lookup paths agree.

Algorithm:

1. Resolve the caller's real live task via `TaskForCwd` (which now yields the single `in_progress` task or refuses on ambiguity).
2. Gather candidate live bindings under the orchestrator: the union of the task-keyed and worktree-keyed lookups (de-duplicated). Zero candidates → refuse (nothing to repair; use `hera_join`).
3. Pick the keeper role: with `role_name`, it must name a role holding a candidate binding; without it, exactly one role must be represented, else refuse and ask for `role_name`.
4. If the keeper binding already points at the caller's task + worktree and owns both target slots → no-op success.
5. Refuse if a *different* role's live binding holds a target slot (a genuine two-role conflict must not be silently resolved).
6. Otherwise end the keeper's stale binding and `Create` a clean one at `(keeper role, orchestrator, caller task, caller worktree)`; best-effort mirror `meta:hera.role`.

**Refusal (genuinely ambiguous) cases:** two `in_progress` tasks share the worktree; multiple roles bound here with no `role_name`; another role holds a target slot; nothing to reconcile. End + `Create` (rather than UPDATE) reuses the DAO's uniqueness enforcement and event emission.

## Scope notes

- **Not touched: the stale "Seven MCP tools" requirement.** The base `hera-coordination` spec lists seven tools and predates `hera_tree_updates` and `hera_get_messages` (both already registered). Reconciling that count fully is pre-existing drift and out of scope; this change adds `hera_rebind` to the actual registration contract (enforced by `internal/daemon/run_test.go`) and captures its behavior as a new requirement.
- **Live-data repair is not performed from this repo.** The live daemon is native argus (`hera_*`-prefixed tables); this repo is standalone `anutron/hera`. The same buggy logic exists in both. Any live repair must run through the daemon's own fixed code path (a restart on the fix, or the new `hera_rebind` verb once ported) — never a manual SQL edit of the shared `~/.argus/data.sql`.
