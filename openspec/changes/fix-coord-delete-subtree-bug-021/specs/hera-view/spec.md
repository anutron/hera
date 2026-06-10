## MODIFIED Requirements

### Requirement: `^d` deletes a role or cascade-deletes an orchestrator

The system SHALL, on `^d` confirmation against a role, end the role's live binding (if any) with `end_reason="user_deleted"`, set the role's `archived_at` if not already archived, and DESTROY the role's bound argus task via `DELETE /api/tasks/{id}` (argus stops the session and removes the task's git worktree AND branch server-side). The argus task id MUST be resolved from the role's LIVE binding when one exists, FALLING BACK to the role's most recent ENDED binding (by `started_at`) — an archived role's binding has ended (`end_reason='argus_archived'`) while its `argus_task_id` is preserved, so a live-only lookup would skip destroying a still-alive task and orphan it. A task argus reports as already gone (HTTP 404) MUST be treated as success, so a cascade does not abort on a sibling deleted out-of-band. After destroying the task, the system SHALL run `git worktree remove --force <worktree_path>` against the binding's worktree path as a defensive local fallback.

Worktree removal MUST be best-effort: an empty path, a missing directory, a worktree whose `.git` is gone, or a detached git admin entry MUST be a soft no-op (logged and skipped, not an error), AND any other removal failure MUST be logged and skipped rather than aborting — least of all a cascade, where one unremovable worktree would otherwise strand every sibling task.

On `^d` confirmation against an orchestrator, the system SHALL tear down the orchestrator's ENTIRE subtree. It MUST first enumerate every orchestrator reachable from the target via shared sub-coordinator argus tasks (a descendant orchestrator's coordinator role is bound to the same argus task as a role in the parent set), snapshotting the subtree BEFORE any deletion so the live-binding-based traversal is not disturbed by the cascade. For every orchestrator in the subtree the system MUST enumerate ALL of its roles — ACTIVE AND ARCHIVED — and perform the role-level destruction above on each, so an archived (completed) worker's still-alive argus task is destroyed rather than orphaned. The system MUST then PHYSICALLY delete every orchestrator row in the subtree (removing its roles and bindings via `ON DELETE CASCADE`), leaving no row in the DB — neither in the active section nor in the Archive section. This is intentionally more destructive than `a` (which only archives, preserving rows for resurrection): `^d` removes the whole subtree permanently and leaves nothing behind to resurface as a freelancer.

The orchestrator delete confirmation message MUST state the true scope: that the coordinator AND its entire agent subtree (direct children plus sub-coordinators and their descendants, including completed/archived agents) are destroyed — each agent's argus task, git worktree, and branch removed — that nothing is left behind as a freelancer, and that the action cannot be undone.

#### Scenario: Delete role destroys the argus task and removes the worktree

- **WHEN** the operator confirms the `^d` modal against role `foo/worker-1` whose live binding records argus task `T1` and worktree path `/Users/x/.argus/worktrees/foo/worker-1`
- **THEN** hera MUST end the binding with `end_reason="user_deleted"`, set the role's `archived_at`, issue `DELETE /api/tasks/T1`, AND run `git worktree remove --force` against the worktree path

#### Scenario: Delete orchestrator tears down the whole subtree

- **WHEN** the operator confirms the `^d` modal against orchestrator `A` whose worker `wa` became a sub-coordinator for child orchestrator `B` (its task is also `B`'s coordinator), and `B` has workers `wb1` and `wb2`
- **THEN** hera MUST destroy the argus task of every role across `A` and `B` (the coordinator task, `wa`'s shared task, `wb1`'s, and `wb2`'s), AND physically delete BOTH orchestrator rows, so no descendant task resurfaces as a freelancer

#### Scenario: Delete orchestrator destroys an archived child role's still-alive task

- **WHEN** the operator confirms the `^d` modal against orchestrator `foo` that has an ARCHIVED worker role whose ended binding records argus task `Tarchived`, and `Tarchived` is still alive on the argus side
- **THEN** hera MUST issue `DELETE /api/tasks/Tarchived` (resolved via the archived role's latest ended binding) so the task does not survive as a freelancer

#### Scenario: Worktree removal failure does not abort the cascade

- **WHEN** the operator confirms the `^d` modal against an orchestrator and one role's `git worktree remove` fails (e.g. exit 128)
- **THEN** hera MUST log the failure and continue — every other role's argus task MUST still be destroyed and the orchestrator row(s) MUST still be deleted

#### Scenario: Worktree missing is soft no-op

- **WHEN** a deleted role's binding has an empty worktree path or the worktree directory does not exist on disk
- **THEN** the `git worktree remove` step MUST be logged and skipped, not treated as an error
