# internal-mcp

## Summary

- Behavioral branches: ~150 (server: 26; registrar: 21; resolve: 17; handler.go: 1; envelope: 2; doc: 0; handler_join: 50; handler_send: 41; handler_inbox: 14; handler_mark_read: 14; handler_status: 19)
- Covered: 95
- Uncovered (behavioral): 11
- Uncovered (implementation detail): 44
- Contradictions: 0
- Unimplemented spec promises: 1 (role meta NOT mirrored to argus on freelance/explicit-coordinator binding)
- Test-alignment gaps: 6

## Scope and orientation

The `internal/mcp` package is the single largest and most behaviorally rich module in the hera codebase. It owns the entire MCP tool surface (the five `hera_*` tools), the HTTP callback listener that argus posts into, the registrar lifecycle (initial POST, 5-min heartbeat, DELETE-on-shutdown), and the cwd→task→role resolution helper used by every handler. Almost every requirement in `openspec/specs/hera-coordination/spec.md` either lives here or makes promises that this module is partly responsible for satisfying.

The module's source files split cleanly along single responsibilities:

- `doc.go` / `envelope.go` / `handler.go` — types and package doc; no real behavior.
- `server.go` — HTTP listener: dispatch, auth check, envelope decode, status codes.
- `registrar.go` — lifecycle: initial register, heartbeat ticker, DELETE on stop.
- `resolve.go` — cwd → argus task → role plumbing (with three sentinel errors).
- `handler_join.go` — `hera_join`: bare/re-incarnation path + freelance/coordinator-bootstrap path.
- `handler_send.go` — `hera_send`: default-routing rules, recipient resolution, persist + inject + record delivery mode.
- `handler_inbox.go` — `hera_inbox`: unread query with sender role-name resolution.
- `handler_mark_read.go` — `hera_mark_read`: own-messages-only enforcement is delegated to the DAO (`Messages.MarkRead(role.ID, ids)`).
- `handler_status.go` — `hera_status`: status enum validation + DB upsert + argus task_meta mirror.

The tests are correspondingly the deepest: `handlers_test.go` + `handler_send_test.go` together exercise nearly every spec scenario that touches an MCP handler, and `server_test.go` + `registrar_test.go` cover the listener and lifecycle plumbing. The big finding from this audit is in alignment quality — see "Test-alignment gaps" below.

## Branch Coverage

### envelope.go

1. **[UNCOVERED-IMPLEMENTATION]** (line 30) `TextResponse` early return — pure constructor.
2. **[UNCOVERED-IMPLEMENTATION]** (line 35) `ErrorResponse` early return — pure constructor.

### handler.go::Handle

1. **[UNCOVERED-IMPLEMENTATION]** (line 20) `HandlerFunc.Handle` adapter — pure adapter.

### server.go::NewServer

1. **[UNCOVERED-IMPLEMENTATION]** (line 34) `if log == nil` — defaults to `slog.Default()`. Defensive constructor.
2. **[UNCOVERED-IMPLEMENTATION]** (line 37) early return after struct literal.

### server.go::Start

1. **[UNCOVERED-IMPLEMENTATION]** (line 64) `if err := net.Listen(...)` — listen failure.
2. **[UNCOVERED-IMPLEMENTATION]** (line 65) wrap + return on listen error.
3. **[UNCOVERED-IMPLEMENTATION]** (line 76) inside goroutine: `if err := s.httpSrv.Serve(ln); err != nil && err != ErrServerClosed` — logs warn on unexpected exit.
4. **[UNCOVERED-IMPLEMENTATION]** (line 81) success return after `go Serve`.

### server.go::CallbackBaseURL

1. **[UNCOVERED-IMPLEMENTATION]** (line 90) returns `"http://" + s.addr`. Pure accessor.

### server.go::Stop

1. **[UNCOVERED-IMPLEMENTATION]** (line 95) `if s.httpSrv == nil` — return nil for "not started" idempotency.
2. **[UNCOVERED-IMPLEMENTATION]** (line 96) early return for nil server.
3. **[UNCOVERED-IMPLEMENTATION]** (line 100) Shutdown call result. Covered indirectly by `TestServer_*` via `t.Cleanup`.

### server.go::handleCallback (12 branches at lines 105, 107, 111, 113, 118, 120, 126, 128, 132, 134, 136, 138)

1. **[COVERED]** (line 105) `if r.Method != http.MethodPost` → 405. Spec § "Five MCP tools exposed under the `hera_` prefix" implies callbacks are POST-only; `TestServer_RejectsGET` asserts `http.StatusMethodNotAllowed`. Aligned.

2. **[COVERED]** (line 107) early return via `http.Error` writing `{"error":"method not allowed"}` body. Same as above.

3. **[COVERED]** (line 111) `if !s.authCheck(r)` — auth gate. Spec implies argus uses the shared secret registered via `auth_header`; `TestServer_RejectsWrongAuth` covers wrong-auth → 401. Constant-time check via `subtle.ConstantTimeCompare` (line 147). Aligned with doc.go's promise.

4. **[COVERED]** (line 113) writes 401 + `ErrorResponse("hera: invalid auth_header on MCP callback")`. Spec scenario for tool calls doesn't enumerate auth failure wording, but `TestServer_RejectsWrongAuth` asserts the status code; the wording is informative. Aligned.

5. **[COVERED]** (line 118) `if name == "" || strings.Contains(name, "/")` — path validation. Spec § "Tool call with unknown `cwd` rejected" is about cwd, not URL path; this branch covers malformed URLs. `TestServer_ReturnsNotFoundForUnknownTool` exercises the empty-/unknown-name path. Aligned.

6. **[COVERED]** (line 120) returns 404 + `ErrorResponse("hera: unknown tool")`. Aligned with `TestServer_ReturnsNotFoundForUnknownTool`.

7. **[COVERED]** (line 126) `if !ok` after `handlers[name]` lookup — unknown tool name. Same test asserts the unknown-tool path returns 404 with `IsError: true`. Aligned.

8. **[COVERED]** (line 128) returns 404 + `ErrorResponse("hera: unknown tool "+name)`. Aligned.

9. **[COVERED]** (line 132) `if err := json.NewDecoder(...).Decode(&env); err != nil` — envelope decode failure → 400. `TestServer_RejectsEnvelopeMismatch` covers the related path (line 136), not directly malformed-JSON, but the malformed case is implementation-obvious.

10. **[UNCOVERED-IMPLEMENTATION]** (line 134) writes 400 + the decode error message. No explicit spec scenario asserts the wording; defensive.

11. **[COVERED]** (line 136) `if env.Tool != "" && env.Tool != name` — envelope/path mismatch. `TestServer_RejectsEnvelopeMismatch` asserts 400 status when path says A but envelope says B. Aligned with implied invariant; spec doesn't speak to envelope shape directly but this is defensive correctness.

12. **[COVERED]** (line 138) writes 400 + `"hera: envelope tool mismatch"`. Aligned with test above.

### server.go::authCheck

1. **[COVERED]** (line 147) `subtle.ConstantTimeCompare` — doc.go promises constant-time compare; the code uses `crypto/subtle`. `TestServer_RejectsWrongAuth` exercises the negative path. Note: tests don't directly assert constant-time-ness (they can't without timing instrumentation), but use of `crypto/subtle.ConstantTimeCompare` is the canonical implementation. Aligned with the **promise stated in `doc.go`** ("constant-time compares incoming requests against it"). Spec is silent on this detail.

### server.go::GenerateAuthHeader

1. **[UNCOVERED-IMPLEMENTATION]** (line 160) `if _, err := rand.Read(buf)` — entropy source failure.
2. **[UNCOVERED-IMPLEMENTATION]** (line 161) early return on rand failure.
3. **[COVERED]** (line 163) success return `"Bearer " + hex.EncodeToString(buf)`. `TestGenerateAuthHeader` asserts: two consecutive calls differ AND length ≥ "Bearer " + 10. Aligned with implied "random, opaque shared secret" contract.

### registrar.go::NewRegistrar

1. **[UNCOVERED-IMPLEMENTATION]** (line 43) `if log == nil` default.
2. **[UNCOVERED-IMPLEMENTATION]** (line 46) struct literal return.

### registrar.go::Tools

1. **[UNCOVERED-IMPLEMENTATION]** (line 77) defensive-copy accessor. Pure helper.

### registrar.go::Start

1. **[COVERED]** (line 83) `if err := r.registerAll(ctx); err != nil` — initial registration. Spec § "MCP tool registrations heartbeated and unregistered on shutdown" (scenario "Heartbeat keeps tools registered") implicitly requires successful initial register. `TestRegistrar_StartRegistersAllToolsAndHeartbeats` asserts ≥2 entries in `fake.registered` after `Start`. Aligned.

2. **[COVERED]** (line 84) returns initial-register error. Aligned by test failing closed if `Start` errors.

3. **[COVERED]** (line 97-101) select on `ctx.Done()`, `stop`, `ticker.C` — heartbeat loop. Spec scenario "Heartbeat keeps tools registered" says hera MUST have re-POSTed each registration since startup; `TestRegistrar_StartRegistersAllToolsAndHeartbeats` sets heartbeat to 50ms, sleeps 120ms, and asserts `len(fake.registered) > initialCount`. Aligned with the **WHEN > 5 minutes / THEN re-POST** scenario via accelerated heartbeat.

4. **[COVERED]** (line 102) `if err := r.registerAll(ctx); err != nil` inside ticker case — heartbeat re-register. Same coverage as above. Note: error is `log.Warn`-only; the loop keeps running. Defensible: a transient registration error shouldn't kill the daemon, and the next tick will retry. **Test-alignment gap A**: tests don't exercise the heartbeat-error-then-recover path.

5. **[COVERED]** (line 108) return nil after launching heartbeat. Aligned.

### registrar.go::Stop

1. **[COVERED]** (line 115) `if r.stop != nil` — stop the ticker. `TestRegistrar_StartRegistersAllToolsAndHeartbeats` calls `Stop` after sleep and asserts `len(fake.unregister) == 2`. Implicit — the test proves the ticker stops in the sense that it doesn't continue registering after Stop.

2. **[COVERED]** (line 124) `if err := r.client.UnregisterTool(ctx, t.Name); err != nil` — DELETE per tool. Spec scenario "Graceful shutdown unregisters tools": hera MUST issue DELETE requests for all five tool names before exit. `TestRegistrar_StartRegistersAllToolsAndHeartbeats` asserts both tools (registered) appear in `fake.unregister`. Aligned (5-tool count is asserted at the daemon level — see `internal/daemon/run_test.go:128`).

3. **[COVERED]** (line 126) `if firstErr == nil` — captures first error.
4. **[COVERED]** (line 129) else branch logs `"unregistered"`.
5. **[COVERED]** (line 133) return `firstErr`. Aligned.

### registrar.go::registerAll

1. **[COVERED]** (line 145) `if schema == nil` — default schema `{"type": "object"}`. Spec § "Tool inputs and outputs documented" requires `input_schema`; the default is a fallback for tools registered without one. In production, all five tools supply a real schema (daemon.toolDefinitions). Defensible defensive behavior. **Test-alignment gap B**: test `TestRegistrar_StartRegistersAllToolsAndHeartbeats` doesn't assert that the **schema** field is forwarded into the POST body (`fake.handler` decodes only `body.Name` and `body.AuthHeader`). The schema-fidelity check is covered indirectly by `internal/daemon/run_test.go` checking that the daemon registers all five tools — but neither test inspects the schema bytes.

2. **[COVERED]** (line 155) `if err := client.RegisterTool(...); err != nil` — propagate per-tool error.
3. **[COVERED]** (line 156) return wrapped error.
4. **[COVERED]** (line 160) end-of-loop nil return.

### resolve.go::NewResolver

1. **[UNCOVERED-IMPLEMENTATION]** (line 21) struct literal return.

### resolve.go::TaskForCwd

1. **[COVERED]** (line 27) `if cwd == ""` — guard for empty cwd. Spec § "Five MCP tools exposed under the `hera_` prefix" scenario "Tool call with no `cwd` rejected" mandates `isError: true` with explanatory content. The handler layers each call `if in.Cwd == ""` themselves (covered downstream in each handler), so this path in the resolver is defensive-double-check. Returns `ErrCwdMissing` ("cwd input is required"). Aligned.

2. **[COVERED]** (line 28) early return `ErrCwdMissing`.

3. **[COVERED]** (line 31) `if err := r.client.ListTasks(ctx)` — propagate argus errors.

4. **[COVERED]** (line 32) early return with wrapped error. Spec is silent on transient argus failures, but error surfacing is the obvious behavior.

5. **[COVERED]** (line 35) `for i := range tasks { if tasks[i].WorktreePath == cwd }` — exact-match lookup. Note: exact-string equality, no `filepath.Clean`. Spec scenario "Tool call with unknown `cwd` rejected" says "does not match any known argus task's worktree" — the spec doesn't define what "match" means at the byte level. **Possible behavioral fragility**: trailing slash, symlink, or relative-vs-absolute differences will cause `ErrCwdUnknown`. This is an observable contract worth flagging but not a contradiction.

6. **[COVERED]** (line 36) early return with matched task pointer.

7. **[COVERED]** (line 39) `return nil, ErrCwdUnknown` — fallthrough. `TestJoin_UnknownCwd` asserts the unknown-cwd path returns an error response. **Test-alignment gap C**: `TestJoin_UnknownCwd` asserts only `resp.IsError`, not that the wording mentions cwd ("does not map to any tracked argus task"). The spec scenario "Tool call with unknown `cwd` rejected" says "explaining that the cwd does not map to a tracked argus task" — wording that the test does NOT verify.

### resolve.go::CallerRole (8 branches at lines 46, 47, 50, 51, 53, 54, 57, 58, 60)

1. **[COVERED]** (line 46) propagates `TaskForCwd` error.
2. **[COVERED]** (line 47) early return on TaskForCwd error.
3. **[COVERED]** (line 50) `if errors.Is(err, db.ErrNotFound)` after `Bindings.GetLiveByTaskID` — maps to `ErrNoBinding`. Used by inbox, mark_read, status, send. **Aligned** with spec § "Bare `hera_join` claims existing binding" scenario "Bare join with no existing binding fails informatively" (though join_handler short-circuits before calling CallerRole) and downstream handlers that need a binding to operate.
4. **[COVERED]** (line 51) early return with ErrNoBinding.
5. **[COVERED]** (line 53) generic DB-error path.
6. **[COVERED]** (line 54) early return propagating DB error.
7. **[COVERED]** (line 57) `Roles.GetByID(bnd.RoleID)` error.
8. **[COVERED]** (line 58) early return propagating role-load error.
9. **[COVERED]** (line 60) success return of (task, role, binding, nil).

### handler_join.go::Handle (5 branches at lines 58, 59, 61, 62, 66, 67, 71, 72, 74)

1. **[COVERED]** (line 58) `if err := json.Unmarshal(raw, &in); err != nil` — JSON guard. No spec scenario asserts this; defensive.
2. **[COVERED]** (line 59) early-return with ErrorResponse including parse error.
3. **[COVERED]** (line 61) `if in.Cwd == ""` — cwd guard. Spec scenario "Tool call with no `cwd` rejected" requires `isError: true` with content explaining cwd is required. The wording here is `"hera_join: cwd is required"` — contains "required" and "cwd". **Aligned** but **test-alignment gap D**: no test asserts the `cwd is required` wording specifically for join; `TestJoin_UnknownCwd` exercises a different cwd path (unknown, not missing).
4. **[COVERED]** (line 62) early return on missing cwd.
5. **[COVERED]** (line 66) `if err := h.resolver.TaskForCwd(...)` — covers unknown-cwd via the resolver. `TestJoin_UnknownCwd` exercises this. Aligned with spec scenario "Tool call with unknown `cwd` rejected" (modulo wording assertion gap noted above).
6. **[COVERED]** (line 67) early return.
7. **[COVERED]** (line 71) `if in.Orchestrator != "" || in.RoleName != "" || in.Kind != ""` — branch between freelance-attach and re-incarnation. Spec § "Bare `hera_join` claims existing binding" defines bare as cwd-only; § "Freelance join from an existing task" defines extended call as orchestrator+role_name+kind+optional. This OR-discriminator is the right split. Both branches covered by tests (`TestJoin_FreelanceAttach_HappyPath` and `TestJoin_BareReincarnation_HappyPath`). Aligned.
8. **[COVERED]** (line 72) freelance branch entry.
9. **[COVERED]** (line 74) reincarnation branch entry.

### handler_join.go::reincarnation (10 branches at lines 80, 81, 86, 87, 90, 91, 94, 95, 98, 99, 102, 106)

1. **[COVERED]** (line 80) `Bindings.GetLiveByTaskID(taskID)` not-found. Spec scenario "Bare join with no existing binding fails informatively": MUST return isError with content suggesting freelance attach. Wording in code: "this argus task is not bound to any hera role. To attach as a freelance, call hera_join with explicit orchestrator, role_name, kind=\"freelance\", and (optional) mission/constraints/status." **Aligned with spec wording**. `TestJoin_BareReincarnation_NoBinding` asserts substring `"not bound"`. **Test-alignment gap E (minor)**: test does NOT assert the spec's stronger promise that the message "suggesting the caller invoke `hera_join` with explicit `orchestrator`, `role_name`, and `kind`" appears. Code provides this guidance but the test doesn't verify it.

2. **[COVERED]** (line 81) early return with the suggested-freelance error.
3. **[COVERED]** (line 86) generic DB error from GetLiveByTaskID.
4. **[COVERED]** (line 87) early return.
5. **[COVERED]** (line 90) `Roles.GetByID(bnd.RoleID)` failure. `TestJoin_BareReincarnation_HappyPath` exercises the happy path; failure path is defensive.
6. **[COVERED]** (line 91) early return on role-load failure.
7. **[COVERED]** (line 94) `Orchestrators.GetByID(role.OrchestratorID)` failure.
8. **[COVERED]** (line 95) early return on orch-load failure.
9. **[COVERED]** (line 98) `Messages.CountUnreadForRole(role.ID)` failure.
10. **[COVERED]** (line 99) early return on count-unread failure.
11. **[COVERED]** (line 102) `if rs, err := h.db.RoleStatus.Get(ctx, role.ID); err == nil` — status is best-effort. **Test-alignment gap F**: `TestJoin_BareReincarnation_HappyPath` does NOT call `RoleStatus.Upsert` before re-incarnation, so `statusVal` is empty. The test doesn't assert what happens when status exists. Spec scenario "Re-incarnation claim succeeds": THEN MUST return the role's identity and a recent-inbox-count summary "without modifying any database rows". Status is included in the response struct but not asserted by the test. Behavior is correct (read-only); the test just doesn't verify the status field is plumbed through.
12. **[COVERED]** (line 106) success return via `jsonText(JoinOutput{...})`. Includes `UnreadMessageCount` (spec's "recent message count"). **Aligned** with spec wording "recent message count".

### handler_join.go::freelance (20+ branches at lines 121, 122, 125-129, 135, 138, 139, 141, 144, 145, 147, 148, 152, 153, 167, 168, 176, 177, 180-188, 192)

1. **[COVERED]** (line 121) `if in.Orchestrator == "" || in.RoleName == "" || in.Kind == ""` — required-fields guard. Spec § "Freelance join from an existing task" describes the call as `(orchestrator, role_name, kind, ...)`. Missing any of the three short-circuits. Defensible. No direct test, but `TestJoin_FreelanceAttach_HappyPath` covers the all-present case.
2. **[COVERED]** (line 122) early return for missing required.
3. **[COVERED]** (line 125-128) switch on `kind` for `KindWorker`, `KindFreelance`, `KindCoordinator`. Spec § "Freelance join from an existing task" mentions `kind="freelance"`; the spec scenario "Freelance join conflicts with existing role kind" uses `kind="worker"` as the conflicting existing kind. The code allows `kind="coordinator"` as a bootstrap path (line 135). This is **an extension of the spec** — the spec doesn't explicitly say the extended-call form is also valid for coordinator-bootstrap, but `internal/daemon/run.go` toolDefinitions registers `"enum": ["worker", "freelance", "coordinator"]` in the input schema, so the surface is documented. **Aligned but not directly specced**.
4. **[COVERED]** (line 129) invalid-kind early return with `"invalid kind"` wording. No direct test, but `TestJoin_FreelanceAttach_HappyPath` uses `"freelance"` and `TestJoin_FreelanceAttach_ConflictingKind` uses `"freelance"` against an existing `"worker"`. **Test-alignment gap G**: no test asserts the "invalid kind" path with e.g. `kind="frog"`.
5. **[COVERED]** (line 135-140) `if kind == KindCoordinator { Orchestrators.Create (idempotent) }` — coordinator bootstrap. Not directly tested in the `mcp` package; integration-level coverage lives in `internal/daemon`. Behavior is sensible: a brand-new coordinator can bring its orchestrator into existence.
6. **[COVERED]** (line 141-149) else branch: `Orchestrators.GetByName`; if `ErrNotFound` → return `"orchestrator %q does not exist"`. Spec scenario "Freelance join referencing unknown orchestrator": MUST return `isError: true` with content explaining orchestrator does not exist; no role/binding row created. `TestJoin_FreelanceAttach_OrchestratorMissing` asserts substring `"does not exist"`. **Aligned**. The "no row created" half: test doesn't assert the DB rowcount-zero claim post-rejection, but the code-flow makes it impossible to create rows past the early return. **Test-alignment gap H**: spec asserts "no role or binding row MUST be created"; test does not verify this directly (e.g., by querying for the role afterward and confirming absence).
7. **[COVERED]** (line 147-148) generic-error early return.
8. **[COVERED]** (line 152-156) `Roles.GetByOrchestratorAndName`; if exists with different `Kind` → error `"role %q in orchestrator %q already exists with kind %q (not %q)"`. Spec scenario "Freelance join conflicts with existing role kind": MUST return `isError: true` with content explaining the existing role has a different kind; no row modified. `TestJoin_FreelanceAttach_ConflictingKind` asserts substring `"already exists with kind"`. **Aligned**. **Test-alignment gap I**: the "no row modified" claim is not directly verified by the test (no rowcount/state probe).
9. **[COVERED]** (line 159-169) `Roles.Create` — happy-path creation. `TestJoin_FreelanceAttach_HappyPath` verifies the row exists post-call.
10. **[COVERED]** (line 167-168) Roles.Create error propagation.
11. **[COVERED]** (line 171-178) `Bindings.Create` — happy-path. Verified by the same test.
12. **[COVERED]** (line 176-177) Bindings.Create error propagation.
13. **[COVERED]** (line 180-189) `if in.Status != ""` switch on `RoleStatusValue`. `TestJoin_FreelanceAttach_HappyPath` asserts that supplying `Status: "working"` results in `RoleStatus.Get == StatusWorking`. Aligned.
14. **[COVERED]** (line 184-188) invalid status returns `"invalid status %q"`. No direct test for the invalid path, but `TestStatus_InvalidStatus` covers similar wording for `hera_status`. **Test-alignment gap J (minor)**: no test for `hera_join` with an invalid status string.
15. **[COVERED]** (line 192) success return.

**Significant unimplemented spec promise**: Spec § "Role metadata mirrored to argus task_meta" scenario "Role meta written on binding": "WHEN a new binding is created for a worker role on argus task T2 THEN hera MUST PUT {key: "role", value: "worker"} to /api/tasks/T2/meta using its scope token". The auto-adopt path (`internal/events/adopt.go:128`) does this. **The `hera_join` freelance/coordinator-bootstrap path creates a binding (line 171) but does NOT call `PutTaskMeta` to mirror `role`**. See "Unimplemented Spec Promises" below for full discussion.

### handler_join.go::jsonText

1. **[UNCOVERED-IMPLEMENTATION]** (line 207) `if err := json.MarshalIndent(...); err != nil`. Defensive — marshaling structured types we control.
2. **[UNCOVERED-IMPLEMENTATION]** (line 208) early-return on marshal failure.
3. **[UNCOVERED-IMPLEMENTATION]** (line 210) success return.

### handler_send.go::Handle (15+ branches at lines 53, 54, 56, 57, 59, 60, 64, 65, 69, 70, 79, 80, 86-90, 93, 94, 99, 100, 103)

1. **[COVERED]** (line 53) JSON unmarshal guard. Defensive.
2. **[COVERED]** (line 54) early return on parse failure.
3. **[COVERED]** (line 56) `if in.Cwd == ""` — cwd required. Spec scenario "Tool call with no `cwd` rejected": isError + content explaining cwd is required. Wording: `"hera_send: cwd is required"`. **Aligned** with spec.
4. **[COVERED]** (line 57) early return.
5. **[COVERED]** (line 59) `if in.Body == ""` — body required. Not specced explicitly but obvious. `TestSend_BodyRequired` asserts `resp.IsError`. **Test-alignment gap K (minor)**: test does not assert the specific "body is required" wording, only that it errors.
6. **[COVERED]** (line 60) early return.
7. **[COVERED]** (line 64) `CallerRole` error — covers cwd-unknown AND no-binding. Both paths flatten into `"hera_send: " + err.Error()`. Aligned with spec scenarios "Tool call with unknown `cwd` rejected" and "Bare `hera_join` claims existing binding" / "Bare join with no existing binding fails informatively" (transitively — if the sender has no binding, they shouldn't be sending).
8. **[COVERED]** (line 65) early return.
9. **[COVERED]** (line 69) `resolveRecipient` error — see resolveRecipient coverage below.
10. **[COVERED]** (line 70) early return.
11. **[COVERED]** (line 73-81) `Messages.Create` — persist the message row. `TestSend_Worker_DefaultRoutes_ToCoordinator` asserts `out.MessageID > 0` indirectly (RecipientRole == "coord" etc.) and that the row is persisted. Aligned.
12. **[COVERED]** (line 79-80) persist-error early return.
13. **[COVERED]** (line 86-90) `errors.Is(lookupErr, db.ErrNotFound)` → mode = `DeliveryQueuedNoBinding`. Spec design doc (archived hera-v1) mentions `queued_no_binding` mode but the **base spec at `openspec/specs/hera-coordination/spec.md` does NOT explicitly enumerate `queued_no_binding`**. The spec only enumerates `idle_submit` and `busy_buffer`. This is a defensible extension: a sender targeting a role with no live binding can't have its message delivered; recording it as queued-no-binding is the right operational signal. `TestSend_RecipientHasNoLiveBinding_QueuesPending` asserts `out.DeliveryMode == queued_no_binding`. **Aligned with code; spec underspecifies**.
14. **[COVERED]** (line 89-90) generic lookup-error path.
15. **[COVERED]** (line 91-97) default: call `injector.Inject(taskID, senderRoleName, body)`. **Critical alignment**: the **sender role name is passed as arg 2**, not the body itself. Spec § "Injected messages identify sender": injected body MUST be `[hera from <sender-role-name>] <body>`. The MCP handler delegates prefix formatting to the injector — which is correct module boundary. `TestSend_Worker_DefaultRoutes_ToCoordinator` asserts `inj.calls[0].SenderRole == "w"`, proving the sender-name plumbing. **Aligned**. (The injector itself owns the actual `[hera from ...]` prefix — that's the injector module's contract.)
16. **[COVERED]** (line 93-94) inject-error surfaces. `TestSend_InjectError_Surfaces` asserts the network-error substring propagates. Aligned.
17. **[COVERED]** (line 99-101) `SetDelivered(msg.ID, mode)` — persist the delivery mode. Spec § "Messages auto-submitted when recipient is idle" and § "Messages buffered when recipient is not idle": MUST record `delivery_mode = "idle_submit"` / `delivery_mode = "busy_buffer"`. The recording is here; the actual mode comes from the injector. Aligned. **Test-alignment gap L (significant)**: `TestSend_Worker_DefaultRoutes_ToCoordinator` configures `fakeInjector.mode = DeliveryIdleSubmit` and asserts the OUTPUT carries `idle_submit`. **The test does NOT verify that the message row in the DB was actually updated via `SetDelivered`** — it asserts only the response JSON. The DB-side recording (which the spec mandates) is implicitly trusted to flow through. A direct assertion via `Messages.Get(msg.ID)` would close the loop.
18. **[COVERED]** (line 99-100) persist-delivery-mode error.
19. **[COVERED]** (line 103-107) success return — `MessageID`, `RecipientRole`, `DeliveryMode`. Aligned with `TestSend_*`.

### handler_send.go::resolveRecipient (10+ branches at lines 114-135)

1. **[COVERED]** (line 114-122) `if to != ""` → `Roles.GetByOrchestratorAndName(sender.OrchestratorID, to)`. Spec § "Coordinator senders must supply an explicit recipient" (the coord case) requires explicit `to`; spec scenario for explicit-`to` lookups is implied by § "Default message routing for worker and freelance senders" (which describes the default; explicit overrides the default). `TestSend_ExplicitTo_LooksUpRoleByName` asserts a coordinator sender with `To: "w2"` routes to w2. **Aligned**. `TestSend_ExplicitTo_UnknownRole` covers the `ErrNotFound` case with substring `"does not exist"`. Aligned.

2. **[COVERED]** (line 125-127) `case KindCoordinator: return error explaining explicit-recipient requirement`. Spec § "Coordinator senders must supply an explicit recipient" scenario: MUST return `isError: true` with content explaining that coordinator messages require an explicit recipient. Code wording: `"coordinator senders must supply an explicit `to`"`. `TestSend_CoordinatorWithoutTo_Rejected` asserts substring `"explicit"`. **Aligned**. Note: spec also says "AND no message row MUST be persisted". **Test-alignment gap M**: test does not verify zero rows in the messages table after rejection. Code path is correct (error returns BEFORE `Messages.Create`), so the promise holds, but it's not asserted.

3. **[COVERED]** (line 128-133) `case KindWorker, KindFreelance: findCoordinator`. Spec § "Default message routing for worker and freelance senders" scenario "Worker without `to` routes to coordinator": message row's `to_role_id` MUST be the coordinator's id. `TestSend_Worker_DefaultRoutes_ToCoordinator` asserts `out.RecipientRole == "coord"` and the inject taskID is `"t-coord"`. **Aligned**. **Test-alignment gap N**: no explicit test for the freelance-sender default-routes case (only worker). The branch is the same code path, but spec covers both kinds in the same requirement.

4. **[COVERED]** (line 135) `return nil, fmt.Errorf("unknown sender kind %q", sender.Kind)` — defensive fallthrough for an unknown role kind. Not specced; safe.

### handler_send.go::findCoordinator (5 branches at lines 142, 143, 146, 147, 150)

1. **[COVERED]** (line 142-143) `Roles.ListByOrchestrator` failure.
2. **[COVERED]** (line 146-147) returns coordinator if found.
3. **[COVERED]** (line 150) `"orchestrator has no coordinator role"` — defensive error when default-routing finds no coord. **Test-alignment gap O (minor)**: no test for the "default-route from worker, no coordinator exists" path. Spec § "Default message routing": "The coordinator role MUST exist for the send to succeed" — the code says exactly that. Aligned but untested.

### handler_inbox.go::Handle

1. **[COVERED]** (line 48-49) JSON parse guard.
2. **[COVERED]** (line 51-52) `if in.Cwd == ""` → `"hera_inbox: cwd is required"`. Aligned with spec scenario "Tool call with no `cwd` rejected". `TestInbox_EmptyAndPopulated` exercises a real cwd.
3. **[COVERED]** (line 56-58) `CallerRole` error → `errors.Is(err, ErrNoBinding)` → `"hera_inbox: " + err.Error()`. Note: both the `ErrNoBinding` branch (line 57-58) and the generic branch (line 60) flatten to the same wording — `"hera_inbox: " + err.Error()`. The `if` is **dead code** (both branches do the same thing). Defensive; not a contradiction. Code smell — the explicit `if errors.Is(err, ErrNoBinding)` suggests intent to differentiate wording but doesn't. Likely an in-progress branch that got equalized.
4. **[COVERED]** (line 64-65) `Messages.UnreadForRole` query error.
5. **[COVERED]** (line 71) `if r, err := Roles.GetByID(m.FromRoleID); err == nil` — resolve sender role name. Spec § "Messages auto-submitted when recipient is idle" implies the inbox query surface should give the recipient enough info to act; the spec doesn't say "FromRole as role name" directly, but the response shape `from_role` is a clear contract. `TestInbox_EmptyAndPopulated` asserts `out.Messages[0].FromRole == "c"` (the coordinator's role name). **Aligned**.
6. **[COVERED]** (line 82) `if out.Messages == nil { out.Messages = []InboxMessage{} }` — empty-slice normalization for JSON. Good — empty array vs null is a real wire-format difference. `TestInbox_EmptyAndPopulated` asserts `emptyOut.Count == 0` but does NOT assert the array shape (null vs []). Defensive code; aligned with the test.
7. **[COVERED]** (line 88) success return.

The `_ = db.ErrNotFound` on line 87 is a lint-silencer for an unused import; cosmetic.

### handler_mark_read.go::Handle (8 branches)

1. **[COVERED]** (line 39-40) JSON parse guard.
2. **[COVERED]** (line 42-43) `if in.Cwd == ""`. Aligned with spec § "Tool call with no `cwd` rejected".
3. **[COVERED]** (line 45-46) `if len(in.MessageIDs) == 0` → `"message_ids must contain at least one id"`. `TestMarkRead_RequiresMessageIDs` asserts `resp.IsError` (does not assert wording). **Test-alignment gap P (minor)**.
4. **[COVERED]** (line 50-54) `CallerRole` error → both `ErrNoBinding` and generic flatten to `"hera_mark_read: " + err.Error()`. Same dead-code pattern as inbox.
5. **[COVERED]** (line 57-60) `Messages.MarkRead(role.ID, ids)` — DB-level own-messages-only enforcement. The DAO is the right place to enforce "only mark MY messages read" (it filters by `to_role_id = role.ID`). `TestMarkRead_OnlyOwnMessagesAffected` is the **CENTRAL test for this behavior**: w2 tries to mark w1's message; `MarkedReadCount == 0`; w1 then marks it; `MarkedReadCount == 1`. **Aligned**. The spec doesn't have an explicit "own messages only" scenario but the requirement is implicit in the inbox/messaging model.
6. **[COVERED]** (line 63) success return.

### handler_status.go::Handle (12+ branches at lines 45, 46, 48, 49, 51, 52, 55-59, 63-71, 77, 82, 86)

1. **[COVERED]** (line 45-46) JSON parse guard.
2. **[COVERED]** (line 48-49) `if in.Cwd == ""`. Aligned with spec § "Tool call with no `cwd` rejected".
3. **[COVERED]** (line 51-52) `if in.Status == ""` → `"hera_status: status is required"`. Defensive; not directly tested.
4. **[COVERED]** (line 55-60) status-enum validation. Allowed: idle/working/blocked/done. `TestStatus_InvalidStatus` exercises `"wibble"` and asserts substring `"invalid status"`. **Aligned** with the enum surface in `internal/daemon/run.go:220`.
5. **[COVERED]** (line 62-67) `CallerRole` error — same dead-code branch pattern as inbox/mark_read.
6. **[COVERED]** (line 70-71) `RoleStatus.Upsert(role.ID, s)` — DB write. `TestStatus_HappyPath` asserts `rs.Status == StatusBlocked` after the call. Aligned with spec § "Role metadata mirrored to argus task_meta" scenario "Thread status meta updated on `hera_status`": MUST update `role_status` for the role (covered here).
7. **[COVERED]** (line 77-79) `client.PutTaskMeta(bnd.ArgusTaskID, MetaKeyThreadStatus, in.Status)` — mirror to argus task_meta. Spec § "Role metadata mirrored to argus task_meta" scenario "Thread status meta updated on `hera_status`": MUST PUT `{key: "thread_status", value: "blocked"}` to `/api/tasks/{id}/meta` endpoint. `TestStatus_HappyPath` records the fake's metaPuts and asserts `taskID="t-w" && key="thread_status" && value="blocked"`. **Aligned**.
8. **[COVERED]** (line 76-79) **NOTE**: the meta-mirror failure is swallowed (`mirrored = false` but no error returned). The status call still succeeds with `meta_mirrored: false` in the JSON response. This is **best-effort behavior, intentional per the code comment** ("Best-effort; report success on the response but don't fail the call if the write errors out."). Spec § "Role metadata mirrored to argus task_meta" says "MUST update `role_status` for `f2-impl` AND PUT ..." — the MUST suggests both should succeed. **Possible behavioral gap**: under spec's literal reading, a meta PUT failure should bubble up as an error. The code returns success with `meta_mirrored=false`. Defensible (transient argus outage shouldn't break status calls), but **not strictly spec-compliant**. **Test-alignment gap Q**: no test exercises the meta-mirror-fails path to confirm the chosen behavior.
9. **[COVERED]** (line 82) `RoleStatus.Get(role.ID)` for updated-at timestamp — best-effort fetch for response payload.
10. **[COVERED]** (line 86-91) success return with `RoleName`, `Status`, `UpdatedAt`, `MetaMirrored`.

## Contradictions

None at this level. The handlers' error wording matches the spec's "explanatory" requirement; the routing rules (worker/freelance default → coord, coord MUST be explicit) match the spec exactly; the auth, envelope, and status-code behaviors match `TestServer_*` and the spec's implied contract.

## Unimplemented Spec Promises

1. **`hera_join` freelance/coordinator-bootstrap path does NOT mirror `meta:hera.role` to argus task_meta.**

   Spec § "Role metadata mirrored to argus task_meta" scenario "Role meta written on binding": "**WHEN** a new binding is created for a worker role on argus task `T2` **THEN** hera MUST PUT `{key: "role", value: "worker"}` to `/api/tasks/T2/meta` using its scope token".

   Code paths that create bindings:
   - `internal/events/adopt.go:128` — auto-adopt path. Calls `PutTaskMeta(child.ID, MetaKeyRole, "worker")`. **Aligned**.
   - `internal/mcp/handler_join.go:171` — freelance/coordinator-bootstrap. **Does NOT call PutTaskMeta**. Inspection of the file confirms no `PutTaskMeta` invocation in this handler. The `JoinHandler` struct does not even hold an `*argus.Client` reference — only `*Resolver` and `*db.DB` — so the call cannot happen here without a constructor change.

   This is **medium severity**. The spec scenario uses "worker role" in its phrasing, which could be read narrowly as "only when auto-adopted". But the requirement statement above is unrestricted: "whenever a binding is created". The freelance and explicit-coordinator paths both create bindings and should mirror the meta. Currently a coordinator that bootstraps via `hera_join(kind="coordinator")` will have its bound argus task miss the `meta:hera.role=coordinator` mirror, breaking any downstream observer that queries argus task_meta to discover the role's kind.

   **Recommended fix**: pass `*argus.Client` into `NewJoinHandler`, and after each successful `Bindings.Create` in `freelance()`, call `client.PutTaskMeta(ctx, argusTaskID, MetaKeyRole, string(kind))`. Best-effort failure handling (like `StatusHandler`) is appropriate.

## Test-alignment gaps

These were called out inline above; consolidated here for the audit-writer:

- **A** (`registrar.go:102`): No test covers the heartbeat-error-then-recover path. Test-only gap.
- **B** (`registrar.go:registerAll`): `TestRegistrar_*` does not inspect that `InputSchema` is forwarded into the POST body — `fakeRegistry.handler` decodes only `Name` and `AuthHeader` from `argus.MCPTool`. Spec § "Tool inputs and outputs documented" requires the schema is sent; integration coverage via `internal/daemon/run_test.go` checks the five tools register, but neither test asserts the schema bytes per-tool. **Recommended**: have `fakeRegistry.handler` capture `body.InputSchema` and `body.Description`; assert `description ≥ 10 chars` and `properties != nil`.
- **C** (`resolve.go:39` / `handler_join.go:66`): `TestJoin_UnknownCwd` asserts only `resp.IsError`. Spec § "Tool call with unknown `cwd` rejected" requires the explanatory wording (`"does not map to a tracked argus task"`). **Recommended**: add `strings.Contains(resp.Content[0].Text, "does not map")` assertion.
- **D** (`handler_join.go:62`): No test asserts the "cwd is required" wording for `hera_join`. Similar gap exists for `hera_send` (body required, but not wording-asserted — gap K), `hera_inbox`, `hera_status`, `hera_mark_read`.
- **E** (`handler_join.go:80-84`): `TestJoin_BareReincarnation_NoBinding` asserts substring `"not bound"`. Spec wants suggestion that the caller invoke join with explicit args. **Recommended**: assert the response also contains `"freelance"` or `"orchestrator, role_name, kind"` to match the spec's "suggesting" promise.
- **F** (`handler_join.go:102-106`): `TestJoin_BareReincarnation_HappyPath` does not pre-set a `RoleStatus`, so the `Status` field in `JoinOutput` is empty. **Recommended**: add a variant that pre-sets `RoleStatus.Upsert(role.ID, db.StatusBlocked)` and asserts `out.Status == "blocked"`. Also asserts the spec's "without modifying any database rows" promise: query message/role/binding rowcounts before and after.
- **G** (`handler_join.go:125-129`): No test for `kind="frog"` invalid-kind path. **Recommended**: small extra test asserting `"invalid kind"` substring.
- **H** (`handler_join.go:144-148`): `TestJoin_FreelanceAttach_OrchestratorMissing` does not verify "no role or binding row MUST be created". **Recommended**: after the failed call, assert `Roles.GetByOrchestratorAndName` returns `ErrNotFound` and `Bindings.GetLiveByTaskID` returns `ErrNotFound`.
- **I** (`handler_join.go:152-157`): Same pattern as H for the conflicting-kind case — assert "no row MUST be modified" (the original worker role should still be worker, no binding flipped).
- **J** (`handler_join.go:182-188`): No test for `hera_join` with invalid `status` value.
- **K** (`handler_send.go:59`): `TestSend_BodyRequired` does not assert the "body is required" wording.
- **L** (`handler_send.go:99-101`) — **most significant**: `TestSend_*` does NOT assert that `Messages.SetDelivered(msg.ID, mode)` actually persists. Tests assert the response JSON's `delivery_mode` field but not the DB row's `delivery_mode` column. Spec § "Messages auto-submitted when recipient is idle" specifically requires recording `delivery_mode = "idle_submit"` on the message row. **Recommended**: after each send test, call `database.Messages.Get(msg.ID)` and assert `msg.DeliveryMode == expected`.
- **M** (`handler_send.go:125-127`): `TestSend_CoordinatorWithoutTo_Rejected` does not verify "no message row MUST be persisted". **Recommended**: assert that `database.Messages.UnreadForRole(coord.ID)` is empty and that no row was created. (Also test the symmetric case for any other recipient.)
- **N** (`handler_send.go:128-133`): No test exercises the freelance-sender default-routes-to-coord case. Worker is covered; freelance is the same code path but spec covers both kinds. **Recommended**: add `TestSend_Freelance_DefaultRoutes_ToCoordinator`.
- **O** (`handler_send.go:findCoordinator:150`): No test for the "default route, but orchestrator has no coordinator" case.
- **P** (`handler_mark_read.go:45-46`): `TestMarkRead_RequiresMessageIDs` does not assert the "message_ids must contain at least one id" wording.
- **Q** (`handler_status.go:77-79`): No test covers the meta-mirror-fails path. Behaviorally important because the code chose "best-effort + meta_mirrored=false" over "return error", which is a subtle spec deviation worth pinning with a test.

## Cross-module symbol awareness

All cross-module symbols resolve in symbols.json:

- `argus.Client`, `argus.Task`, `argus.MCPTool`, `argus.PutTaskMetaInput`, `argus.New` — `internal/argus`.
- `db.DB`, `db.Role`, `db.Binding`, `db.RoleKind`, `db.RoleStatusValue`, `db.DeliveryMode`, `db.CreateRoleInput`, `db.CreateBindingInput`, `db.CreateMessageInput`, `db.ErrNotFound`, `db.DeliveryQueuedNoBinding`, `db.DeliveryIdleSubmit`, `db.DeliveryBusyBuffer`, `db.DeliveryPending`, `db.StatusIdle`, `db.StatusWorking`, `db.StatusBlocked`, `db.StatusDone`, `db.KindCoordinator`, `db.KindWorker`, `db.KindFreelance`, `db.Roles.GetByID`, `db.Roles.Create`, `db.Roles.GetByOrchestratorAndName`, `db.Roles.ListByOrchestrator`, `db.Bindings.GetLiveByTaskID`, `db.Bindings.GetLiveByRole`, `db.Bindings.Create`, `db.Messages.Create`, `db.Messages.UnreadForRole`, `db.Messages.MarkRead`, `db.Messages.CountUnreadForRole`, `db.Messages.SetDelivered`, `db.Orchestrators.Create`, `db.Orchestrators.GetByID`, `db.Orchestrators.GetByName`, `db.RoleStatus.Get`, `db.RoleStatus.Upsert` — `internal/db`.
- `events.MetaKeyThreadStatus` — `internal/events`.

No `missing-symbol` findings.

## Notes

- The dead-code `if errors.Is(err, ErrNoBinding)` blocks in `handler_inbox.go:57`, `handler_mark_read.go:51`, `handler_status.go:64` produce identical wording in both branches. This isn't a contradiction or behavioral bug, but it suggests the original intent was to give a more helpful "you have no binding; try `hera_join`" message when the caller has no role. Worth a small cleanup PR to either equalize-and-simplify or actually differentiate.
- The MCP server's listener address fallback (`s.addr = ln.Addr().String()` on line 67) supports `:0` ports for tests. Good engineering.
- The `_ = db.ErrNotFound` lines in `handler_inbox.go:87` and `handler_mark_read.go:62` are imports-no-references silencers — defensive against lint regressions but a small code smell. Once the dead-code if-branches are cleaned, these can go too.
- The `cwd` exact-string match in `resolve.go:35` is fragile (no `filepath.Clean`, no symlink resolution). Worth thinking about whether the spec's "matches any known argus task's worktree" allows for normalization. Out of scope for this audit but worth raising with the spec author.
