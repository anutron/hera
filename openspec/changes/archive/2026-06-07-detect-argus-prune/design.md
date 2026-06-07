# Design: detect-argus-prune

## TypeTaskDeleted handler

`internal/events/types.go`: add constant `TypeTaskDeleted = "task.deleted"`.

`internal/events/adopt.go`: add `TypeTaskDeleted` to the `HandleEvent` switch, routing to
a new `handleTaskDeleted` method. The method mirrors `handleTaskArchived`:

1. Guard: if `ev.TaskID` is empty, return.
2. `db.Bindings.ListLiveByTaskID(ctx, ev.TaskID)` — find all live bindings for the task.
3. For each binding: `db.Bindings.End(ctx, bnd.ID, "task_deleted")`.
4. Log INFO `"binding ended on task.deleted"` per binding. Log WARN on error and continue.

No daemon wiring change is needed — `AdoptHandler` is already registered in
`internal/daemon/run.go` via `subscriber.Register(events.NewAdoptHandler(...))`.

## Boot reconcile

`internal/daemon/run.go`: after `resyncHandler` is constructed (around line 216) and
before `return &Daemon{...}`, call `resyncHandler.Reconcile(ctx)` synchronously.

Error handling: log at WARN, do NOT return the error. The daemon must start even if the
argus task-list endpoint is temporarily unavailable.

```go
if err := resyncHandler.Reconcile(ctx); err != nil {
    log.Warn("boot reconcile failed", "err", err)
}
```

## Test plan

### events package (adopt_test.go)

- `TestAdopt_TaskDeleted_BindingEnds`: live binding → `End` called with `"task_deleted"`.
- `TestAdopt_TaskDeleted_NoBinding_NoError`: no binding → no error, no mutation.

### daemon package (run_test.go)

- `TestDaemonStart_BootReconcileCallsListTasks`: after `Start()`, at least one `GET
  /api/tasks` was made synchronously (the boot reconcile ran).
- `TestDaemonStart_BootReconcileFailure_DaemonStillStarts`: when `/api/tasks` returns 500,
  `Start()` still returns `(daemon, nil)`.
