**Design doc:** `openspec/changes/harden-spawn-worker-edge-cases/design.md`

## 1. Tests

- [ ] 1.1 Bridge test: confirming the spawn-worker modal with an empty/whitespace prompt surfaces a dismissible notice (not a silent close) and issues no op call (`internal/view/mutations_test.go`). Failing first.
- [ ] 1.2 Ops test: role/binding insert failure AFTER `CreateTask` succeeds → `SpawnWorker` returns an error, logs the orphan, and issues NO `DeleteTask` call (`internal/view/ops/spawn_worker_test.go`, via the fake argus recording DeleteTask). Failing first.
- [ ] 1.3 Real-selection test: `w` on an archived (and on a dead) agent row resolves to its coordinator and spawns (`internal/view/app_test.go` or `mutations_test.go`, driving the real selection). Failing first.
- [ ] 1.4 Bridge tests: `w` on an orchestrator header with no coord role (`CoordRoleID==0`), and on a sub-coordinator row with no child orchestrator (`ChildOrchestratorID==0`), each surface a "not applicable" notice with no op call. Failing first (for any case not already covered).
- [ ] 1.5 Confirm existing coverage pins the remaining documented behaviors: GetTask-fail soft-degrade (`TestSpawnWorker_GetTaskFailureSoftDegrades`) asserts binding inserted with empty worktree path; auto-select abandon (`TestApp_QueueSelectRole_AbandonsUnresolvableAfterBound`) asserts the bound abandon. Add assertions if they are missing.
- [ ] 1.6 Confirm every `it should X` criterion in `design.md` has a corresponding test.

## 2. Implement empty-prompt feedback

**Depends on:** Stage 1

- [ ] 2.1 In `internal/view/mutations.go` `OnNewWorker`, replace the empty/whitespace-prompt silent `return` in the modal-submit callback with a dismissible notice (e.g. `notApplicable`/error-modal path) reading "w: prompt is required". No argus/DB call on this path. Make 1.1 pass.

## 3. Verify

**Depends on:** Stage 1, Stage 2

- [ ] 3.1 `go test ./...` green; `go vet ./...` clean.
- [ ] 3.2 `openspec validate harden-spawn-worker-edge-cases --strict` passes.
