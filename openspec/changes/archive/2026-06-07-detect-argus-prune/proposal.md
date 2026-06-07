# Detect argus prune (task.deleted event)

## Why

Argus is adding a `task.deleted` event emitted when a task is deleted or pruned. Hera
currently has no handler for this event, so a deleted task's binding stays live in the
hera DB forever until the next periodic reconcile fires (up to `ReconcileInterval`). If
hera was down when a deletion happened, the stale binding persists indefinitely.

## What changes

- **TypeTaskDeleted handler**: `AdoptHandler` gains a `TypeTaskDeleted` case that walks
  live bindings for the deleted task and calls `Bindings.End` with reason `"task_deleted"`,
  mirroring the existing `TypeTaskArchived` path.
- **Boot reconcile**: `Start()` calls `resyncHandler.Reconcile(ctx)` synchronously after
  the `ResyncHandler` is constructed. A failure is logged at WARN but does not block
  startup; the periodic reconciler still runs as a fallback.

## Capabilities modified

- `hera-coordination`: new requirement — `task.deleted` ends live bindings; boot reconcile
  catches deletions that occurred while hera was down.
