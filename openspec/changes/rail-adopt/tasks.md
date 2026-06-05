## 1. Spec

- [x] 1.1 Write proposal + design + hera-view delta (`J` on a freelancer opens an orchestrator picker and creates an operator-side worker binding mirroring `hera_join` attach-mode; non-freelancer rows get feedback; off-loop); `openspec validate rail-adopt --strict` green

## 2. Tests first (TDD)

- [x] 2.1 Failing ops tests (`ops/adopt_test.go`): `AdoptTaskIntoOrchestrator` creates a worker role + live binding under the chosen orchestrator for the freelancer's task; de-collides the role name against an existing active role; best-effort `PutTaskMeta(role=worker)`; rejects empty task id, unknown orchestrator, and a task already live-bound; `ListActiveOrchestrators` returns the active set
- [x] 2.2 Failing router test (`keys_test.go`): `J` in RAIL fires `OnAdopt`; lowercase `j` still maps to KeyDown; in a pane `J` forwards as the rune
- [x] 2.3 Failing bridge tests (`mutations_test.go`): `OnAdopt` on a freelancer lists orchestrators and opens the picker, and selecting one calls `AdoptTaskIntoOrchestrator`; `OnAdopt` on a coordinator/managed/orchestrator row calls `notApplicable` (no adopt); a freelancer with empty task id gets feedback; no active orchestrators gets feedback; runs off-loop (no deadlock)
- [x] 2.4 Failing modal test (`modals_test.go`): `ShowSelect` invokes `onSelect` with the chosen index on submit and `onCancel` on dismiss

## 3. Implementation

- [x] 3.1 `ops/types.go` + `ops/adopt.go`: `CreateRoleInput`/`CreateBindingInput` neutral shapes; `ops.DB` gains `CreateRole`/`CreateBinding`/`GetRoleByOrchestratorAndName`; `ops.ArgusClient` gains `PutTaskMeta`; `Service.AdoptTaskIntoOrchestrator` (validate → orchestrator lookup → already-bound guard → de-collide → create role+binding → best-effort meta) and `Service.ListActiveOrchestrators`
- [x] 3.2 `ops_adapters.go`: `dbAdapter.CreateRole`/`CreateBinding`/`GetRoleByOrchestratorAndName`; `argusAdapter.PutTaskMeta`
- [x] 3.3 `keys.go`: `OnAdopt()` on `MutationHandler`; `J` fires it in `handleRail` (RAIL-only)
- [x] 3.4 `mutations.go`: `mutationService` gains `AdoptTaskIntoOrchestrator`/`ListActiveOrchestrators`; `mutationBridge.OnAdopt` (freelancer-only gate + feedback; off-loop list+picker; off-loop adopt via `mutate`)
- [x] 3.5 `modals.go`: `ShowSelect` themed picker on `modalAPI` + `*App`
- [x] 3.6 `app.go` + `rail_list.go`: carry the freelancer's `Project` onto the role selection so the adopted role records `argus_project`
- [x] 3.7 Full `go test ./... -race -count=1` green (note the environmental 1Password-signer worktree-remover test if it appears)

## 4. Ship

- [x] 4.1 Commit code + tests + change folder together on the feature branch; report via hera_send (reused binding path, picker UX, key chosen, edge handling, files, spec path, tests, branch)
