# Hera v1 follow-ups

Items the spec-audit and ralph-review passes surfaced and we consciously chose to defer (vs silently parked). Each entry includes the rationale for deferring and a one-line signal for when to escalate it back to active work.

## Dogfood loop reliability (surfaced during hera-settings)

### `argus.PostTaskInput` response decode error — RESOLVED

**Status:** Fixed in commit `fb04c27`. Introduced `flexInt` type in `internal/argus/tasks.go` with a tolerant `UnmarshalJSON`. Regression test: `TestClient_PostTaskInput_BytesAsString` in `internal/argus/client_test.go`.

### Argus settings-callback proxy drops registered auth_header

**Status:** Substrate-side bug. Discovered during hera-settings live-install.

**What:** When argus's TUI submits a settings form, its callback proxy POSTs the values to the section's registered `callback_url` with NO `Authorization` header — even though the registration payload carries an `auth_header` field. Hera's MCP callback listener requires `Authorization: <auth_header>` on `/mcp/*` requests, so every settings-save form submit from the argus UI will 401 against hera's `/mcp/settings_save` handler. Registration round-trip works (the daemon boots cleanly and the section renders in the UI), but saving values does not.

**Why escalate now:** Hera-settings is functionally broken end-to-end until either (a) argus forwards `auth_header`, or (b) hera special-cases the `settings_save` route to skip auth. (a) is the cleaner contract — every plugin will hit this. Filed against argus by drn / via the substrate coordinator agent task.

**Workaround until fixed:** Operators can `INSERT` rows into `~/.hera/state.sqlite` `config` table directly, restart hera, and the persisted values still flow through `LoadPersistedSettings` on the next boot. Painful but unblocked.

### Possible auto-execute regression / idle-gate misclassification — RESOLVED

**Status:** Closed. Smoke-confirmed working post-decode-fix.

**Resolution:** The original "auto-execute doesn't seem to be working" report was almost certainly the response-decode bug masquerading. After that bug was fixed (commit fb04c27 — `flexInt` in `internal/argus/tasks.go`, regression test `TestClient_PostTaskInput_BytesAsString` in `internal/argus/client_test.go`), the `hera-smoke-decode-fix` worker re-ran a cross-agent `hera_send` and confirmed the coordinator's PTY received it as a new turn with `delivery_mode: idle_submit`. The inject path is behaving correctly; the idle gate is not misclassifying. Existing coverage at `internal/inject/inject_test.go` (`TestInject_IdleSubmits`, `TestInject_DefaultAutoInjectEnabledIsTrue`) and `internal/mcp/handler_send_test.go` (`TestSend_Worker_DefaultRoutes_ToCoordinator`) asserts the idle → `idle_submit` path end-to-end through the handler.

**Audit trail:** Originally flagged by a worker during hera-settings as "trailing-`\n` submit on idle did not auto-submit". The decode bug surfaced an error to the tool caller even though the POST succeeded, which made the send *look* failed; that is the most plausible cause of the original report. No second-source reproduction has materialized. If a fresh reproduction does appear, suspect: (1) the idle tracker reading a still-busy session as quieted (window-edge race), (2) a regression in `FormatBody` newline semantics.

## Hera bugs (found in the 1.0 regression)

### BUG-011 – doorbell loops until mark_read — RESOLVED

**Status:** Fixed in commit `1fa3628`. `hera_inbox` now stamps `read_at` on every message it returns, so a recipient who reads without explicitly calling `hera_mark_read` is still treated as read and the doorbell stops.

### BUG-012 – rail doesn't promote a new worker binding out of Freelance live

**Status:** Active bug. Found during 1.0 regression.

**What:** A task that calls `hera_join` as a worker after it was created (the self-join pattern) gets a live binding in the DB, but the rail keeps rendering it in the Freelance section (observed >1 minute, well past the ~2 s argus-state cache window) until a full reload. The new-binding broadcast → rail reclassification path isn't moving it under its coord live.

**Why noted:** The `hera_spawn_worker` verb (see NEXT.md backlog) would sidestep this for coord-spawned workers since they'd be born bound. The self-join pattern still needs the live reclassification fix for tasks that genuinely freelance-join after creation.

**Escalate when:** We add `hera_spawn_worker` (which reduces the blast radius to genuinely freelance-joining tasks only).

## Substrate hand-offs (not hera – for the argus / iris owners)

### BUG-009 (argus rerender-kick)

**Owner:** drn / argus.

**What:** Entering or resizing a task's pane (a PTY width-cross, e.g. 80×24 → 138×75) makes argus stop the session and restart it with `--resume` (logged as "runner: restarting after kick" in `~/.argus/daemon.log`). The `--resume` replays the transcript, so the PTY ends up containing the session twice. Surfaces in hera as a "duplicated agent" pane, but hera is faithfully rendering the two-session PTY – the defect is argus-side.

**Fix direction:** Gate the rerender kick on `needs-input` only; don't kick a session that doesn't need re-prompting.

### iris gaps

**Owner:** iris.

- **Stale-tip tag.** `iris_tag` tags the local `origin/main` without fetching first, so it can tag a stale pre-merge commit. This caused a dud `argus-sdk v0.0.5` tag (pointed at the pre-merge commit, missing the new constants). Fix: `iris_fetch` before tagging.

- **No tag-delete or force-tag verb.** A bad tag can't be corrected via iris – we had to supersede it with `v0.0.6`. A force-tag or tag-delete verb would cover this.

## Architecture (deferred to v1.1+)

### Atomic role+binding insert across DAOs

**Status:** Deferred.

**What:** `Roles.Create` and `Bindings.Create` are two separate `sqldb.Exec` calls in the auto-adopt path (`internal/events/adopt.go`), in `hera_new_orchestrator`, and in `hera_join` attach. If `Bindings.Create` fails after `Roles.Create` succeeds, the orphan role row persists (with no live binding to it).

**Why deferred:** The fix requires a `db.WithTx` helper that lets each DAO accept either `*sql.DB` or `*sql.Tx` as its executor (or per-use-case bundled methods like `db.AdoptWorker`). Either is a real refactor across 4-5 files. The practical risk is very low in a single-user, single-tenant SQLite system: `Bindings.Create` only fails on schema constraint violations (which migration 0002's new unique indexes now catch *before* the role would be created – the orphan condition is essentially impossible for the common race) or catastrophic SQLite errors that would already have broken everything else.

**Escalate when:** We see an orphaned-role bug in practice, OR we move to a multi-process / multi-host topology where concurrent calls are realistic.

### Cursor advancement on handler failure

**Status:** Deferred.

**What:** `events.Subscriber.Run` advances `EventCursor` after every event delivered to handlers, regardless of handler outcome. The `Handler` interface returns nothing – handlers signal failure only via log lines. A transient adoption error (binding insert error, argus `GetTask` 503) silently consumes the event; the missed work is not retried.

**Why deferred:** Fixing this means changing the `Handler` interface to return an error AND deciding the cursor-advancement policy (hold-on-any-failure? per-handler? best-effort with a retry queue?). It's a real operational concern but not a behavioral correctness concern — the spec scenarios are still satisfied for the happy path. The failure mode (a worker is missed) is visible in the log and recoverable by an operator restarting hera with the cursor reset.

**Escalate when:** We see hera in production missing adoptions because of transient argus errors, OR we want hera to be more autonomous about recovery.

## Tests (deferred — incremental tightening)

### ~25 low-priority test-alignment polish items

**Status:** Deferred.

**What:** The spec-audit per-module agents flagged that several tests exercise the spec-described path but don't assert the spec's exact wording (e.g., a test exercises "rejects invalid status" but asserts on the error response rather than quoting the spec's "must include the enum members" wording). 17 in `internal/mcp`, 4 in `internal/db`, 3 in `internal/argus`, 2 in `internal/wiring`.

**Why deferred:** These are individually low-value (the behavior is verified; the wording isn't). Tackling all 25 in a single pass would be ~2 hours of grinding through test files for marginal improvement. We've fixed the high-severity test-alignment gaps (delivery_mode persistence, ResyncHandler coverage, skipped-adoption log capture, soft-fail meta-mirror, etc.) – the remainder is incremental tightening.

**Escalate when:** A new behavior lands that touches one of these tests; tighten the assertion as part of that change.

### `cmd/hera` CLI verb branches (6 uncovered behaviors)

**Status:** Intentional non-coverage.

**What:** The spec-audit flagged 6 uncovered-behavioral branches in CLI verb files (`start.go`, `stop.go`, `status.go`, `list.go`): output formatting, exit-code paths, `--foreground`-required enforcement, PID-file lifecycle.

**Why deferred:** The base spec is about hera's coordination *behavior*, not CLI ergonomics. The CLI verbs are operational plumbing — the spec doesn't (and shouldn't) describe them. Treat as intentional non-coverage.

**Escalate when:** We add a CLI behavior that the spec should describe (e.g., a verb that mutates orchestrator state observably).

## Documentation (in-line)

The following items were resolved by documenting the existing behavior rather than changing the code:

- **`Roles.Create` write-once semantics** – documented in `internal/db/roles.go` doc-comment. Prompt/argus_project on a re-Create are silently dropped; this is intentional (role identity is established at first creation, subsequent agents inherit). Spec design D1 says role identity outlives task lifecycle; this is part of that.
- **Status meta-mirror best-effort** – spec amendment landed in the previous commit; `hera_status` returning success with `meta_mirrored: false` on argus failure is now explicit, not ambiguous.
- **`StreamEvents` resync requirement** – doc-comment now tells callers they MUST handle resync events; previously this was implicit.

## 1.0 closeout cleanup

Remaining housekeeping from the 1.0 release cycle.

- **`openspec archive` the landed change folders.** All change folders for shipped work should be archived into `openspec/changes/archive/` so `openspec/changes/` reflects only genuinely in-flight work. Many 1.0-era folders (add-hera-view, rail-search, pane-fullscreen, rail-sections, etc.) are still in `openspec/changes/` unarchived.

- **Finish `add-multi-binding` archiving.** Code is merged on main. Three tasks remain: `iris_publish(reset=true, push=true)`, verify reload via `iris_status`, and `hera_status done` + `task_set_result`. Then `openspec archive add-multi-binding`.

## v1.x backlog (new deferred items)

These items were explicitly considered for v1.0 and deferred. See also NEXT.md for the prioritized list.

### Re-attach detached agents (top v1.x item)

**Status:** Deferred. The session-vs-task identity gap means a restart leaves dead pane sessions with no clean re-attach path. Design tracked in `memory/session-vs-task-identity-gap.md`.

**Escalate when:** A worker's pane goes dead mid-session and the operator needs to recover without restarting the whole daemon.

### `hera_spawn_worker` MCP verb

**Status:** Deferred. The operator-side spawn (`w` key → `ops.SpawnWorker`) is implemented; the MCP equivalent is not. The verb would call `ops.SpawnWorker` directly, creating a born-bound worker without the transient-freelancer window.

**Escalate when:** Coordinators start spawning workers programmatically via MCP (the current task_create + hera_join dance is error-prone).

### BUG-012 – rail doesn't promote self-joined worker out of Freelance live

**Status:** Active. Found during 1.0 regression. The new-binding broadcast → rail reclassification path doesn't move a self-joined worker out of the Freelance section until a full reload. Adding `hera_spawn_worker` sidesteps this for coord-spawned workers; the self-join path still needs the live-reclassification fix.

**Escalate when:** We add `hera_spawn_worker` (reduces blast radius to genuinely freelance-joining tasks only), or the UX regressions become frequent.

### Status-step latency

**Status:** Known UX gap. `s`/`S` incur ~0.5 s round-trip to argus; no optimistic update.

**Escalate when:** The latency becomes disruptive in day-to-day use.

### Remove the outer plugin-view border and "Hera" title

**Status:** Deferred. `viewTitle = "Hera"` in `internal/daemon/run.go` causes argus to draw a title bar around the plugin view. Empty title = HTTP 400 + crash-loop (BUG-034 regression risk). Requires an argus-side fix first.

**Escalate when:** argus is updated to allow an empty plugin-view title.

### Coord-metadata auto-summary

**Status:** Deferred. The Details pane live-refreshes agent count, activity, and status. The description/goal/scope auto-summary is a placeholder.

**Escalate when:** We have a clear algorithm for inferring goal/scope from the role's message history and prompt.

### Coord archive/mark-done/undo MCP verbs

**Status:** Deferred. Coordinators currently have to drop to argus `task_archive` to close out agents. Coord-side verbs (`hera_archive_role`, `hera_mark_done`, undo) would make lifecycle management possible from the coordination layer.

**Escalate when:** Orchestrators routinely need to close out completed workers from within a coordinator script.

### Hera-aware skills

**Status:** Deferred. Workers spawned via `/fixit` etc. need hand-supplied `hera_join` instructions. Three paths: (a) project CLAUDE.md snippet, (b) modify individual skills, (c) hera-prefixed wrapper skills. Approach (a) is lowest-touch.

**Escalate when:** The repeated "remember to hera_join" instruction becomes a noticeable overhead.

### Reliable-messaging redesign (email model)

**Status:** Deferred. The minimal doorbell (re-nudge on unread `read_at`) shipped in 1.0. The full design stops injecting message bodies into the PTY and instead injects only a doorbell that triggers `hera_inbox`. Cleaner, idempotent, re-nudge-safe.

**Escalate when:** Body-injection causes double-delivery or ordering issues under concurrent sends.

### Install: Linux/systemd parity + programmatic install/uninstall

**Status:** Deferred. setup.sh is macOS-only. Linux/systemd path and `hera install` / `hera uninstall` CLI subcommands are the natural extensions.

**Escalate when:** A Linux user reports the daemon lifecycle is unmanageable without a managed service.

## Resolved this pass

For context: items the spec-audit and ralph-review flagged that have now been fixed and have asserting tests where applicable:

- Partial unique indexes on bindings(argus_task_id|role_id|worktree_path) WHERE ended_at IS NULL (migration 0002)
- `TaskForCwd` filepath.Clean normalization (matches trailing-slash variants)
- Idle tracker drops entries on `task.archived` (no unbounded map growth)
- SSE `since=` query param always emitted (uniform request shape)
- Constant-time auth pads to fixed length (no timing distinction between wrong-length and wrong-content)
- `hera_new_orchestrator` `created` flag uses the real existence signal
- Removed unused `Config.CallbackBaseURL` field
- `internal/log/doc.go` matches the v1 reality (stderr-only)
- New tests: role survival across binding end, role rebind across incarnations, argus_project write-once, partial-unique-index defense, idle tracker cleanup on archive, since= cursor value, since=0 emission, soft-fail meta-mirror for both `hera_new_orchestrator` and `hera_status`, `TaskForCwd` normalization
- Ralph-review's earlier batch: StreamEvents reconnect-on-EOF, hera_join already-bound guard, Registrar heartbeat-vs-Stop race, daemon Stop ctx bound, hera_send tool description, five-tools doc drift, dead code cleanup
