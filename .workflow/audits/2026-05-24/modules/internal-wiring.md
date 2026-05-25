# internal/daemon + internal/config

Combined module: daemon assembly/teardown and config (token loader, defaults).

Files in scope:

- `/Users/aaron/.argus/worktrees/Ludwig/ludwig-argus-coordinator/internal/daemon/doc.go`
- `/Users/aaron/.argus/worktrees/Ludwig/ludwig-argus-coordinator/internal/daemon/run.go`
- `/Users/aaron/.argus/worktrees/Ludwig/ludwig-argus-coordinator/internal/daemon/run_test.go`
- `/Users/aaron/.argus/worktrees/Ludwig/ludwig-argus-coordinator/internal/config/doc.go`
- `/Users/aaron/.argus/worktrees/Ludwig/ludwig-argus-coordinator/internal/config/config.go`
- `/Users/aaron/.argus/worktrees/Ludwig/ludwig-argus-coordinator/internal/config/config_test.go`

## Summary

- Behavioral branches: 39 (26 in `run.go`, 13 in `config.go`)
- Covered: 13
- Uncovered (behavioral): 0
- Uncovered (implementation detail): 26
- Contradictions: 0
- Unimplemented spec promises: 0
- Test-alignment gaps: 2

## Branch Coverage

### `internal/daemon/run.go` – Start

1. **[UNCOVERED-IMPLEMENTATION]** L38 `if cfg == nil` – defaults cfg via `config.Default()`. Convenience for callers; not a spec requirement.

2. **[UNCOVERED-IMPLEMENTATION]** L41 `if log == nil` – defaults log via `slog.Default()`. Implementation detail.

3. **[UNCOVERED-IMPLEMENTATION]** L45/46 `cfg.EnsureStateDir()` error path – wraps mkdir failure. Implementation detail; spec is silent on state-dir creation semantics.

4. **[COVERED]** L49/50 `cfg.LoadToken()` error path returns early before any other subsystem boots.
   Spec: `openspec/specs/hera-coordination/spec.md` § "Scope token loaded from filesystem; missing token aborts startup" – "If the file is missing or empty, the daemon MUST exit with status code 1 and a stderr message instructing the user to run `argus token mint --scope hera`...". The error propagates to `Run`, which returns it to `cmd/hera` for the actual `os.Exit(1)`.

5. **[UNCOVERED-IMPLEMENTATION]** L53/54 `db.Open` error path – wraps DB open failure. Spec doesn't dictate this specific branch's behavior beyond "open DB" being part of startup.

6. **[UNCOVERED-IMPLEMENTATION]** L64/66 `mcp.GenerateAuthHeader` error path – cleans up DB and returns error. Implementation detail.

7. **[UNCOVERED-IMPLEMENTATION]** L70/72 `mcpSrv.Start` error path – cleans up DB and returns error. Implementation detail.

8. **[COVERED]** L86–88 `for _, def := range toolDefinitions() { registrar.Add(def) }` – iterates over all five tool defs and registers each.
   Spec: § "Five MCP tools exposed under the `hera_` prefix" – "The system SHALL register exactly five MCP tools with argus when the daemon starts: `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`."

9. **[UNCOVERED-IMPLEMENTATION]** L89/92 `registrar.Start` error path – rolls back `mcpSrv.Stop()` and `database.Close()`. Implementation detail; ordering is correct (cleanup in reverse of acquisition).

10. **[COVERED]** L95–98 subscriber wired with adopt, resync, and idle-tracker handlers, then started in its own goroutine.
    Spec: § "Auto-adopt coordinator-spawned worker tasks", § "Event stream cursor persisted and replayed", § "Idle gate requires sustained `session.idle` state" – the wiring puts all three handler chains in place. Coverage of the handler logic itself is in the events/ and idle/ modules.

11. **[UNCOVERED-IMPLEMENTATION]** L102 `if err := subscriber.Run(ctx); err != nil && ctx.Err() == nil` – logs warning only when the subscriber exits for a reason other than ctx cancellation. Implementation detail (observability); not a spec branch.

12. **[UNCOVERED-IMPLEMENTATION]** L113 final return constructing the Daemon struct – not a real branch.

### `internal/daemon/run.go` – Stop

13. **[UNCOVERED-IMPLEMENTATION]** L123/124 `if d == nil` – nil-safety guard so Stop is safe to call twice.

14. **[COVERED]** L126 `if d.Registrar != nil { _ = d.Registrar.Stop(ctx) }` – delegates to `Registrar.Stop`, which DELETEs each registered tool (verified in the internal-mcp module).
    Spec: § "MCP tool registrations heartbeated and unregistered on shutdown" – "On graceful shutdown (SIGINT/SIGTERM), hera MUST DELETE each registered tool via `DELETE /api/mcp/tools/{name}` before exiting." Stop's ordering (Registrar.Stop → MCPServer.Stop → DB.Close) ensures the DELETEs happen first.

15. **[UNCOVERED-IMPLEMENTATION]** L129 `if d.MCPServer != nil { _ = d.MCPServer.Stop() }` – shuts the callback HTTP listener. Implementation detail.

16. **[UNCOVERED-IMPLEMENTATION]** L132 `if d.DB != nil { _ = d.DB.Close() }` – closes SQLite. Implementation detail.

### `internal/daemon/run.go` – Run

17. **[UNCOVERED-IMPLEMENTATION]** L142/143 `Start` error path – propagates startup error to the caller (cmd/hera).

18. **[UNCOVERED-IMPLEMENTATION]** L147 `if err := os.WriteFile(cfg.PIDPath(), ...)` – best-effort PID-file write, logs `Warn` on failure. Spec is silent on PID files.

19. **[UNCOVERED-IMPLEMENTATION]** L152/153 `<-ctx.Done()` – the blocking wait until SIGINT/SIGTERM cancels ctx. The wait itself is implementation detail; the resulting Stop is covered.

### `internal/daemon/run.go` – toolDefinitions

20. **[COVERED]** L158–225 returns five `ToolDefinition` entries (`hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`), each with a non-empty `description` and an `input_schema` whose `properties` covers every documented parameter and lists `cwd` in `required`.
    Spec: § "Five MCP tools exposed under the `hera_` prefix" (exactly five tools, force-prefixed `hera_`, `cwd` declared as input parameter), § "Tool inputs and outputs documented" (description ≥10 chars, schema covers every parameter), § "Default message routing for worker and freelance senders" (the description for `hera_send` notes worker/freelance → coordinator).

### `internal/config/config.go` – Default, TokenPath, StatePath, PIDPath, LogPath, EnsureStateDir

21. **[UNCOVERED-IMPLEMENTATION]** L43–55 `Default()` – returns the v1 defaults (`~/.hera`, `127.0.0.1:7743`, `127.0.0.1:7744`, `2s`, `5m`). The defaults themselves are partially observable in spec (`5m` heartbeat, `2s` idle debounce) but the function body is pure construction.

22. **[UNCOVERED-IMPLEMENTATION]** L59–61 `TokenPath()` – returns `<StateDir>/api-token`. The path is dictated by spec ("read from `~/.hera/api-token`") but this single-line getter is plumbing.

23. **[UNCOVERED-IMPLEMENTATION]** L64–66 `StatePath()` – returns `<StateDir>/state.sqlite`. Implementation detail.

24. **[UNCOVERED-IMPLEMENTATION]** L69–71 `PIDPath()` – returns `<StateDir>/hera.pid`. Implementation detail.

25. **[UNCOVERED-IMPLEMENTATION]** L74–76 `LogPath()` – returns `<StateDir>/hera.log`. Implementation detail; not referenced by other modules in this audit (cmd/hera consumer probably uses it).

26. **[UNCOVERED-IMPLEMENTATION]** L108–110 `EnsureStateDir()` – `os.MkdirAll(StateDir, 0o700)`. Token-grade perms; spec is silent on perm bits but `0o700` is appropriate for a token store.

### `internal/config/config.go` – LoadToken

27. **[COVERED]** L84/85/86 `os.ReadFile` returns `os.IsNotExist(err)` → returns an error string that includes the path AND `Run: argus token mint --scope hera > <path>`.
    Spec: § "Scope token loaded from filesystem; missing token aborts startup" – Scenario: Token file missing – "hera MUST print an instructional error message to stderr AND exit with status 1". The wording requirement ("instructing the user to run `argus token mint --scope hera`") is met.

28. **[UNCOVERED-IMPLEMENTATION]** L93 read-error path (not IsNotExist) – wraps generic file-read errors (e.g., permission denied). Spec covers missing/empty specifically; this is the catch-all.

29. **[COVERED]** L96/97 `if token == ""` after `strings.TrimSpace` → returns the empty-file instructional error.
    Spec: § "Scope token loaded from filesystem; missing token aborts startup" – Scenario: Token file empty – "exists but contains only whitespace ... print an instructional error message to stderr AND exit with status 1". The `TrimSpace` covers the whitespace-only case.

30. **[COVERED]** L103 success path – returns the trimmed token.
    Spec: § "Scope token loaded from filesystem; missing token aborts startup" – Scenario: Token file present – "hera MUST proceed through the rest of its startup sequence".

## Cross-module dependencies (covered elsewhere)

- `os.Exit(1)` on token failure: the spec requires status-1 exit, but `daemon.Run` returns an error rather than calling `os.Exit`. The actual `os.Exit(1)` is the responsibility of `cmd/hera`; this is a cross-module dependency, not a gap in the wiring module. Verify in the `cmd-hera` module report.

- The five tool registrations' DELETE-on-shutdown behavior depends on `mcp.Registrar.Stop`. Verified in the `internal-mcp` module's coverage of `Registrar`.

- The heartbeat cadence (5-minute re-POST) depends on `mcp.Registrar` honouring `SetHeartbeat(cfg.MCPHeartbeat)`. The wiring code sets it correctly; the heartbeat loop itself is covered in `internal-mcp`.

## Test-Alignment Gaps

1. **`TestDaemonStart_TokenMissing` (run_test.go:138–161)** only checks `strings.Contains(err.Error(), "api-token")`. The spec requires the error message to instruct the user to run `argus token mint --scope hera`. The implementation does include that wording, but the test does not assert it – so a future regression that drops the actionable suggestion would not fail this test. Recommend asserting `strings.Contains(err.Error(), "argus token mint --scope hera")` to match the spec wording requirement.

2. **`TestLoadToken_FileEmpty` (config_test.go:41–55)** only checks for the substring `"empty"`. The spec's empty-token scenario also requires an "instructional error message" (same shape as the missing-file case). The implementation's empty-file error string does include `"Run: argus token mint --scope hera > %s"`, but the test does not pin that wording. The missing-file test (`TestLoadToken_FileMissing`) does assert `"argus token mint"`, so this asymmetry is a coverage gap, not a contradiction. Recommend extending the assertion to also check for `"argus token mint"` in the empty case.

## Unimplemented Spec Promises

None within this module. The token-loader, the five-tool registration list, and the reverse-order shutdown all map to concrete code in `run.go` / `config.go`.

## Notes on potentially-suspicious behavior (not flagged as contradictions per the spec-misreading guard)

- **`cwd` as "first input parameter":** the spec says each tool's input schema MUST declare `cwd` as the first input parameter. Go `map[string]any` does not preserve insertion order, and `encoding/json` sorts map keys alphabetically on Marshal. So in the on-wire JSON, `cwd` will not appear first for `hera_join` (alphabetically: `constraints`, `cwd`, `kind`, ...) or `hera_send` (`body`, `cwd`, `in_reply_to`, `to`). However, `cwd` is always listed first in the `required` array, and the spec phrasing is ambiguous as to whether "first parameter" refers to JSON key order, documented call-signature order, or required-list order. The current `required` ordering is plausibly compliant. Flagging here as a watch-item rather than a contradiction.

- **Subscriber teardown:** `Stop` does not explicitly cancel or wait for the subscriber goroutine; it relies on the outer `ctx` (passed to `Start` / `Run`) being cancelled. As long as production callers cancel ctx before `Stop` runs (which `Run` does via `<-ctx.Done()` followed by deferred `Stop`), this is correct. A `Stop` called with a fresh ctx while the subscriber's original ctx is still live would leave the subscriber running after Stop returns. Not a spec violation, but worth noting.
