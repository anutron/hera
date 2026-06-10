# Tasks

## 1. Implementation

- [x] 1.1 Add `BindingsDAO.ListByTaskID` in `internal/db/bindings.go` returning every binding (live AND ended) for an argus task id, ordered by `started_at, id`.
- [x] 1.2 Add `ListBindingsByTask` to the ops `DB` interface in `internal/view/ops/service.go` and implement `dbAdapter.ListBindingsByTask` in `internal/view/ops_adapters.go`.
- [x] 1.3 In `internal/view/ops/reparent.go`, end every LIVE parent-link binding (`end_reason="reparented"`, skipping the coordinator's own coord role by role id), then delete EVERY distinct parent-link role for the coord task (live or ended) via `ListBindingsByTask` before creating the fresh link.
- [x] 1.4 Identify the coordinator's own coord binding by role id (not orchestrator id), so a legacy binding with a NULL `orchestrator_id` is never mistaken for a parent link.

## 2. Tests

- [x] 2.1 `internal/view/ops/reparent_test.go`: repeated re-parenting of a dormant coordinator — with the reconciler ending the link binding between presses — leaves exactly one link role with the clean (non-de-collided) name.
- [x] 2.2 `internal/view/reparent_idempotent_test.go`: real-DB (ops.Service over dbAdapter over sqlite) end-to-end — three `J` presses with reconciler interference yield exactly one clean link, exercising `ListByTaskID` + `ON DELETE CASCADE`.
- [x] 2.3 The single-move case (`MovesFromExistingParent`) and legitimate de-collision (`DeCollidesRoleName`) still pass unchanged.
- [x] 2.4 `make test` passes with `-race`.
