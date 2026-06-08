# Design: Bounce Recovery

## Trigger point

The `BounceRecoverer.ResumeWorkers` method is called from a wrapper around `argus.RecoverFunc` in `daemon.Start`. It runs only when link recovery fully succeeds (link state transitions to `LinkHealthy`). If recovery fails (link state stays `LinkDown`), no resume messages are sent — the next successful recovery attempt will send them.

## BounceRecoverer

`BounceRecoverer` lives in `internal/daemon/bounce_recovery.go`. It holds:

- `DB *db.DB` — reads orchestrators, roles, bindings; creates and updates messages.
- `Injector workerInjector` — attempts PTY delivery after persisting each message.
- `Log *slog.Logger`

### ResumeWorkers algorithm

1. Call `db.Orchestrators.List` to enumerate all active (non-archived) orchestrators.
2. For each orchestrator, call `db.Roles.ListByOrchestrator` to get active roles.
3. Partition roles: collect the first `kind=coordinator` as `coord`, collect all `kind=worker` into `workers`. Skip `kind=freelance`.
4. If `coord == nil` or `len(workers) == 0`, skip this orchestrator.
5. For each worker: call `db.Bindings.GetLiveByRole`. If `ErrNotFound`, skip (worker is done). If other error, log and skip.
6. Call `db.Messages.Create(FromRoleID=coord.ID, ToRoleID=worker.ID, Body=bounceResumeBody, Tldr=bounceResumeTldr)`.
7. Call `Injector.Inject` to attempt PTY delivery. If inject fails (e.g., argus task session not yet running post-bounce), set `mode = DeliveryQueuedNoBinding`. The worker will read the message via `hera_inbox` on reconnect.
8. Call `db.Messages.SetDelivered` to persist the final delivery mode.
9. Errors on individual workers are logged but do not abort the sweep.

### Static resume message

```
Tldr: "argus bounced — please resume"
Body: "argus bounced — please resume any unfinished work and report back to your coordinator"
```

## Idempotency

The watcher's single-flight gate prevents concurrent recovery runs. A single argus bounce fires the callback exactly once. Multiple calls to `ResumeWorkers` in sequence would send duplicate messages, but the watcher design prevents this in normal operation.

## Daemon wiring

```go
bounceRec := &BounceRecoverer{DB: database, Injector: injector, Log: log}

linkRecover := argus.RecoverFunc(ports, client, registrar, settingsReg, log)
recover := func(ctx context.Context) {
    linkRecover(ctx)
    if argus.GetLinkState() == argus.LinkHealthy {
        bounceRec.ResumeWorkers(ctx)
    }
}

registrar.SetOnHeartbeat404(recover)
watcher := &argus.Watcher{..., OnRestart: recover, ...}
```

## Scope limitation: hera + argus co-restart

If both hera and argus restart simultaneously, the watcher does not fire (hera is starting fresh). In this case, resume messages are not sent automatically.

**Integration note**: The parallel argus-bounce-signal worker is implementing the argus side — posting `{"type":"ARGUS_BOUNCED"}` (sender task ID `0`) to the coordinator's argus task inbox when argus restarts. Once that argus PR lands and argus exposes a `GET /api/tasks/{id}/inbox` REST endpoint, a future change can add co-restart support by checking the task inbox in `hera_new_orchestrator` / `hera_join`.

This change covers the primary use case (hera running, argus bounces alone).
