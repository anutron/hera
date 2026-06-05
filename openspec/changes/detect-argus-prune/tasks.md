# Tasks

## 1. Spec

- [ ] 1.1 Delta: task.deleted ends live bindings; boot reconcile catches offline deletions

## 2. Tests (failing first)

- [ ] 2.1 TestAdopt_TaskDeleted_BindingEnds (live binding ended with reason "task_deleted")
- [ ] 2.2 TestAdopt_TaskDeleted_NoBinding_NoError (no binding — no error, no mutation)
- [ ] 2.3 TestDaemonStart_BootReconcileCallsListTasks (GET /api/tasks hit during Start)
- [ ] 2.4 TestDaemonStart_BootReconcileFailure_DaemonStillStarts (/api/tasks 500 → daemon still starts)

## 3. Implementation

- [ ] 3.1 Add TypeTaskDeleted constant to internal/events/types.go
- [ ] 3.2 Add handleTaskDeleted method + TypeTaskDeleted case in internal/events/adopt.go
- [ ] 3.3 Call resyncHandler.Reconcile(ctx) at boot in internal/daemon/run.go

## 4. Verify

- [ ] 4.1 `go test ./... -race -count=1` green
- [ ] 4.2 `openspec validate --all --strict` green
