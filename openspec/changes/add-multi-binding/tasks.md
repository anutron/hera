## 1. Stage A — Schema migration + DAO surface

- [x] 1.1 Add migration `0004_bindings_per_orchestrator` to `internal/db/schema.go`. Adds `bindings.orchestrator_id`, backfills, drops `_task` / `_worktree` unique indexes, replaces with `_task_orch` / `_worktree_orch`.
- [x] 1.2 `Binding.OrchestratorID` added to `internal/db/types.go`.
- [x] 1.3 `CreateBindingInput.OrchestratorID` + DAO `Create` persists the column. When the input field is zero the DAO derives it from the role row (defensive default — existing fixtures pass through unchanged).
- [x] 1.4 Every `SELECT` in `internal/db/bindings.go` reads `orchestrator_id`.
- [x] 1.5 `GetLiveByTaskAndOrchestrator` added (primary handler-side lookup).
- [x] 1.6 `ListLiveByTaskID` added (multi-binding case; used by adopt + archive).
- [x] 1.7 `ErrAmbiguous` sentinel added; `GetLiveByTaskID` returns it on 2+ live rows.
- [x] 1.8 Tests in `internal/db/bindings_multi_test.go`: derive-from-role, multi-binding insert, ErrAmbiguous, partial-unique-index violation, migration backfill simulation.
- [x] 1.9 `go test ./internal/db/... -race -count=1` green.

## 2. Stage B — Resolver + caller-role surface

- [x] 2.1 `Resolver.CallerRole(ctx, cwd, orchestrator)` per design D4.
- [x] 2.2 `AmbiguousBindingError` + `NoBindingForOrchestratorError` typed errors; `buildAmbiguousError` helper.
- [x] 2.3 Call sites in `handler_send.go`, `handler_inbox.go`, `handler_mark_read.go`, `handler_status.go` pass the new `Orchestrator` input through.
- [x] 2.4 `go test ./internal/mcp/... -race -count=1` green.

## 3. Stage C — Handler updates

- [x] 3.1 `handler_new_orchestrator.go` rejection narrowed to `GetLiveByTaskAndOrchestrator`; binding INSERT now passes `OrchestratorID` explicitly.
- [x] 3.2 `handler_join.go::attach` rejection narrowed to same-orchestrator.
- [x] 3.3 `handler_join.go::claim` handles 0/1/2+ bindings and explicit-orchestrator claim.
- [x] 3.4 `handler_join.go::Handle` routes (role_name OR kind) → attach, else → claim.
- [x] 3.5 `SendInput.Orchestrator` + handler wiring.
- [x] 3.6 `InboxInput.Orchestrator` + handler wiring.
- [x] 3.7 `MarkReadInput.Orchestrator` + handler wiring.
- [x] 3.8 `StatusInput.Orchestrator` + handler wiring.
- [x] 3.9 `internal/daemon/run.go` tool registrations: `orchestrator` field added to send/inbox/mark_read/status schemas with descriptive copy; hera_join schema description rewritten to reflect new claim + attach shapes.
- [x] 3.10 `go test ./internal/mcp/... -race -count=1` green.

## 4. Stage D — Events package (adopt + archive)

- [x] 4.1 `handleLinkCreated`: parent multi-binding handled. Picks the single coordinator binding; logs WARN + skips on 2+ coords.
- [x] 4.2 `handleTaskArchived` ends every live binding for the archived task.
- [x] 4.3 Tests added: `ParentHasMultipleCoordinatorBindings_NotAdopted`, `ParentHasWorkerAndCoordinator_AdoptUnderCoordinator`, `TaskArchived_EndsEveryLiveBinding`.
- [x] 4.4 `resync_test.go` reviewed — it iterates `ListLive()` and ends bindings whose task is gone; per-row iteration already handles multi-binding correctly.
- [x] 4.5 `go test ./internal/events/... -race -count=1` green.

## 5. Stage E — Integration smoke (in-process)

- [x] 5.1 `internal/mcp/multi_binding_test.go` covers the cross-handler multi-binding setup with bindings to orch A (worker) and orch B (coord).
- [x] 5.2 `TestMultiBinding_HeraJoinClaimAmbiguous` + `_HeraJoinClaimWithOrchestrator`.
- [x] 5.3 `TestMultiBinding_NewOrchestratorAddsThirdBinding`.
- [x] 5.4 `go test ./... -race -count=1` green.

## 6. Stage F — Ship

- [x] 6.1 `make build` succeeds.
- [ ] 6.2 Commits: brainstorm (fb21676 → rebased), schema+DAO, handlers+events+integration tests.
- [ ] 6.3 `mcp__argus__iris_publish(task_id=$ARGUS_TASK_ID, reset=true, push=true)`.
- [ ] 6.4 `mcp__argus__iris_status` verify reload outcome.
- [ ] 6.5 `hera_status done`; `task_set_result`.
