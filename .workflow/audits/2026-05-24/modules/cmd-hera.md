# cmd-hera

## Summary

- Behavioral branches: 64
- Covered: 1
- Uncovered (behavioral): 6
- Uncovered (implementation detail): 57
- Contradictions: 0
- Unimplemented spec promises: 1 (resume; explicitly deferred by spec/design — see notes)

## Scope and orientation

The `cmd/hera` package is the Cobra-rooted CLI entrypoint. It wires five verbs:

- `start` — runs the daemon via `daemon.Run`
- `stop` — sends SIGTERM to the PID in `~/.hera/hera.pid` and waits up to 10s
- `status` — prints daemon liveness + DB summary
- `list` — prints orchestrator/role/binding inventory
- `resume` — stubbed; explicitly deferred per design

The hera-coordination spec is overwhelmingly about daemon-internal behavior (MCP tool handlers, event-stream cursor, idle gate, auto-adoption, etc.). The CLI surface is **not** directly described by the spec — it's the operator-facing wrapper. As a result, almost all branches in `cmd/hera` are correctly classified as `[UNCOVERED-IMPLEMENTATION]` (CLI plumbing, error formatting, defensive checks). The notable exceptions are the foreground-only gate in `start.go` and the `resume` stub, both of which encode promises/limitations that the spec does or could describe.

## Branch Coverage

### main.go::newRootCmd

1. **[UNCOVERED-IMPLEMENTATION]** `return root` after subcommand registration. CLI assembly; not a behavioral branch.

### main.go::main

1. **[UNCOVERED-IMPLEMENTATION]** `if err := newRootCmd().Execute(); err != nil` — prints to stderr and exits 1 on any cobra error. Standard CLI exit-status plumbing. Note: the spec only mandates `exit 1` for the missing/empty token file case (§ "Scope token loaded from filesystem; missing token aborts startup"); that path is enforced inside `daemon.Run` (and surfaces here only by way of `Execute` returning that error). The behavioral promise is covered downstream, not here.

### start.go::newStartCmd

1. **[UNCOVERED-BEHAVIORAL]** `if !foreground { return fmt.Errorf("hera start without --foreground is not yet implemented; ...") }` — running `hera start` without `--foreground` returns an error rather than starting the daemon. Because the spec promises the daemon SHALL start (e.g., § "Five MCP tools exposed under the `hera_` prefix": "WHEN the hera daemon completes startup successfully"), and § "MCP tool registrations heartbeated and unregistered on shutdown" presumes a running daemon, the operator-facing default path of `hera start` not actually starting hera is an externally observable limitation. The spec doesn't say "daemonization is deferred." Intentional? (Aligns with `design.md` per the inline comment, but the archived spec doesn't carry the deferral.)

2. **[UNCOVERED-IMPLEMENTATION]** Early return propagating `daemon.Run(ctx, cfg, logger)` error. Standard error propagation.

3. **[UNCOVERED-IMPLEMENTATION]** `signal.NotifyContext(... SIGINT, SIGTERM)` shutdown plumbing wraps the call. The graceful-shutdown spec § "MCP tool registrations heartbeated and unregistered on shutdown" mandates DELETEs before exit; that's `daemon.Run`'s job, not CLI's. CLI just delivers the cancellation context.

4. **[UNCOVERED-IMPLEMENTATION]** Returning the cobra command pointer. Construction-only.

### stop.go::newStopCmd (14 branches at lines 17, 24, 25, 26, 28, 31, 32, 35, 36, 38, 39, 45, 47, 51)

1. **[UNCOVERED-IMPLEMENTATION]** Read `~/.hera/hera.pid`; on `os.IsNotExist` return an instructive "no PID file ... is hera running?" error. Spec is silent on `hera stop` CLI ergonomics. Defensive/operator UX only.

2. **[UNCOVERED-IMPLEMENTATION]** Generic read-error wrapping (`read pidfile: %w`). Error formatting.

3. **[UNCOVERED-IMPLEMENTATION]** Parse PID with `strconv.Atoi(strings.TrimSpace(...))`; on parse failure return "malformed pidfile" error. Defensive.

4. **[UNCOVERED-IMPLEMENTATION]** `os.FindProcess(pid)` error — on Unix this never returns an error in practice; defensive only.

5. **[UNCOVERED-IMPLEMENTATION]** `proc.Signal(syscall.SIGTERM)` error — wrap and return. The spec § "MCP tool registrations heartbeated and unregistered on shutdown" says SIGTERM triggers graceful shutdown; sending SIGTERM is the matching CLI action. Not a behavioral branch of its own — it's the trigger, not the behavior.

6. **[UNCOVERED-BEHAVIORAL]** `for time.Now().Before(deadline) { ... } return fmt.Errorf("hera (pid %d) did not exit within 10s", pid)` — the CLI waits up to 10 seconds for the PID file to disappear, then errors. The spec doesn't mention a stop-wait window or that the PID file is the liveness signal. Intentional? Operator-only contract, but observable (e.g., scripts that chain stop→start).

7. **[UNCOVERED-IMPLEMENTATION]** Success path: PID file gone, print "hera (pid %d) stopped." to stdout. Operator UX.

### status.go::newStatusCmd (11 branches at lines 18, 26, 28×2, 30, 37, 39, 43, 44, 60, 66)

1. **[UNCOVERED-IMPLEMENTATION]** Three-way if/elsif/else for daemon state: `running` → "hera: running (pid %d)"; stale PID present but signal(0) fails → "hera: stale pidfile (pid %d not alive)"; no PID file → "hera: not running". Operator UX; not specced.

2. **[UNCOVERED-IMPLEMENTATION]** `os.Stat(cfg.StatePath()); os.IsNotExist` → print "state: (no SQLite yet)" and return early. Defensive: status before first start.

3. **[UNCOVERED-IMPLEMENTATION]** `db.Open` error wrap (`open db: %w`).

4. **[UNCOVERED-BEHAVIORAL]** Prints `argus_url`, `mcp_addr`, `state_path`, `last_event_id`, `orchestrator_cnt`, plus per-orchestrator `roles` and `live bindings`. The spec touches several of these surfaces (e.g., event cursor in § "Event stream cursor persisted and replayed", orchestrators/roles/bindings) but does not specify any CLI inspection format. The status output is an inspection contract that scripts/users may come to rely on. Intentional that it's CLI-only and not specced?

5. **[UNCOVERED-IMPLEMENTATION]** Inner loop branch `if _, err := database.Bindings.GetLiveByRole(...); err == nil { live++ }` — counts live bindings per role. Error from `GetLiveByRole` (including `db.ErrNotFound`) is silently treated as "no live binding". This drops `ErrNotFound` correctly but also swallows real DB errors. Defensive but slightly noisy on real errors — flag below as well.

6. **[UNCOVERED-IMPLEMENTATION]** Final `return nil`. End of happy path.

### status.go::pidIsAlive (9 branches at lines 74, 75, 78, 79, 82, 83, 86, 87, 89)

1. **[UNCOVERED-IMPLEMENTATION]** Read pidfile; on error → `(false, 0)`. Pure helper for the CLI status display, not spec-bound.

2. **[UNCOVERED-IMPLEMENTATION]** Parse PID; on error → `(false, 0)`.

3. **[UNCOVERED-IMPLEMENTATION]** `os.FindProcess(pid)` — on error → `(false, pid)`. Defensive.

4. **[UNCOVERED-IMPLEMENTATION]** `proc.Signal(syscall.Signal(0)); err != nil` → `(false, pid)` — Unix liveness probe.

5. **[UNCOVERED-IMPLEMENTATION]** Default fallthrough → `(true, pid)`.

### list.go::newListCmd (15 branches at lines 16, 22, 24, 27, 28, 34, 35, 37, 39, 44, 45, 49, 51×2, 52, 57)

1. **[UNCOVERED-IMPLEMENTATION]** No DB yet → "(no orchestrators – hera has never started)" and return nil. Defensive first-run UX.

2. **[UNCOVERED-IMPLEMENTATION]** `db.Open` error wrap.

3. **[UNCOVERED-IMPLEMENTATION]** `Orchestrators.List` error propagated.

4. **[UNCOVERED-IMPLEMENTATION]** Empty list → "(no orchestrators)" and return. UX.

5. **[UNCOVERED-IMPLEMENTATION]** Per-orchestrator loop printing name.

6. **[UNCOVERED-IMPLEMENTATION]** `Roles.ListByOrchestrator` error propagated.

7. **[UNCOVERED-IMPLEMENTATION]** Per-role branch: if `Bindings.GetLiveByRole` returns nil → `"live (task %s)"` with binding's `ArgusTaskID`. If error is `db.ErrNotFound` → `"none"`. Otherwise → propagate error. Correctly distinguishes "not found" from real errors. The spec § "Roles live in argus projects; orchestrators do not" implies the role row carries `argus_project`; this CLI prints `r.ArgusProject`. That's consistent with the spec but the spec doesn't mandate this CLI surface.

8. **[UNCOVERED-BEHAVIORAL]** Prints `r.Name [r.Kind, r.ArgusProject]: <state>` — exposes `kind` (worker/freelance/coordinator) and `argus_project` as operator-visible attributes. Same theme as status: not a specced surface but observable. Intentional?

### resume.go::newResumeCmd

1. **[UNCOVERED-IMPLEMENTATION]** `Args: cobra.ExactArgs(1)` — argument-count check.

2. **[COVERED]** `RunE: return errNotYetImplemented("hera resume (deferred from v1)")` — Stub-only. The current `hera-coordination` base spec at `openspec/specs/hera-coordination/spec.md` does NOT contain a § for `hera resume` (the Requirements list has 19 entries, none describing `resume`). The deferral is consistent with how the spec is currently published — there is nothing for it to contradict. The inline comment cites `openspec/changes/hera-v1/tasks.md` task 16.7 and the design doc; those say "stub in v1" per the trigger phrase guard ("deferred", "stub"). Treat as covered by absence: the spec does not promise `resume`, so the stub does not contradict the spec.

   - Side note for the "Unimplemented Spec Promises" section: § "Roles live in argus projects; orchestrators do not" has a scenario "Role's argus_project preserved across incarnation" that says: "**WHEN** ... then `hera resume` creates a new binding for the same role — **THEN** the new binding MUST be in argus project `foo-frontend`". That scenario mentions `hera resume` by name. Since the verb is stubbed, this scenario cannot be exercised today. See Unimplemented Spec Promises below.

### errors.go::errNotYetImplemented

1. **[UNCOVERED-IMPLEMENTATION]** Returns a formatted error message. Pure helper; not spec-bound.

## Unimplemented Spec Promises

- `openspec/specs/hera-coordination/spec.md` § "Roles live in argus projects; orchestrators do not" — scenario "Role's argus_project preserved across incarnation": "**WHEN** a role's first binding was created in argus project `foo-frontend` and that binding ends, then `hera resume` creates a new binding for the same role — **THEN** the new binding MUST be in argus project `foo-frontend`". The `hera resume` verb is stubbed (`resume.go` returns `errNotYetImplemented`), so the scenario's WHEN clause cannot be satisfied. The base spec does not flag `resume` as deferred (no "TODO", "deferred", or "not yet implemented" trigger phrase in the spec itself — only in the code and design doc). Medium severity: the scenario is unreachable from the CLI surface as shipped.

## Cross-module symbol awareness

All cross-module symbols referenced from `cmd/hera` resolve in the symbols index:

- `config.Default`, `cfg.PIDPath`, `cfg.StatePath`, `cfg.ArgusBaseURL`, `cfg.ListenAddr` — `internal/config`
- `daemon.Run` — `internal/daemon`
- `db.Open`, `database.Orchestrators.List`, `database.Roles.ListByOrchestrator`, `database.Bindings.GetLiveByRole`, `database.EventCursor.Get`, `db.ErrNotFound` — `internal/db`

No `missing-symbol` findings.

## Notes on the "foreground only" gate

The hardest call in this audit is `start.go:30` — `if !foreground { return error }`. Two readings:

- (a) This is a temporary engineering posture (design doc says deferred). Spec is silent because it considers "how hera starts" out of scope.
- (b) The spec describes a running daemon and assumes hera starts. A `hera start` that doesn't start violates an operator's reasonable expectation.

I'm flagging it as `[UNCOVERED-BEHAVIORAL]` rather than `[CONTRADICTS]` because the spec applies the SPEC-MISREADING GUARD here only weakly (the spec itself doesn't say "deferred"; the design doc does, but that's not where the audit's spec source lives). It's a question of intent for the spec author: should the spec carry the `--foreground`-only limitation, or should the CLI gain real daemonization?
