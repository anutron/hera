## ADDED Requirements

### Requirement: Binding identity resolves consistently across a reused worktree path

A `worktree_path` is NOT unique across a task's full lifecycle: argus reuses a worktree directory when a task name / branch is reused after the prior task moved to `in_review` / `complete` / archived without its worktree being cleared. The system SHALL resolve a caller's hera identity so that the claim lookup and the attach/bootstrap uniqueness NEVER disagree about "which task is this," even when two argus tasks share one `worktree_path`.

Resolving a `cwd` to an argus task SHALL disambiguate a shared worktree rather than returning the first match:

- exactly one task matches the `worktree_path` → that task (the common case);
- otherwise, archived matches SHALL be dropped; if exactly one non-archived match remains → that task;
- otherwise, if exactly one non-archived match is `in_progress` → that task (the running session making the call);
- otherwise (all matches archived, or two or more equally-plausible live matches) → the call SHALL fail with an error naming the candidate tasks, rather than silently binding identity to a guessed task.

Binding lookups (claim, the coordinator-role resolution used by message/status/spawn tools, and the attach/bootstrap reject-guards) SHALL key first on the resolved `argus_task_id` and, on a miss, SHALL fall back to a lookup keyed on `(worktree_path, orchestrator_id)` — the same key the live-binding uniqueness is defined on. The fallback SHALL be scoped to the orchestrator in question so a stale binding under a different orchestrator that shares the worktree is never returned. Because the fallback resolves the exact binding an attach INSERT would collide with, a claim SHALL succeed precisely when an attach would have been rejected.

A claim SHALL NOT rewrite the resolved binding's `argus_task_id`; it only returns the existing binding's identity. Repairing a binding whose own `argus_task_id` has drifted is a separate explicit act (`hera_rebind`).

An attach or bootstrap that detects an existing live binding for the caller's `(worktree_path, orchestrator)` SHALL reject with an actionable message (claim the existing binding, or use `hera_rebind` when the existing binding's `argus_task_id` differs from the caller's resolved task) instead of surfacing a raw database `UNIQUE constraint failed` error.

#### Scenario: cwd prefers the in_progress task over a stale archived task sharing the worktree

- **WHEN** two argus tasks share one `worktree_path` — one archived / `in_review`, one live `in_progress` — and a hera tool is invoked with that `cwd`
- **THEN** resolution MUST select the `in_progress` task, so identity keys off the live task and not the stale one

#### Scenario: cwd with two live in_progress tasks is refused, not guessed

- **WHEN** two `in_progress` argus tasks share one `worktree_path` and a hera tool is invoked with that `cwd`
- **THEN** the call MUST return `isError: true` naming the candidate tasks, and MUST NOT bind identity to either

#### Scenario: Claim resolves the worktree binding when cwd resolved a colliding task id

- **WHEN** `hera_join(cwd, orchestrator=X)` claim is called and the resolved `argus_task_id` has no live binding under `X`, but a live binding for `X` exists at the caller's `worktree_path`
- **THEN** hera MUST return that binding's role identity (the worktree-keyed fallback), rather than reporting that no binding exists

#### Scenario: Attach on a worktree collision returns a friendly message, not a constraint error

- **WHEN** `hera_join(cwd, orchestrator=X, role_name=…, kind=…)` attach is called and a live binding for `X` already exists at the caller's `worktree_path`
- **THEN** hera MUST return `isError: true` with content directing the caller to claim the existing binding (or to `hera_rebind` when the existing binding's `argus_task_id` has drifted), and MUST NOT surface a raw `UNIQUE constraint failed` message

### Requirement: Binding repair via `hera_rebind`

The system SHALL provide `hera_rebind(cwd, orchestrator, [role_name])` as the supported repair path for a hera binding stuck in the claim-says-none / attach-says-exists state (a reused worktree path left the live binding pointing at a stale argus task, so delivery and status routing — which key on the binding's `argus_task_id` — go nowhere). `hera_rebind` SHALL reconcile the binding to the caller's real live argus task WITHOUT tearing down the argus session: it reuses the existing role (so the role's prompt, messages, and status, all keyed on `role_id`, survive) and refreshes only the binding row.

`hera_rebind` SHALL resolve the caller's real live task from `cwd` (using the shared-worktree disambiguation above), then reconcile the live binding for the named orchestrator so that both the task-keyed and worktree-keyed lookups resolve the SAME single binding, whose `argus_task_id` and `worktree_path` are the caller's. When the binding is already consistent, the call SHALL be a no-op that reports success without changing any row. On repair, `hera_rebind` SHALL end the stale binding and insert one clean binding under the keeper role, and SHALL best-effort mirror `meta:hera.role` to the caller's task.

`hera_rebind` SHALL REFUSE (return `isError: true`, change no rows) rather than guess when the state is genuinely ambiguous: two live `in_progress` tasks share the worktree; more than one role holds a live binding at the caller's worktree or task and no `role_name` is supplied to pick one; a different role's live binding already occupies the caller's target task or worktree slot; or there is no live binding to reconcile at all (in which case the caller is directed to `hera_join`). `hera_rebind` SHALL NOT create a binding where none existed.

#### Scenario: Rebind reconciles a drifted binding to the caller's live task

- **WHEN** `hera_rebind(cwd, orchestrator=X)` is called and the live binding for `X` at the caller's worktree points at a stale `argus_task_id` while the caller's real task is a single `in_progress` task at that worktree
- **THEN** hera MUST end the stale binding and insert one clean binding under the same role with the caller's `argus_task_id` and `worktree_path`, MUST leave both the task-keyed and worktree-keyed lookups resolving that one binding, and MUST preserve the role's messages

#### Scenario: Rebind is a no-op when the binding is already consistent

- **WHEN** `hera_rebind(cwd, orchestrator=X)` is called and the live binding already points at the caller's task and worktree
- **THEN** hera MUST report success without changing any binding row (the reconciled flag is false and the binding id is unchanged)

#### Scenario: Rebind refuses an ambiguous cwd

- **WHEN** `hera_rebind(cwd, orchestrator=X)` is called and two live `in_progress` argus tasks share the caller's worktree
- **THEN** hera MUST return `isError: true` naming the candidates and MUST NOT change any binding row

#### Scenario: Rebind refuses when multiple roles are bound and no role_name is given

- **WHEN** `hera_rebind(cwd, orchestrator=X)` is called, the caller's task and worktree are bound to DIFFERENT roles under `X`, and no `role_name` is supplied
- **THEN** hera MUST return `isError: true` listing the candidate roles and directing the caller to pass `role_name`, and MUST NOT change any binding row

#### Scenario: Rebind refuses when there is nothing to reconcile

- **WHEN** `hera_rebind(cwd, orchestrator=X)` is called and no live binding for `X` exists at the caller's worktree or task
- **THEN** hera MUST return `isError: true` directing the caller to `hera_join`, and MUST NOT create a binding
