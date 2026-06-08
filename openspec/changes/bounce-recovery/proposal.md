## Why

When argus bounces (restarts), managed worker agents running under hera orchestrators lose their delivery pathway and have no signal to resume. Workers that were mid-task sit silently until a human manually nudges them.

## What Changes

- After the argus watcher fires `OnRestart` and link recovery succeeds, hera automatically sends a static resume message to every active managed worker (kind=worker) with a live binding under every active coordinator orchestrator.
- Freelancers are excluded — they are self-managed.
- No new workers are spawned; only existing workers receive the resume message.
- The existing watcher single-flight gate ensures each bounce triggers exactly one recovery sweep.

## Capabilities

### New Capabilities

_(none)_

### Modified Capabilities

- `hera-substrate-link`: Recovery routine extended — after re-registration succeeds, hera enumerates active workers and sends them a resume message.
- `hera-coordination`: Workers receive a static resume message from their coordinator when argus bounces, informing them to check their inbox and resume unfinished work.

## Impact

- `internal/daemon/run.go`: Wire a `BounceRecoverer` into the OnRestart callback after link recovery.
- `internal/daemon/bounce_recovery.go`: New file implementing the `BounceRecoverer` type.
- `internal/argus/recovery.go`: No changes — the recovery function is wrapped at the daemon layer.
- No schema changes, no new MCP tools.
