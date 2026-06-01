## What

Exposes each task's "needs input" state (the agent is blocked waiting on the human — the red `?` in the TUI) to external API/SSE consumers, so they no longer have to scrape on-disk session logs.

- `GET /api/tasks`: new `needs_input` boolean (`omitempty`), gated on `in_progress`.
- SSE: new `session.needs_input` event, emitted on **enter** and on **clear** (payload bool distinguishes the edge), mirroring how `session.idle` already travels.

## Why

The hera daemon (an argus consumer) wants to render an attention indicator in its own rail — including for freelancer agents — so the operator never leaves hera to notice an agent needs them. Re-deriving "needs input" inside hera would duplicate argus's detection heuristic (which has already changed once) and couple hera to argus's private on-disk log format. argus should own and publish the signal.

## Design — daemon-authoritative

Computed in the daemon (same process as the HTTP API), hung off the existing daemon `idleWatcher` tick, so it is correct **with no TUI attached**.

- `EventTypeSessionNeedsInput = "session.needs_input"` (model)
- `agent.Runner`: `SetNeedsInputIDs` / `NeedsInput` / `NeedsInputIDs`
- `detectNeedsInputTick` reuses the existing `agent.DetectNeedsInput` (idle-gated + sticky; no heuristic fork)
- Gotchas documented in `events.md`

Additive and non-breaking: `omitempty` field, new event type. Existing consumers unaffected.

## Tests

`make test` (race), `make fmt-check`, and `make lint-pr` all green. New code 95–100% covered; filtered total holds at 88.5% (above the floor).

Built by dogfooding hera (orchestrator `argus-needs-input`, worker role `impl`).

🤖 Generated with [Claude Code](https://claude.com/claude-code)
