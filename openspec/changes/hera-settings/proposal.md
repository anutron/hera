## Why

Two operational knobs that landed hardcoded in v1 need a way for the operator to tune them without rebuilding the daemon:

- **Idle debounce.** v1 hardcodes 2 seconds — the substrate `session.idle` semantics are still in flight, and even once that's pinned down, the right value will depend on the operator's typing/thinking cadence. The right answer is a UI knob, not a code constant.
- **Auto-inject master switch.** Some operators will decide auto-submit is too aggressive (e.g., they want to read what hera delivered before it lands in their PTY). v1 has no off-switch. With one boolean we get a "buffer everything" mode without removing the feature.

The argus substrate exposes a settings-section registration surface (`POST /api/plugins/settings/sections` with `type: "form"`) — the morning review locked the field list to exactly these two, and the build order (settings → view → install) puts this change next.

## What Changes

This change adds the smallest possible substrate surface to expose two pre-existing runtime parameters. No new product behavior; only new operator control over existing behavior.

- Add `internal/settings/` package with a `SettingsRegistrar` that mirrors `mcp.Registrar`: on daemon start, POSTs one form-type settings-section to argus with two fields; re-POSTs on a 5-minute heartbeat; DELETEs on graceful shutdown.
- Add `argus.Client.RegisterSettingsSection` / `UnregisterSettingsSection` HTTP wrappers (parallel to `RegisterTool` / `UnregisterTool`).
- Add `Config.AutoInjectEnabled` field (default `true`).
- Add `Tracker.SetDebounce(d time.Duration)` to allow hot-reload of the idle window without daemon restart.
- Add a callback handler at `POST /mcp/settings_save` on hera's existing HTTP listener (port 7744). Uses the same per-session shared-secret auth as the six MCP tool callbacks. On save: validates input (int 0–60, bool), persists both values to the existing `config` table via the existing `ConfigDAO`, and pushes the new values into the live `Tracker` and `Injector`.
- Modify daemon startup to read the two config keys at boot and apply them to `Config.IdleDebounce` / `Config.AutoInjectEnabled` before `Tracker` and `Injector` are instantiated. Saved settings survive restart.
- Modify `Injector.Inject` to gate the auto-submit path on `AutoInjectEnabled`: when false, every message lands in `busy_buffer` mode regardless of idle state. When true (default), v1 behavior is unchanged.

**Out of scope** (deliberately, per NEXT.md):

- No active-orchestrators list (that belongs in `hera-view`).
- No default-orchestrator field (orchestrator names are project names, always typed).
- No user-message-surface field (user-routing was killed in the morning review).
- No new fields beyond the locked two.
- No CLI verb (`hera config set ...`) — out of scope unless and until the substrate's settings UI proves insufficient.

## Capabilities

### Modified Capabilities

- `hera-coordination`: Adds operator-facing settings registration + persistence + hot-reload. Modifies idle-gate and message-delivery requirements to reference the configurable values instead of hardcoded constants.

### New Capabilities

None.

## Impact

- **No new external dependencies.** All work is inside the existing hera codebase.
- **No new ports or files.** Settings live in the existing `config` table inside `~/.hera/state.sqlite`. Callback shares the existing `127.0.0.1:7744` listener.
- **No schema migration.** The `config` table already exists from v1.
- **Backwards compatible.** With no config rows present, defaults (`idle_debounce_seconds=2`, `auto_inject_enabled=true`) reproduce v1 behavior exactly.
- **Substrate surface used:** `POST/DELETE /api/plugins/settings/sections` (settings-section CRUD) plus the existing MCP callback contract (re-used for the save callback).
