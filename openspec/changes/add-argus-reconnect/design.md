## Context

Hera is a long-lived Go daemon that coordinates argus tasks via argus's REST API. Today the REST URL is hardcoded as `http://127.0.0.1:7743`. In practice, argus picks its REST port dynamically via `bindWithRetry` starting at 7742 and walking up until it finds a free one. The latest argus restart landed REST on 7745, so every hera MCP tool is now 404'ing.

Argus also exposes a unix socket at `~/.argus/daemon.sock` with a JSON-RPC service. A parallel argus worker is shipping `Daemon.Ports() → {mcp_port, api_port}`. The pid file at `~/.argus/daemon.pid` is rewritten on every argus startup.

Goal: smallest argus-link layer that lets hera survive argus restarts without operator intervention, while keeping coordination behavior unchanged.

## Decision summary

- **D1.** On startup, query the daemon socket's `Daemon.Ports` for the REST port. Exit non-zero if the socket call fails.
- **D2.** Detect argus restarts at runtime via a `Watcher` goroutine that polls `~/.argus/daemon.pid` mtime every 1 second AND pings the daemon socket. Either signal triggers recovery.
- **D3.** On recovery, re-discover the port via the same D1 path, then atomically update the shared `argus.Client`'s baseURL, then force immediate re-registration on both `MCPRegistrar` and `SettingsRegistrar` (bypassing the 5-minute heartbeat).
- **D4.** Expose link state as `healthy | recovering | down` on `hera status` output. During `recovering`, MCP tool handlers return a structured `argus link recovering, retry in a moment` error rather than failing the MCP connection.
- **D5.** No persistence. No automatic argus restart. No event-stream reconnect work (already covered by `internal/events/Subscriber`).

## Alternatives considered

### A1: Heartbeat-driven recovery only, no pid watch

Drop the watcher. Detect argus restarts only when the next MCP registrar heartbeat returns a 404, then run recovery from there.

**Rejected because** the heartbeat is 5 minutes apart. Between the restart and the next heartbeat, every `hera_*` MCP tool call quietly 404s with no recovery in flight. The pid watcher closes that gap to ~1 second. The heartbeat 404 path remains as a passive fallback for cases where the watcher misses the event.

### A2: Cache last-known port in SQLite

Persist the discovered port. On startup, try the cached port first before querying the socket.

**Rejected because** the socket query is fast (single RPC over a unix socket already on the local machine) and the cache adds a stale-state failure mode: if argus's port changed while hera was down, the cached value misleads the next startup. Fresh-every-time is simpler and faster than maintaining cache invalidation.

### A3: Two atomic-pointer baseURL implementations

Use `sync/atomic.Pointer[string]` for the baseURL setter on `argus.Client`.

**Considered but not chosen for the locked design.** A small mutex-protected setter is simpler to reason about, integrates with the client's existing `sync.Mutex` usage, and the URL read is not on a hot enough path to justify the lockless complexity. We recommend the mutex variant; the implementation worker can revisit if profiling shows contention.

## Failure modes covered

- **Argus restarts gracefully.** Watcher sees pid mtime change within 1 second, fires recovery, swaps the client baseURL, force re-registers tools + settings. Tool calls during the gap return `recovering`.
- **Argus crash-loops (multiple restarts in quick succession).** The watcher debounces by treating any in-flight recovery as authoritative: a second mtime change while recovery is running is no-ops the trigger and lets the in-flight recovery finish. The next polling tick after recovery completes re-checks mtime; if it's changed again, recovery runs again.
- **Argus socket exists but daemon is wedged (Ping times out).** Watcher's socket ping has a short timeout (1 second). Ping timeout counts as a restart signal and fires recovery. If the daemon really is wedged, recovery's own socket query will also time out, and link state transitions to `down`.
- **Argus down for longer than the operator's patience.** Link state stays `down`; `hera status` surfaces it; MCP tool calls continue to return the `recovering` structured error (with a hint that argus may be down). Hera does not exit. Recovery keeps retrying on each pid-mtime change.
- **First boot of hera before argus is up (race).** Startup port discovery fails (no socket, no live port). Hera exits non-zero per D1. The operator's launch agent (or whoever brings hera up) is responsible for ordering; hera does not silently wait.

## Implementation sketch

### New package surface

- `internal/argus/socket.go` – JSON-RPC client over `net.Dial("unix", "~/.argus/daemon.sock")`. Exposes `PortsClient.Ports(ctx) (api int, mcp int, err error)` and `PortsClient.Ping(ctx) error`. Uses Go stdlib `net/rpc/jsonrpc`. This is the sole port-discovery entry point used by both startup and recovery.
- `internal/argus/watcher.go` – `Watcher` struct with `Start(ctx)` / `Stop(ctx)` lifecycle. Polls `~/.argus/daemon.pid` mtime every 1 second AND calls `PortsClient.Ping`. On change-or-failure, invokes a registered `OnRestart func(ctx)` callback that calls `PortsClient.Ports` directly to re-discover the port. Single-flight: an in-flight callback suppresses concurrent triggers.

### Client baseURL setter

Today `argus.Client.baseURL` is set in the constructor and never changes. Add `(c *Client) SetBaseURL(u string)` guarded by the client's existing mutex (or a new dedicated one if the existing mutex doesn't fit). All HTTP-issuing methods on `Client` read baseURL through the mutex. This keeps the change localized to the client and avoids leaking atomic-pointer types into the client's public API.

### Force re-register exposure

Both `mcp.Registrar` and `settings.Registrar` already run a heartbeat goroutine that calls an internal `registerAll` method on a ticker. Expose that as a public `ForceReregister(ctx) error` method. The watcher's `OnRestart` callback calls `client.SetBaseURL(newURL)` then both `ForceReregister` calls in sequence; failures are logged but do not panic.

### Recovery state machine

Link state is a small `atomic.Int32` exposed via `LinkState()` returning `healthy | recovering | down`:

- `healthy` – normal steady state. Transitions to `recovering` when the watcher fires `OnRestart`.
- `recovering` – set at the top of `OnRestart`. Transitions to `healthy` on successful re-discovery and re-registration, or to `down` if the socket query fails during re-discovery.
- `down` – set when recovery fails. Transitions back to `recovering` on the next watcher trigger. Hera does not exit from `down`; it keeps trying.

The `hera_status` MCP tool reads this state and includes it in the response payload as `argus_link`.

### Degraded-state MCP error

MCP tool handlers gain a shared preamble that checks `LinkState()`. When `recovering` or `down`, the handler returns:

- `isError: true`
- a content block with text `argus link recovering, retry in a moment` (or `argus link down: <last error>` for the `down` case)

The MCP server keeps running normally; only the individual tool call returns the error.

## Open questions for review

None – design is locked.
