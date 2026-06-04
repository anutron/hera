**Design doc:** `openspec/changes/add-spawn-worker-from-rail/design.md`

## 1. Tests

- [x] 1.1 Write failing ops tests for `SpawnWorker` (new `internal/view/ops/spawn_worker_test.go`): empty-prompt validation; task POST in the coord's `argus_project` with `meta:hera.role=worker`; role+binding inserted with the worktree path read via `GetTask`; mission set to the prompt; orientation-prefixed task prompt; name derived from prompt and suffixed on collision with a non-archived sibling. Use the existing ops fakes (`fakes_test.go`), extending the fake `ArgusClient` with `GetTask`.
- [x] 1.2 Write failing mutation-bridge tests (`internal/view/mutations_test.go`): `OnNewWorker` opens an input modal; confirm runs the op off the loop and auto-selects the created row; non-coordinator selection yields a "not applicable" notice with no op call; op error surfaces an error modal.
- [x] 1.3 Write a failing keys test: `w` in RAIL focus invokes `OnNewWorker`; `w` in COORD/AGENT focus is NOT intercepted (forwarded as a byte).
- [x] 1.4 Confirm every `it should X` acceptance criterion in `design.md` has a corresponding failing test (Prove-It Pattern).

## 2. Ops: SpawnWorker

**Depends on:** Stage 1

- [x] 2.1 Add `GetTask(ctx, taskID) (*CreatedTask-or-Task-with-WorktreePath, error)` to the ops `ArgusClient` interface in `internal/view/ops/service.go`; extend `CreatedTask` (or add a small result type) to carry `WorktreePath`. Wire the production adapter to `*argus.Client.GetTask`.
- [x] 2.2 Add `SpawnWorkerInput{TargetCoordRoleID int64 (or OrchestratorID), Prompt string}` and implement `Service.SpawnWorker` in `internal/view/ops/spawn_worker.go`: validate prompt; resolve orchestrator + coord role `argus_project`; derive + unique the role name; `CreateTask` with worker meta; `GetTask` for worktree path; insert role (`kind=worker`, mission=prompt) then binding. Return the created role + task ids.
- [x] 2.3 Implement prompt→name derivation (argus-style slug of the prompt head) and the `-N` suffix uniqueness loop against non-archived sibling roles; empty-slug fallback stem.
- [x] 2.4 Build the orientation-prefixed task prompt (`buildWorkerPrompt(coordName, userPrompt)`), asserting the prefix names the coordinator and mentions `hera_send`.
- [x] 2.5 Handle the partial-failure / worktree-unknown degradations per design Risks (insert binding even with empty worktree path + log; do not delete the argus task on insert failure).

## 3. View: `w` key + bridge + auto-select

**Depends on:** Stage 1

- [x] 3.1 Add `OnNewWorker()` to the `mutations.go` bridge and to the `mutationService` interface: capture selection synchronously, resolve target coordinator from `railSelection` (coordinator row, or an agent row's `OrchestratorID`; freelancer/separator → `notApplicable`), open the input modal, run `SpawnWorker` via `mutate`, then auto-select the created row.
- [x] 3.2 Wire the auto-select-after-create path in `internal/view/app.go` (select the rail row by new role/task id once the broadcaster-driven repopulate lands), keeping focus in `RAIL`.
- [x] 3.3 Add `case 'w':` to RAIL-focus dispatch in `internal/view/keys.go` calling `OnNewWorker()`; confirm it is gated to RAIL focus only (forwarded as a byte in COORD/AGENT).
- [x] 3.4 Add `w` to the advertised RAIL hotkey set pushed to argus (so it appears in the bottom bar / help overlay for RAIL focus).

## 4. Verify

**Depends on:** Stage 2, Stage 3

- [x] 4.1 `go test ./...` green; `go vet ./...` clean.
- [x] 4.2 `openspec validate add-spawn-worker-from-rail --strict` passes.
- [ ] 4.3 Manual dogfood in the live rail: select a coordinator, press `w`, enter a prompt, confirm; verify the worker appears nested under the coordinator within ~100 ms, is selected, focus stays in RAIL, and the argus task starts in the coord's project; repeat targeting an agent row (resolves to its coordinator) and a freelance row (not-applicable notice).
