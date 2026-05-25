# hera-substrate-link Specification

## Purpose
TBD - created by archiving change add-argus-reconnect. Update Purpose after archive.
## Requirements
### Requirement: Argus REST port discovered on hera startup

The system SHALL discover argus's REST API port at daemon startup before constructing the shared `argus.Client`. The discovery MUST: (1) query `Daemon.Ports` over the unix socket at `~/.argus/daemon.sock`; (2) if the socket call fails, log the error and exit non-zero.

The discovered port MUST be used as the baseURL for all `argus.Client` HTTP traffic.

#### Scenario: Socket query succeeds

- **WHEN** the daemon starts AND the argus socket at `~/.argus/daemon.sock` responds to `Daemon.Ports` with `{api_port: 7745, mcp_port: <n>}`
- **THEN** hera MUST construct the `argus.Client` with baseURL `http://127.0.0.1:7745` AND proceed with the rest of startup (DB open, registrar start, event stream subscribe)

#### Scenario: Socket query fails, hera exits non-zero

- **WHEN** the daemon starts AND the socket call to `Daemon.Ports` returns an error
- **THEN** hera MUST exit with status code 1 AND emit a stderr message naming the socket error

### Requirement: Argus restart detected via pid-file mtime and socket ping

The system SHALL run a `Watcher` goroutine for the lifetime of the daemon that polls `~/.argus/daemon.pid` mtime every 1 second AND calls `Daemon.Ping` over the unix socket with a 1-second deadline. Either signal MUST be sufficient to declare an argus restart and trigger the recovery routine.

The Watcher MUST be single-flight: while a recovery callback is in flight, additional restart signals MUST be suppressed until the in-flight callback completes. The next polling tick after completion re-checks both signals.

#### Scenario: Pid file mtime changes, watcher fires recovery

- **WHEN** argus restarts AND the pid file at `~/.argus/daemon.pid` is rewritten with a fresh mtime
- **THEN** the Watcher MUST invoke the recovery callback within `≤2 polling intervals` (≤2 seconds) of the mtime change

#### Scenario: Socket ping fails, watcher fires recovery

- **WHEN** the Watcher's `Daemon.Ping` call returns an error (e.g., the socket connection-refuses or times out) AND the pid mtime has not yet changed
- **THEN** the Watcher MUST invoke the recovery callback on that same polling tick

#### Scenario: Concurrent restart signals suppressed during in-flight recovery

- **WHEN** the recovery callback is in flight AND a new pid mtime change is observed on the next polling tick
- **THEN** the Watcher MUST NOT invoke a second recovery callback; the in-flight one MUST be allowed to finish AND the next polling tick after it completes MUST re-check both signals

### Requirement: Recovery re-discovers port and force-registers tools and settings

The system SHALL run a `Recover` routine when the Watcher fires a restart signal. The routine MUST: (1) set link state to `recovering`; (2) re-query `Daemon.Ports` over the unix socket; (3) update the shared `argus.Client`'s baseURL atomically via `SetBaseURL`; (4) invoke `ForceReregister` on the MCP registrar; (5) invoke `ForceReregister` on the settings registrar; (6) set link state to `healthy` on full success, or `down` (recording the error) on any failure.

The force-reregister methods MUST bypass the registrars' 5-minute heartbeat ticker and POST fresh registrations immediately.

The existing MCP registrar heartbeat 404 path SHALL also call `Recover` as a passive fallback for cases where the Watcher missed the restart signal.

#### Scenario: Pid mtime change triggers re-discovery

- **WHEN** the Watcher fires recovery after a pid mtime change AND the socket's `Daemon.Ports` now returns `{api_port: 7746}`
- **THEN** `Recover` MUST call `client.SetBaseURL("http://127.0.0.1:7746")` BEFORE invoking either registrar's `ForceReregister`

#### Scenario: Re-register fires before next heartbeat

- **WHEN** `Recover` is invoked between heartbeat ticks (i.e., not on the 5-minute boundary)
- **THEN** both `mcp.Registrar.ForceReregister` and `settings.Registrar.ForceReregister` MUST be called within the same `Recover` execution, without waiting for the next heartbeat tick

#### Scenario: Heartbeat 404 triggers same recovery routine

- **WHEN** the MCP registrar's 5-minute heartbeat POST returns a 404 (argus has restarted but the Watcher missed it)
- **THEN** the heartbeat path MUST invoke the same `Recover` routine instead of just logging the error

#### Scenario: Recovery fails, link state set to down

- **WHEN** `Recover` is invoked AND the socket query fails during re-discovery
- **THEN** link state MUST transition to `down`, `LastError` MUST contain the wrapped socket error, AND hera MUST continue running (no exit); the next Watcher trigger MUST re-attempt `Recover`

### Requirement: hera MCP tools return structured "recovering" error during gap

The system SHALL gate every `hera_*` MCP tool handler with a preamble that checks the current link state. When link state is `recovering`, the handler MUST return `isError: true` with a content block whose text is `argus link recovering, retry in a moment`. When link state is `down`, the handler MUST return `isError: true` with a content block whose text is `argus link down: <LastError>`. When link state is `healthy`, the handler MUST proceed with its normal body.

The MCP server itself MUST remain running and accept connections regardless of link state; only the individual tool call returns the structured error.

#### Scenario: Tool call mid-recovery returns recovering error

- **WHEN** `hera_send(cwd=$PWD, body="...")` is invoked AND link state is `recovering`
- **THEN** the tool MUST return `isError: true` with a content block reading `argus link recovering, retry in a moment` AND MUST NOT attempt the normal `argus.Client` HTTP path

#### Scenario: Tool call with link down returns down error

- **WHEN** `hera_inbox(cwd=$PWD)` is invoked AND link state is `down` AND the recorded `LastError` is `socket Ports call: dial unix /Users/aaron/.argus/daemon.sock: connect: no such file or directory`
- **THEN** the tool MUST return `isError: true` with a content block reading `argus link down: socket Ports call: dial unix /Users/aaron/.argus/daemon.sock: connect: no such file or directory`

#### Scenario: Tool call after recovery succeeds normally

- **WHEN** `Recover` completes successfully and link state transitions to `healthy` AND `hera_send` is invoked
- **THEN** the tool MUST proceed with its normal body using the updated `argus.Client` baseURL AND MUST NOT return the recovering or down error

### Requirement: hera status surfaces argus link state

The system SHALL include an `argus_link` field in the `hera_status` tool response with one of three string values: `healthy`, `recovering`, or `down`. The field MUST reflect the current link state at the time of the call. When the state is `down`, the response SHALL also include a `argus_link_error` field carrying the `LastError` string.

#### Scenario: Healthy link state surfaced

- **WHEN** `hera_status(cwd=$PWD)` is called AND link state is `healthy`
- **THEN** the response payload MUST include `argus_link: "healthy"` AND MUST NOT include the `argus_link_error` field

#### Scenario: Recovering link state surfaced

- **WHEN** `hera_status(cwd=$PWD)` is called AND link state is `recovering`
- **THEN** the response payload MUST include `argus_link: "recovering"` AND MUST NOT include the `argus_link_error` field

#### Scenario: Down link state surfaced with error

- **WHEN** `hera_status(cwd=$PWD)` is called AND link state is `down` AND `LastError` is `socket Ports call: dial unix /Users/aaron/.argus/daemon.sock: connect: no such file or directory`
- **THEN** the response payload MUST include `argus_link: "down"` AND `argus_link_error: "socket Ports call: dial unix /Users/aaron/.argus/daemon.sock: connect: no such file or directory"`

