# Tasks

## 1. Spec

- [x] 1.1 Delta: task.deleted ends live bindings; boot reconcile catches offline deletions
- [x] 1.2 Delta: task.archived preserves bindings (archive is non-destructive); reconcile uses archived=all

## 2. Tests (failing first)

- [x] 2.1 TestAdopt_TaskDeleted_BindingEnds (live binding ended with reason "task_deleted")
- [x] 2.2 TestAdopt_TaskDeleted_NoBinding_NoError (no binding — no error, no mutation)
- [x] 2.3 TestDaemonStart_BootReconcileCallsListTasks (GET /api/tasks hit during Start)
- [x] 2.4 TestDaemonStart_BootReconcileFailure_DaemonStillStarts (/api/tasks 500 → daemon still starts)
- [ ] 2.5 TestAdopt_TaskArchived_BindingPreserved (task.archived does NOT end binding)
- [ ] 2.6 TestAdopt_TaskArchived_MultiBinding_BindingsPreserved (all bindings survive archive)
- [ ] 2.7 TestResync_PreservesBindingForArchivedTask (archived task excluded from default list → binding preserved)

## 3. Implementation

- [x] 3.1 Add TypeTaskDeleted constant to internal/events/types.go
- [x] 3.2 Add handleTaskDeleted method + TypeTaskDeleted case in internal/events/adopt.go
- [x] 3.3 Call resyncHandler.Reconcile(ctx) at boot in internal/daemon/run.go
- [ ] 3.4 handleTaskArchived: no-op on binding (archive is non-destructive)
- [ ] 3.5 Reconcile: use ListTasksAll so archived tasks stay in the live set

## 4. Verify

- [ ] 4.1 `go test ./... -race -count=1` green
- [ ] 4.2 `openspec validate --all --strict` green
