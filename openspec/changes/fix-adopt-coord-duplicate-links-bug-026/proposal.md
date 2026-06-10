# `J` re-parenting a coordinator is idempotent — no duplicate link rows (BUG-026)

## Why

BUG-025 made `J` re-parent a coordinator regardless of coord session liveness, resolving the child's coordinator argus task from the coordinator role's latest binding (live OR ended). But its prior-parent-linkage teardown only fires on a LIVE binding (`ListLiveBindingsByTask`). For a DORMANT coordinator that is exactly the wrong assumption.

When a coordinator's coord session is gone from argus, the resync reconciler (`ResyncHandler.Reconcile`) ends that coordinator's binding — and, crucially, ends the worker LINK binding a re-parent creates (`end_reason="resync_missing"`), since the link binds the same now-missing coord task. The link ROLE row survives. So the next `J` on that coordinator finds no live link to tear down, `uniqueRoleName` de-collides the new link against the surviving stale role, and a duplicate worker row appears: `2a-team`, then `2a-team-2`, `2a-team-3`, … Pressing `J` repeatedly piles up one orphan link role per press. This is the reported symptom: "J creates duplicate entries."

Re-parenting is meant to be idempotent — pressing `J` and picking the same (or a different) parent should leave exactly one link, never a growing stack of dead de-collided rows.

## What Changes

- **The prior-parent-linkage teardown removes EVERY prior link role for the coordinator's coord task — live AND ended — not only live ones.** A "parent link" is any binding of the coordinator's coord task on a role OTHER than the coordinator's own coordinator role. All such link roles are deleted (their bindings cascade) before the fresh link is created, so a dormant coordinator whose link binding was ended by the reconciler does not leave an orphan behind for the next press to de-collide against.
- **Live prior links are still ended with `end_reason="reparented"`** before deletion, preserving the audit trail the clean-move path already produces.
- **The coordinator's own coordinator role is never torn down**, identified by role id (robust against legacy bindings that carry a NULL `orchestrator_id`).
- **Behavior for a live coordinator and the single-move case is unchanged**: re-parenting a coordinator that is currently nested under one parent still ends that one link and creates one new link.
- **Legitimate de-collision is unaffected**: a NEW link still de-collides against an unrelated active role of the same name; only STALE links to the SAME coord task are removed first.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the `J` adopt requirement — re-parenting a coordinator tears down ALL prior parent linkages for its coord task (live or ended), making repeated re-parents idempotent (no accumulating duplicate link rows), instead of tearing down only live linkages.

## Impact

- `internal/db/bindings.go` — new `BindingsDAO.ListByTaskID` returning every binding (live AND ended) for an argus task id.
- `internal/view/ops/service.go` — new `ListBindingsByTask` on the ops `DB` interface.
- `internal/view/ops_adapters.go` — `dbAdapter.ListBindingsByTask` wires the DAO into ops.
- `internal/view/ops/reparent.go` — `ReparentCoordinator` ends live parent-link bindings (audit), then deletes EVERY distinct parent-link role (live or ended) for the coord task — identified by role id, never the coordinator's own coord role — so re-parenting is idempotent.
- `internal/view/ops/reparent_test.go` — coverage that repeated re-parenting of a dormant coordinator (with the reconciler ending the link binding between presses) leaves exactly one clean link.
- `internal/view/reparent_idempotent_test.go` — real-DB end-to-end coverage of the same, exercising the actual `ListByTaskID` query and the `ON DELETE CASCADE`.
