# Re-parent a coordinator with `J` regardless of coord session liveness (BUG-025)

## Why

BUG-024 added `J` re-parenting of a coordinator under another coordinator, but it only fires when the coordinator has a LIVE coordinator binding. It derives the child's worktree from the live binding, and the routing guard (`coordAdoptTarget`) requires a non-empty live coord task on the selection. Selecting a coordinator whose session is not live — for example an old sub-coordinator like `2a-team` whose coord task has ended — falls through to the generic "J: select a freelancer or a live coordinator…" notice. The operator cannot reorganize a dormant coordinator into the hierarchy.

Re-parenting is a STRUCTURAL operation. It links the parent's new worker role to the child coordinator's argus TASK, which exists independent of whether the child's coord session is currently running. The coord task id and worktree are both recoverable from the coordinator's coordinator role's LATEST binding (live OR ended) — the same fallback BUG-021's subtree teardown already uses to recover an archived role's argus task. There is no reason to require liveness.

## What Changes

- **`J` re-parents a coordinator even when its coord session is not live.** The op resolves the child's coordinator argus task id + worktree from the child orchestrator's coordinator role's LATEST binding (live OR ended), instead of the live-only binding. A dormant coordinator whose coord task has ended is now re-parentable; its task id and worktree come from the most-recent ended binding.
- **The routing guard loosens.** A coordinator selection routes to coord-adopt when the orchestrator HAS a coordinator role (live or dead), not only when it has a live coord task. Only an orchestrator that NEVER had a coordinator role at all falls through to the not-applicable notice.
- **Live-coordinator behavior is unchanged.** A coordinator with a live coord session re-parents exactly as before (the latest binding is then the live one).
- **The cycle guard is unchanged.** Re-parenting a coordinator under itself or any descendant is still rejected (`SubtreeOrchIDs`).
- **The not-applicable notice drops "live".** It now reads "select a freelancer or a coordinator to adopt into a coordinator", since liveness is no longer required.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the `J` adopt requirement — a coordinator selection re-parents regardless of coord session liveness, resolving the child's coordinator argus task id + worktree from the latest binding (live or ended); only an orchestrator with no coordinator role at all falls through.

## Impact

- `internal/view/ops/reparent.go` — `ReparentCoordinator` resolves the child's coordinator argus task id + worktree from the coord role's LATEST binding (live or ended) via `GetLatestBindingByRole`, instead of requiring a live binding; the prior-parent-linkage teardown still keys off live bindings.
- `internal/view/mutations.go` — `coordAdoptTarget` routes an orchestrator-header selection to coord-adopt on `CoordRoleID != 0` (coordinator role exists) rather than `CoordTaskID != ""` (live coord task); the `J` not-applicable notice drops "live".
- `internal/view/ops/reparent_test.go` — the dormant-coordinator case inverts from rejected to succeeds-from-ended-binding; coverage that the worktree is recovered from the ended binding.
- `internal/view/mutations_test.go` — coverage that a coordinator header with no live coord task still routes to the coordinator picker.
