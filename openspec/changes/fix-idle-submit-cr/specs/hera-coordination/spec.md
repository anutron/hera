## MODIFIED Requirements

### Requirement: Messages auto-submitted when recipient is idle

The system SHALL inject `<formatted-body>\r` into the recipient's argus task PTY via `POST /api/tasks/{id}/input` when ALL of the following hold:

1. The recipient's bound task has been in `session.idle` state for at least `Config.IdleDebounce`.
2. `Config.AutoInjectEnabled` is `true`.

The trailing byte MUST be CR (`\r`, byte 0x0D), not LF. Claude Code's TUI puts the PTY into raw mode, so termios does NOT translate CR to LF on input. The keyboard's Return key emits CR, and CR is the only byte the input handler treats as submit. LF would land in the recipient's input buffer as a visible newline character and would NOT trigger submit.

#### Scenario: Recipient idle, auto-inject enabled, message auto-submits

- **WHEN** `hera_send` is called with a recipient whose bound task has been idle ≥ the configured debounce AND `AutoInjectEnabled = true`
- **THEN** hera MUST POST the formatted body terminated by `\r` (single CR, byte 0x0D — NOT `\n`, NOT `\r\n`) to argus's input endpoint AND record `delivery_mode = "idle_submit"` on the message row

#### Scenario: Recipient idle, auto-inject disabled, message busy-buffers

- **WHEN** `hera_send` is called with a recipient whose bound task has been idle ≥ the configured debounce AND `AutoInjectEnabled = false`
- **THEN** hera MUST POST the formatted body WITHOUT a trailing terminator AND record `delivery_mode = "busy_buffer"` on the message row

### Requirement: Injected messages identify sender

The system SHALL prefix every injected message body with `[hera from <sender-role-name>] ` so the recipient agent can identify the source role without an additional tool call.

#### Scenario: Injected body carries sender prefix

- **WHEN** hera injects a message from role `foo-coordinator` with body `"please review"`
- **THEN** the bytes posted to argus's input endpoint MUST be `[hera from foo-coordinator] please review` (plus `\r` in the idle-submit case, no terminator in the busy-buffer case)

### Requirement: Auto-inject master switch gates idle-submit path

The system SHALL treat `Config.AutoInjectEnabled` as a master switch over the auto-submit branch of `Injector.Inject`. When `AutoInjectEnabled = false`, every message MUST be delivered in `busy_buffer` mode (formatted body, no trailing terminator) regardless of the recipient task's idle state. When `AutoInjectEnabled = true` (the default), v1 behavior is preserved: idle tasks get `idle_submit` (formatted body terminated by CR), non-idle tasks get `busy_buffer` (formatted body, no terminator).

#### Scenario: Auto-inject off, idle recipient still busy-buffers

- **WHEN** `AutoInjectEnabled = false` AND `hera_send` targets a worker whose bound task has been idle ≥ the configured debounce
- **THEN** the message MUST be persisted with `delivery_mode = "busy_buffer"` AND the bytes POSTed to argus's input endpoint MUST be the formatted body WITHOUT a trailing terminator

#### Scenario: Auto-inject toggles back to true, behavior restored

- **WHEN** `AutoInjectEnabled` transitions from `false` to `true` via a successful settings_save AND a subsequent `hera_send` targets an idle recipient
- **THEN** the message MUST be persisted with `delivery_mode = "idle_submit"` AND the bytes POSTed MUST include the trailing CR (`\r`)
