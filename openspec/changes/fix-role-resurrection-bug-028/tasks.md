# Tasks

## 1. Implementation

- [x] 1.1 Add `ops.ResurrectRole(roleID)` (`internal/view/ops/resurrect.go`): load the role, reject an empty `argus_project` as a system error, create a fresh argus task in the role's project (named after the role, kind-appropriate meta), end the role's stale live binding, insert a NEW binding to the SAME role id, auto-submit the prompt via CR. Born-bound; preserves role identity.
- [x] 1.2 Add `resurrectPrompt` / `coordNameFor` helpers: re-wrap the role's stored prompt in the coordinator orientation (orchestrator name) or worker orientation (coordinator name), best-effort with safe fallbacks.
- [x] 1.3 Add `ops.ResurrectRoleResult` (`internal/view/ops/types.go`) carrying the preserved role id + fresh argus task id.
- [x] 1.4 Add `ResurrectRole` to the `mutationService` interface (`internal/view/mutations.go`).
- [x] 1.5 Add `offerReviveOrDelete`: a three-way picker (revive / delete / esc) run off the event loop via `goUI` (NOT `mutate`, mirroring `offerDeleteOrphaned`); revive routes to `ResurrectRole`, shows the splash on the fresh task, notifies the reattach notifier, and queue-selects the row.
- [x] 1.6 Route both `OnReattach` worktree-missing branches (mixed-coord header with a coord role, and managed dead-session role) to `offerReviveOrDelete`; fall back to the BUG-020 delete-only `offerDeleteOrphaned` when revive is impossible (no coord role / freelancer).

## 2. Tests

- [x] 2.1 `ResurrectRole` on a worktree-missing worker: fresh task in the role's project, stale binding ended, new live binding to the same role id, CR auto-submitted, role identity preserved.
- [x] 2.2 `ResurrectRole` on a coordinator: coordinator orientation, new binding to the same coord role id under its orchestrator.
- [x] 2.3 `ResurrectRole` rejects an empty `argus_project` as a system (non-validation) error and creates no task; tolerates a role with no prior binding.
- [x] 2.4 `OnReattach` worktree-missing (mixed-coord header + worker): offers the revive/delete picker; revive routes to `ResurrectRole` (+ splash + select), delete routes to the delete path, esc does neither, revive failure surfaces its error.
- [x] 2.5 `OnReattach` worktree-missing header with no coord role falls back to the delete-only confirm.
- [x] 2.6 `make test` passes with `-race`.
