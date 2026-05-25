## Context

Two v1 runtime knobs (`Config.IdleDebounce`, no equivalent for "auto-inject off") need operator-facing controls. The argus substrate exposes a settings-section registration surface; the morning review settled the field list at exactly two; the build-order roadmap puts this change before `hera-view`.

Goal: smallest substrate footprint that lets the operator tune both knobs from argus's settings UI, with values persisted and applied without a daemon restart.

## Decisions

### D1: Form section, exactly two fields

- **Decision:** Register a single settings-section with `type: "form"` containing fields `idle_debounce_seconds` (int, min 0, max 60, default 2) and `auto_inject_enabled` (bool, default true). No other fields.
- **Why:** The morning review locked this scope. Each excluded field had a specific reason: active-orchestrators list belongs in the plugin view, default-orchestrator is meaningless (operators always type the project name), user-message-surface is moot now that user-routing is gone.
- **Alternative considered:** Multiple sections (one per concern). Rejected as premature — two fields fit in one form section, and the substrate UI groups by section already.

### D2: Mirror the `mcp.Registrar` pattern exactly

- **Decision:** Add `internal/settings/registrar.go` whose `SettingsRegistrar` struct mirrors `mcp.Registrar` field-for-field — heartbeat ticker, mutex-guarded list of sections (just one for now, but the slice-shape leaves room), Start/Stop lifecycle, DELETE on shutdown.
- **Why:** The pattern is already proven (Registrar has solid tests and survived the v1 audit pass). Mirroring it gives us the same shutdown safety, heartbeat semantics, and test scaffold. The `registrar_test.go` fakeRegistry pattern translates 1:1 to `registrar_settings_test.go`.
- **Cost:** ~150 LOC duplicated between Registrar and SettingsRegistrar. We considered extracting a generic `HTTPRegistrar[T]`. Rejected for v1: one duplicate of a 150-line file is cheaper than the generics-with-callbacks gymnastics, and we don't know yet whether v1.x will add a third registrar (plugin-view) with different lifecycle needs.

### D3: Re-use the existing 7744 HTTP listener for the save callback

- **Decision:** The settings-section registration's `callback_url` points at `http://127.0.0.1:7744/mcp/settings_save`. The route registers as one more handler on the existing `mcp.Server.handlers` map, with the same auth (per-session shared secret, constant-time compare, padded 256-byte).
- **Why:** The substrate sends the auth header on every callback; the secret-rotation lifecycle is already tied to the listener; no need for a second listener or a different auth surface.
- **Cost:** The route lives at `/mcp/settings_save` rather than the more natural `/settings/save`. The naming reflects implementation reality (everything on this listener is dispatched via the `/mcp/` mux); not worth a second mux.

### D4: Persist to the existing `config` table; no migration

- **Decision:** Two keys in the existing `config` table: `idle_debounce_seconds` (value: stringified int, e.g. `"3"`) and `auto_inject_enabled` (value: `"true"` or `"false"`). Use the existing `ConfigDAO.Get/Set`.
- **Why:** The table exists, the DAO works, the migration count stays at 2. Stringified primitives keep the DAO untyped (matches what's already there) — typed parsing happens at the call site in the daemon.
- **Cost:** Per-key parsing duplicated at each read site. Acceptable for two keys; if this grows past five, extract a typed `SettingsStore` over `ConfigDAO`.

### D5: Hot-reload, not restart-to-apply

- **Decision:** On a successful `/settings/save`, the new values are pushed into the live `Tracker` (via new `Tracker.SetDebounce(d)` method) and the live `Injector` (via new `Injector.SetAutoInjectEnabled(b)` method). Operator sees the effect immediately.
- **Why:** Settings panels universally imply hot-reload; restart-to-apply is a footgun, especially when one of the values controls how messages are delivered to the operator's own PTY.
- **Cost:** Each setting needs a setter on its consuming component. Tracker is already mutex-guarded; Injector becomes mutex-guarded for the new boolean (read-side is the hot path, so we use `atomic.Bool` rather than a mutex). All worker tasks dependent on these components inherit the new value automatically (no goroutine restarts needed).

### D6: Defaults on startup, override from DB

- **Decision:** `Default()` sets `IdleDebounce=2s`, `AutoInjectEnabled=true`. After DB open, daemon reads the two keys via `ConfigDAO.Get`; if found, overrides the Config fields BEFORE Tracker and Injector are instantiated. Missing keys → keep defaults (no-op).
- **Why:** This gives us "first-run sane" + "restart preserves saved tuning" without a separate "config has been initialized" flag.

### D7: Input validation in the save handler

- **Decision:** The save handler validates: `idle_debounce_seconds` is an int in `[0, 60]`; `auto_inject_enabled` is a bool. Invalid input → `isError: true` with a content block naming the offending field. The substrate's form validation should catch most of this client-side, but hera enforces server-side anyway (defense in depth — never trust client validation alone).
- **Why:** A 0-second debounce is valid (means "submit on first idle event"). A negative is not. 60 seconds is a generous ceiling — past that, the operator is configuring around a substrate bug, not a UX preference, and they should file an issue.
- **Why not 0–10 or 0–30?** Aaron's typing/thinking cadence varies; 60s leaves room for "I want to be sure" without inviting "I want to disable this." `auto_inject_enabled=false` is the disable path.

### D8: Idle = 0 still requires the most-recent session event to be `session.idle`

- **Decision:** Setting `idle_debounce_seconds=0` does NOT mean "always idle." It means "any `session.idle` event makes the task immediately eligible" — `session.started` / `session.exited` still gate it off. The semantics of `IsIdle` stay the same; only the debounce threshold changes.
- **Why:** Without this, `idle_debounce_seconds=0` would auto-submit messages into actively-running sessions, breaking the spec's "messages buffered when recipient is not idle" invariant.

## Risks / Trade-offs

- **Risk: substrate settings UI semantics not yet exercised.** No previous plugin has registered a settings-section against this argus build. The smoke harness shows it works, but a real callback round-trip with a real form-submitted payload is untested. **Mitigation:** the SettingsRegistrar test uses `httptest.Server` to mock argus exactly as the MCP Registrar test does; the save handler test mocks the callback envelope. If the substrate's payload shape differs from what the smoke harness suggested, we adjust the parsing in one place.
- **Risk: hot-reload races.** Tracker.SetDebounce while a parallel goroutine is reading `t.debounce`. **Mitigation:** Tracker is already `sync.RWMutex`-guarded for the state map; debounce gets a write-lock at the setter and a read-lock at IsIdle. Injector's `autoInjectEnabled` is `atomic.Bool` so the inject path never blocks on a setter.
- **Trade-off: substrate validation vs. server-side validation.** We're doing both, which costs ~20 LOC of duplicated bounds checks. The alternative — trusting the substrate's form-type to enforce min/max — is a thin layer of defense and an unnecessary coupling to substrate behavior we don't control.

## Migration

None. No DB migration. Existing daemon instances boot with defaults; their persisted config is empty and the defaults equal v1's hardcoded values. First save from the UI populates the rows.
