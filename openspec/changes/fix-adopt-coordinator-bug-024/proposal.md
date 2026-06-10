# Extend `J` (adopt) to re-parent a coordinator under another coordinator (BUG-024)

## Why

`J` adopts a freelancer (an unmanaged argus task) into a coordinator as a worker. The operator also wants to organize INDEPENDENT coordinators into a hierarchy after the fact — to take a top-level coordinator and nest it under another coordinator as a sub-coordinator. Today there is no rail gesture for this: `J` on a coordinator row gave a "only freelancers can be adopted" notice.

The orchestrator hierarchy is not stored as a column — there is no `parent_orchestrator_id`. Sub-coordinator nesting is EMERGENT at render time from a multi-binding: a worker role in parent `P` whose binding's argus task equals child orchestrator `C`'s coordinator argus task (`resolveSubCoordinators`). The same multi-binding chain backs `SubtreeOrchIDs` (BUG-021). So re-parenting an existing coordinator needs no schema migration — it only needs to create that multi-binding (and tear down a prior one when moving).

## What Changes

- **`J` on a coordinator row re-parents it under a chosen coordinator.** Pressing `J` on a LIVE coordinator — a root orchestrator header OR a promoted sub-coordinator role row — opens the SAME coordinator picker (now excluding the coordinator itself) and, on selection, nests that coordinator under the chosen parent as a sub-coordinator. The coordinator's whole subtree (its roles/workers) moves with it because the subtree is derived from the coordinator, which is untouched — only its parent linkage changes.
- **The re-parent linkage is the multi-binding the rail already renders.** A new `worker` role under the parent is bound to the child coordinator's coordinator argus task (reusing the child's coord worktree). `resolveSubCoordinators` then nests the child under the parent.
- **Moving an already-nested coordinator is clean.** If the coordinator is already nested under some other parent, that prior parent linkage (the worker role + its binding) is ended (`end_reason="reparented"`) and removed first, so the coordinator never appears under two parents.
- **Cycle guard.** Re-parenting is rejected when the chosen parent is the coordinator itself or any of its own descendants (reusing the `SubtreeOrchIDs` subtree walk from BUG-021) — nesting a coordinator under its own subtree would create a cycle.
- **Freelancer-adopt behavior is unchanged.** A freelancer row still creates a worker role + binding via `AdoptTaskIntoOrchestrator`. The `J` not-applicable notice now reads "select a freelancer or a live coordinator".

Design note: the child coordinator's argus task `meta:hera.role` is left untouched (it remains a coordinator in its own orchestrator); only hera-side bindings change.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the `J` adopt requirement now ALSO re-parents a coordinator (root header or sub-coordinator row) under another coordinator via a multi-binding, with a cycle guard and clean move-from-prior-parent, in addition to the existing freelancer adopt.

## Impact

- `internal/view/ops/reparent.go` (new) — `ReparentCoordinator` + `ReparentCoordInput`/`ReparentCoordResult`: cycle guard via `SubtreeOrchIDs`, tear down the prior parent linkage, create the worker role + binding for the child's coord task reusing its coord worktree.
- `internal/view/ops/types.go` — `ops.Binding` gains `OrchestratorID` (needed to tell the child's own coord binding from a parent link binding).
- `internal/view/ops_adapters.go` — `adaptBinding` carries `OrchestratorID`.
- `internal/view/mutations.go` — `mutationService` gains `ReparentCoordinator`; `OnAdopt` routes coordinator selections to a coordinator picker that excludes the coord itself; `coordAdoptTarget` classifies the selection.
- `internal/view/ops/reparent_test.go`, `internal/view/mutations_test.go`, `internal/view/ops/fakes_test.go` — re-parent op + bridge coverage; bindings carry `OrchestratorID`; new `reparentCalls` recorder.
