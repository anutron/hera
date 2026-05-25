## Why

Hera silently breaks every time argus restarts.

The root cause is two missing capabilities in the argus-link layer:

- **No port discovery.** Hera talks to argus's REST API at a hardcoded `http://127.0.0.1:7743`. Argus picks its REST port dynamically via `bindWithRetry` (7742, 7743, 7744, ...). On the latest argus restart, REST landed on 7745 because 7742-7744 were taken. Every `hera_*` MCP tool now 404s because `TaskForCwd` is hitting the wrong port.
- **No restart detection.** Even if hera started on the right port, an argus restart later in the session moves the port out from under it. Today hera has no way to notice the substrate has restarted until the next 5-minute MCP registrar heartbeat returns a 404 – and even then, it has no recovery path beyond logging the error.

The fix is a small, isolated substrate-link layer that discovers the port at startup, watches for argus restarts, and force re-registers tools and settings on detection. Nothing about hera's coordination behavior changes; only its resilience to the link going up and down.

## What Changes

This change adds the smallest possible argus-link layer to make hera resilient to argus restarts. No new product behavior; only new resilience around existing behavior.

- Add `internal/argus/socket.go` with a JSON-RPC client for `~/.argus/daemon.sock` exposing `Daemon.Ports() → {mcp_port, api_port}` and `Daemon.Ping()`.
- Wire startup port discovery in the daemon bootstrap: socket query, hard exit if it fails. Use the discovered port for the `internal/argus/Client` baseURL instead of the hardcoded `:7743`.
- Add an atomic baseURL setter on `argus.Client` so the URL can be swapped at runtime without recreating the client (today baseURL is read-only after construction).
- Add `internal/argus/Watcher` – a goroutine that polls `~/.argus/daemon.pid` mtime every 1 second and pings the daemon socket. On mtime change OR ping failure, the watcher fires a recovery routine that re-runs the discovery and updates the client's baseURL.
- Add "force re-register now" methods on `mcp.Registrar` and `settings.Registrar` so the watcher can drive immediate re-registration instead of waiting for the next 5-minute heartbeat. The existing heartbeat 404 path also calls the same recovery routine as a passive fallback.
- Add a degraded-state error path in MCP tool handlers: while recovery is in progress, `hera_*` calls return a structured error `argus link recovering, retry in a moment` instead of failing the connection.
- Add an `argus_link` field (`healthy | recovering | down`) to the `hera_status` output so operators can see the link state without reading logs.

## Capabilities

### New Capabilities

- `hera-substrate-link`: All of the resilience surface above. Defines how hera discovers argus's REST port, detects argus restarts, recovers from them, and surfaces the link state to operators and tool callers.

### Modified Capabilities

None. The 5-minute heartbeat requirement in `hera-coordination` (MCP tool registrations heartbeated and unregistered on shutdown) is unchanged; it now serves as a passive fallback when the watcher misses an event, but the requirement text and scenarios stay as-is.

## Out of scope

Deliberately excluded from this change:

- No new persistence. Port info is queried fresh every time, not cached in SQLite. The pid file + socket are the source of truth.
- No automatic argus restart by hera. Hera never restarts argus; that's drn's territory.
- No reconnect for the SSE event stream. `internal/events/Subscriber` already has reconnect logic per its own existing requirement; we leave it alone.
- No Linux parity claim. The mtime-polling pattern is portable, but we only test and support macOS for v1.

## Impact

- **Scope is tightly contained.** All work lives in `internal/argus/` (new socket + watcher files), plus thin "force re-register" methods on the two existing registrars, plus one new field on `hera_status`, plus a structured-error wrapper in the MCP handlers. Coordination behavior is unchanged.
- **No new external dependencies.** Go stdlib `net/rpc/jsonrpc` over `net.Dial("unix", ...)`. File polling via `os.Stat`.
- **No new ports or files owned by hera.** The new socket is argus's existing `~/.argus/daemon.sock`; the new pid file is argus's existing `~/.argus/daemon.pid`.
- **No schema migration.** Nothing persists.
