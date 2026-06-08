# hera-substrate-link Delta: delegate-delivery-to-argus

## ADDED Requirements

### Requirement: Message delivery delegated to argus notify endpoint

The system SHALL delegate all PTY message delivery to argus via
`POST /api/tasks/{id}/notify` and SHALL cancel pending deliveries via
`DELETE /api/tasks/{id}/notify/{delivery_id}`. These calls go through the same
recovering `argus.Client` that all other argus API calls use.

#### Scenario: Notify call goes through the recovering client

- **WHEN** `hera_send` is called AND the argus link is healthy
- **THEN** hera MUST POST to `{argus_base_url}/api/tasks/{id}/notify` using the
  current baseURL from the recovering client

#### Scenario: Notify during recovery gap returns structured error

- **WHEN** `hera_send` is called AND link state is `recovering` or `down`
- **THEN** hera MUST return the existing link-state structured error (`argus link
  recovering, retry in a moment` or `argus link down: <error>`) from the `LinkGate`
  preamble; the notify call MUST NOT be attempted

#### Scenario: Cancel during recovery gap is silently skipped

- **WHEN** `hera_inbox` or `hera_mark_read` calls `CancelNotify` AND the argus
  link is recovering or the cancel call fails for any reason
- **THEN** the cancel failure MUST be logged at debug level and MUST NOT fail
  the `hera_inbox` / `hera_mark_read` response

#### Scenario: Client base URL updated on recovery before next notify

- **WHEN** argus restarts AND `Recover` updates the client's baseURL to the new port
- **THEN** subsequent `NotifyTask` calls MUST use the new baseURL automatically
  (no additional wiring needed — the recovering client is shared)
