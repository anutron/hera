**Design doc:** `openspec/changes/hera-settings/design.md`

**Acceptance criteria:** `openspec/changes/hera-settings/specs/hera-coordination/spec.md` (each `#### Scenario:` is one acceptance criterion).

## 1. Tests (TDD: write these first, watch them fail)

**Depends on:** nothing.

- [x] 1.1 In `internal/settings/registrar_test.go`, write failing tests for: initial POST to `/api/plugins/settings/sections`; 5-minute heartbeat re-POST; DELETE on Stop; payload shape (one form section, two fields with right types/defaults/bounds); callback_url includes `/mcp/settings_save`.
- [x] 1.2 In `internal/argus/settings_test.go`, write failing tests for `RegisterSettingsSection` / `UnregisterSettingsSection` HTTP method/path/headers against an `httptest.Server` mock.
- [x] 1.3 In `internal/mcp/handler_settings_save_test.go`, write failing tests for: valid save updates `config` table rows AND calls `Tracker.SetDebounce` AND calls `Injector.SetAutoInjectEnabled`; out-of-range int returns `isError: true`; non-bool returns `isError: true`; missing field uses last persisted value (partial save).
- [x] 1.4 In `internal/idle/tracker_test.go`, add tests for `SetDebounce(d)` changing the in-effect threshold; concurrent SetDebounce + IsIdle (race detector).
- [x] 1.5 In `internal/inject/inject_test.go`, add tests for `SetAutoInjectEnabled(false)` forcing busy_buffer mode even when the task is idle; SetAutoInjectEnabled(true) restoring idle_submit; default value is true.
- [x] 1.6 In `internal/daemon/run_test.go`, add a smoke test that: persists config rows via `ConfigDAO.Set` before Start; asserts Tracker.debounce and Injector.autoInjectEnabled reflect the persisted values; asserts SettingsRegistrar is started and stopped alongside the MCP Registrar.
- [x] 1.7 Validate the change: `openspec validate hera-settings --strict` MUST pass after spec text is committed (no implementation needed for this gate).

## 2. Argus client extension

**Depends on:** Stage 1.

- [x] 2.1 Add `internal/argus/settings.go` with `SettingsSectionDefinition`, `SettingField`, `RegisterSettingsSection(ctx, def)`, `UnregisterSettingsSection(ctx, name)`. Use the existing `c.doJSON` helper. Endpoints: `POST /api/plugins/settings/sections` and `DELETE /api/plugins/settings/sections/{name}`.
- [x] 2.2 Make Stage 1.2 tests green.

## 3. Settings registrar package

**Depends on:** Stage 2.

- [x] 3.1 Add `internal/settings/registrar.go` mirroring `internal/mcp/registrar.go`. Struct: `{ client *argus.Client, callbackURL, authHeader string, heartbeat time.Duration, log *slog.Logger, mu sync.Mutex, sections []argus.SettingsSectionDefinition, stop chan struct{}, wg sync.WaitGroup }`.
- [x] 3.2 Implement `NewRegistrar(client, callbackBaseURL, authHeader, log) *Registrar`, `Add(section)`, `Start(ctx)`, `Stop(ctx)`.
- [x] 3.3 Start: initial registerAll → spawn heartbeat goroutine. Stop: close stop chan, wait wg, then DELETE each registered section name with a short timeout (10s like the MCP Registrar).
- [x] 3.4 Build the one section definition: name `hera`, type `form`, callback URL = `http://127.0.0.1:7744/mcp/settings_save`, two fields with the following exact descriptions (each describes what the field does AND the operational impact of changing it — operators read these in the settings UI and need both pieces):
  - `idle_debounce_seconds` (int, min 0, max 60, default 2):
    > Seconds an agent's session must stay quiet before hera auto-submits any messages waiting in its input buffer. **Lower** = faster delivery once an agent goes quiet, but higher risk of submitting while the agent is still working between bursts. **Higher** = more padding before submit, at the cost of slower message delivery. **0** submits on the first quiet event. **60** is the ceiling — past that you're working around a substrate bug, not tuning UX. Default 2 reproduces v1 behavior.
  - `auto_inject_enabled` (bool, default true):
    > When **on**, hera auto-submits cross-agent messages (presses Enter for you) once the recipient agent's session has been quiet for the debounce above. When **off**, every message is left sitting in the recipient's input buffer for you to read and submit manually — same as how busy sessions are already handled. Turn off when you want to QA every cross-agent message before it lands. Default on reproduces v1 behavior.
- [x] 3.5 Make Stage 1.1 tests green.

## 4. Config + DB read-back

**Depends on:** Stage 1.

- [x] 4.1 Add `AutoInjectEnabled bool` to `internal/config/config.Config`. `Default()` sets it to `true`.
- [x] 4.2 Add `internal/config/settings_keys.go` with the two key string constants (`KeyIdleDebounceSeconds = "idle_debounce_seconds"`, `KeyAutoInjectEnabled = "auto_inject_enabled"`).
- [x] 4.3 Add `internal/daemon/loadsettings.go` with `func LoadPersistedSettings(ctx, cfg *config.Config, dao *db.ConfigDAO) error` that reads both keys, parses the int (returning a clear error on parse failure), parses the bool, and overwrites the `Config` fields. Missing keys → no-op (defaults stand).
- [x] 4.4 Wire `LoadPersistedSettings` into `daemon.Start` BEFORE Tracker and Injector instantiation.
- [x] 4.5 Make the Stage 1.6 daemon smoke test green for "persisted values override defaults on Start."

## 5. Tracker + Injector setters

**Depends on:** Stage 1.

- [x] 5.1 Add `Tracker.SetDebounce(d time.Duration)` to `internal/idle/tracker.go`. Acquire write-lock on existing mutex; update `t.debounce`. Document zero-and-negative behavior (clamp to 0 — never go negative).
- [x] 5.2 Convert `Tracker.IsIdle` to read-lock `debounce` along with the state map (it already does state-map read-lock; extend to cover debounce).
- [x] 5.3 Add `autoInjectEnabled atomic.Bool` field to `internal/inject/inject.Injector`. Constructor sets it true. Add `SetAutoInjectEnabled(b bool)`.
- [x] 5.4 In `Injector.Inject`, after computing `isIdle`, replace the branch with `if isIdle && i.autoInjectEnabled.Load() { ... } else { ... }`. Body of `else` is the existing busy_buffer path.
- [x] 5.5 Make Stage 1.4 and 1.5 tests green.

## 6. Save handler

**Depends on:** Stages 4, 5.

- [x] 6.1 Add `internal/mcp/handler_settings_save.go`. Implements `Handler` interface (same as the six tool handlers). Wired to the route `settings_save` via `RegisterHandler` in daemon Start.
- [x] 6.2 Parse the callback envelope's `input` map: extract `idle_debounce_seconds` (any-typed; tolerate JSON int or string), `auto_inject_enabled` (any-typed; tolerate bool or "true"/"false" string). Both fields optional — partial save updates only the supplied keys.
- [x] 6.3 Validate: int in `[0, 60]`; bool valid. Reject with `isError: true` and an explanatory content block on validation failure. No DB writes happen if validation fails.
- [x] 6.4 On valid input: `ConfigDAO.Set` each provided key; then call `Tracker.SetDebounce(time.Duration(seconds) * time.Second)` and `Injector.SetAutoInjectEnabled(b)` for whichever values were provided.
- [x] 6.5 Return success response with the new effective values (so the substrate UI can re-render with confirmed state).
- [x] 6.6 Make Stage 1.3 tests green.

## 7. Daemon wiring

**Depends on:** Stages 3, 4, 6.

- [x] 7.1 In `internal/daemon/run.go`, add `SettingsRegistrar *settings.Registrar` field on `Daemon`.
- [x] 7.2 After `mcp.Registrar` is started (and after `LoadPersistedSettings` ran), instantiate `settings.NewRegistrar` with the existing argus client + callback base URL + same auth header that `mcp.Registrar` uses. Add the one section. Call `Start(ctx)`.
- [x] 7.3 Wire `mcp.Server.RegisterHandler("settings_save", ...)` immediately after the six tool handlers, passing the `ConfigDAO`, `Tracker`, `Injector` it needs.
- [x] 7.4 In `Stop`, call `SettingsRegistrar.Stop(ctx)` BEFORE `mcp.Server.Stop()` (graceful unregister before listener teardown), with the same 10-second timeout.
- [x] 7.5 Run `go test ./... -race -count=1`. All packages green.

## 8. Spec base update (handled by archive)

**Depends on:** all above.

- [x] 8.1 `openspec validate hera-settings --strict` still passes after all task checkboxes flip.
- [ ] 8.2 `openspec archive hera-settings` merges the delta into `openspec/specs/hera-coordination/spec.md`.
