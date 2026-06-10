# Resurrect a role whose worktree/argus task is gone (BUG-028)

## Why

A hera ROLE is durable; its Claude INSTANCE (argus task + worktree + session) is ephemeral. When the operator prunes argus `:check:` worktrees to reclaim disk, the instances behind dormant roles are destroyed — workers under a coordinator, and sub-coordinators like `2a-team`. The roles correctly survive via the binding model, but there is no way to bring them back to life: they render dormant `○` and can't be interacted with.

BUG-020 gave the worktree-missing reattach a recovery action, but only ONE: delete. That throws away the role's durable identity (its name, its stored prompt, its place in the tree) for a problem that is purely about a missing ephemeral instance. The operator wants to revive the role IN PLACE.

## What Changes

- **New `ops.ResurrectRole(roleID)`:** mints a FRESH argus instance for an EXISTING role. It is fully programmatic and born-bound (the `SpawnWorker` / `NewOrchestrator` pattern): create an argus task in the role's stored `argus_project`, end the role's stale live binding, insert a NEW binding tying the fresh task to the SAME role id, and auto-submit the prompt via CR. Role identity — id, name, prompt, orchestrator — is preserved; NO new role is created. The new session is seeded with the role's stored (verbatim) prompt re-wrapped in the kind-appropriate orientation (coordinator vs worker-under-coordinator). Works for both workers and coordinators; a resurrected coordinator role comes live in its existing place because its `orchestrator_id` is unchanged.
- **Reattach worktree-missing now offers REVIVE alongside DELETE:** when `OnReattach` hits the `ErrWorktreeMissing` condition (BUG-020) on a managed role or a mixed-coord header that HAS a coord role, hera opens a three-way picker — "(r)evive a fresh instance / delete permanently / esc" — instead of the delete-only confirm. Revive routes to `ResurrectRole`, then shows the REATTACHING splash on the fresh pane and selects the now-live row. Delete keeps the BUG-020 behavior. A freelancer (no hera role) or a header with no coord role falls back to the prior delete-only path (revive needs a durable role to rebind).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the worktree-missing reattach recovery (added by BUG-020) is upgraded from delete-only to a revive-or-delete choice; revive mints a fresh born-bound instance for the existing role, preserving its identity.

## Impact

- `internal/view/ops/resurrect.go` — new `ResurrectRole` + `resurrectPrompt` / `coordNameFor` helpers (born-bound fresh instance, kind-aware orientation).
- `internal/view/ops/types.go` — new `ResurrectRoleResult`.
- `internal/view/mutations.go` — `ResurrectRole` added to `mutationService`; new `offerReviveOrDelete` three-way picker; both `OnReattach` worktree-missing branches route to it (with delete-only fallback when revive is impossible).
- Tests across `internal/view/ops` and `internal/view`.
