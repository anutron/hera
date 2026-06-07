# Tasks: reliable-bus-delivery

## 1. DB migration + DAO methods

- Add migration `0006_nudge_tracking` to `internal/db/schema.go` (nudge_count, nudged_at columns on messages).
- Add `db.MessagesDAO.UnreadIdleSubmitStale(ctx, firstNudgeCutoff, repeatNudgeCutoff time.Time, maxNudges int)` — returns `[]*Message` rows needing nudge.
- Add `db.MessagesDAO.RecordNudge(ctx, messageIDs []int64)` — increments nudge_count and sets nudged_at=now for each id.
- Update `Message` struct in `types.go` with `NudgeCount int` and `NudgedAt *time.Time`.
- Update `scanMessageRow` to scan the two new columns.
- Tests: UnreadIdleSubmitStale returns correct rows; RecordNudge updates count+timestamp; cap enforced by query.

## 2. Config additions

- Add `NudgeAfter time.Duration`, `NudgeEvery time.Duration`, `MaxNudges int` to `Config` struct.
- Set defaults in `Default()`: 30s, 30s, 5.
- Tests: Default() values.

## 3. DeliveryWatcher

- New `internal/inject/doorbell.go` with `DeliveryWatcher` struct.
- `FormatDoorbell(n int) string` — exported formatter.
- `NewDeliveryWatcher(db, pty, nudgeAfter, nudgeEvery, maxNudges, log)` constructor.
- `Run(ctx)` — ticker loop calling `scan(ctx)`.
- `scan(ctx)` — query stale messages, aggregate by recipient, fire one doorbell per recipient, call RecordNudge.
- Tests: watcher fires when stale unread; stops when read_at set; aggregates multiple stale messages for same recipient into one doorbell; cap respected; doorbell does not re-inject original body.

## 4. Daemon wiring

- Wire `DeliveryWatcher` into `daemon.Start()` and `daemon.Daemon` struct.
- Start goroutine with dedicated context.
- Cancel in `Daemon.Stop()` before DB close (after periodic reconciler, before MCPServer stop).

## 5. Skill update

- Add doorbell contract section to `claude/skills/hera/SKILL.md`.

## 6. Validate + commit

- `go test ./... -race -count=1` green.
- `go build ./...` clean.
- `golangci-lint run` clean.
- `openspec validate --change reliable-bus-delivery --strict` passes.
- Commit all: specs delta, migration, watcher, wiring, skill.
