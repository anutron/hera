**Design doc:** `openspec/changes/add-argus-reconnect/design.md`

**Acceptance criteria:** `openspec/changes/add-argus-reconnect/specs/hera-substrate-link/spec.md` (each `#### Scenario:` is one acceptance criterion).

## 1. Tests (TDD: write these first, watch them fail)

**Depends on:** nothing.

- [ ] 1.1 In `internal/argus/socket_test.go`, write failing tests for `PortsClient.Ports` and `PortsClient.Ping` against an in-process unix-socket JSON-RPC server mock.
- [ ] 1.2 In `internal/argus/client_setbaseurl_test.go`, write failing tests for `Client.SetBaseURL` under race detector: concurrent HTTP-issuing methods reading baseURL while a setter updates it.
- [ ] 1.3 In `internal/argus/watcher_test.go`, write failing tests for `Watcher`: pid mtime change fires `OnRestart`; socket ping failure fires `OnRestart`; in-flight callback suppresses concurrent triggers (single-flight).
- [x] 1.4 In `internal/mcp/registrar_test.go` and `internal/settings/registrar_test.go`, add failing tests for `ForceReregister`: invocation issues fresh POSTs without waiting for the next heartbeat tick.
- [x] 1.5 In `internal/mcp/handler_link_state_test.go`, write failing tests for the degraded-state preamble: tool handler called with `LinkState() == recovering` returns `isError: true` with the `recovering` content block; `LinkState() == down` returns the `down` content block; `LinkState() == healthy` proceeds normally.
- [x] 1.6 In `internal/mcp/handler_status_test.go`, add a failing test for the `argus_link` field on `hera_status` output covering all three states.
- [ ] 1.7 In `internal/daemon/run_test.go`, add a failing smoke test that asserts startup discovery runs before the `mcp.Registrar` is started, and that a hard exit occurs when discovery fails.
- [ ] 1.8 Validate the change: `openspec validate add-argus-reconnect --strict` MUST pass after spec text is committed (no implementation needed for this gate).

## 2. Socket RPC client

**Depends on:** Stage 1.

- [ ] 2.1 Add `internal/argus/socket.go` with `PortsClient` struct, constructor `NewPortsClient(socketPath string)`, and methods `Ports(ctx) (api, mcp int, err error)` and `Ping(ctx) error`. Use stdlib `net/rpc/jsonrpc` over `net.Dial("unix", ...)` with a short per-call deadline (1s).
- [ ] 2.2 Make Stage 1.1 tests green.

## 3. Atomic baseURL setter

**Depends on:** Stage 1.

- [ ] 3.1 Add `SetBaseURL(u string)` on `internal/argus/Client` guarded by the client's existing mutex (or a new dedicated `sync.RWMutex` if the existing one doesn't fit). Convert all HTTP-issuing methods to read `baseURL` through the same lock.
- [ ] 3.2 Make Stage 1.2 tests green (including `go test -race`).

## 4. Watcher

**Depends on:** Stages 2, 3.

- [ ] 4.1 Add `internal/argus/watcher.go` with `Watcher` struct: `{ pidPath string, ping func(ctx) error, interval time.Duration, onRestart func(ctx), log *slog.Logger, stop chan struct{}, wg sync.WaitGroup, inflight atomic.Bool }`.
- [ ] 4.2 Implement `Start(ctx)` / `Stop(ctx)`. The Start loop: every `interval` (1s), `os.Stat` the pid file and capture mtime; call `ping(ctx)`; if either signal indicates restart (mtime changed OR ping returned error), invoke `onRestart(ctx)` under single-flight (`inflight.CompareAndSwap(false, true)` guard).
- [ ] 4.3 Make Stage 1.3 tests green.

## 5. Force-reregister on registrars

**Depends on:** Stage 1.

- [x] 5.1 Add `(r *Registrar) ForceReregister(ctx) error` on `internal/mcp/Registrar`. Implementation: call the same `registerAll` helper the heartbeat goroutine uses, return the aggregated error.
- [x] 5.2 Add the same `ForceReregister` on `internal/settings/Registrar`.
- [x] 5.3 Make Stage 1.4 tests green.

## 6. Link state + degraded MCP path

**Depends on:** Stage 1.

- [x] 6.1 Add `internal/argus/linkstate.go` with `LinkState` enum (`healthy | recovering | down`), an `atomic.Int32`-backed setter/getter, and a `LastError() error` accessor for surfacing the `down` reason.
- [x] 6.2 Add a shared preamble used by every MCP tool handler that returns `isError: true` with the `argus link recovering, retry in a moment` content block when the state is `recovering`, and `argus link down: <last error>` when the state is `down`. Wire it ahead of every existing tool handler's body.
- [x] 6.3 Add the `argus_link` field to the `hera_status` response payload, sourced from `LinkState()`.
- [x] 6.4 Make Stages 1.5 and 1.6 tests green.

## 7. Recovery routine + daemon wiring

**Depends on:** Stages 2-6.

- [ ] 7.1 Add `internal/argus/recovery.go` with a `Recover(ctx)` function: set state to `recovering`, call `PortsClient.Ports`, call `client.SetBaseURL`, call `mcpRegistrar.ForceReregister`, call `settingsRegistrar.ForceReregister`. On any failure, set state to `down` and store the error; on full success, set state to `healthy`.
- [ ] 7.2 In `internal/daemon/run.go`, wire startup discovery: before constructing the `argus.Client`, call `PortsClient.Ports`. On error, log and exit non-zero. On success, construct the client with the discovered baseURL.
- [ ] 7.3 After both registrars are started, instantiate the `Watcher` with `onRestart = Recover` and start it. Stop it in the daemon's shutdown path before the registrars stop.
- [ ] 7.4 Wire the existing 5-minute heartbeat 404 path on `mcp.Registrar` to also call `Recover` as a passive fallback.
- [ ] 7.5 Make Stage 1.7 daemon smoke test green.

## 8. Validate

**Depends on:** all above.

- [ ] 8.1 `go test ./... -race -count=1`. All packages green.
- [ ] 8.2 `openspec validate add-argus-reconnect --strict` still passes after all task checkboxes flip.
- [ ] 8.3 `openspec archive add-argus-reconnect` merges the delta into `openspec/specs/hera-substrate-link/spec.md` (creating the new base spec file).
