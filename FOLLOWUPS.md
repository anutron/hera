# Hera v1 follow-ups

Items the spec-audit and ralph-review passes surfaced and we consciously chose to defer (vs silently parked). Each entry includes the rationale for deferring and a one-line signal for when to escalate it back to active work.

## Dogfood loop reliability (surfaced during hera-settings)

### `argus.PostTaskInput` response decode error

**Status:** Active bug. Bit w1, w3, and coord during the hera-settings dogfood loop.

**What:** `hera_send` to a worker via the coordinator returns the error `decode: json: cannot unmarshal string into Go struct field postTaskInputResponse.bytes of type int`. The POST itself succeeds — the bytes reach the recipient's PTY — but the response unmarshal fails on a schema mismatch between hera's `postTaskInputResponse` struct (defines `bytes int`) and argus's actual response (returns a stringified int). The error propagates back to the tool caller as a hera_send failure, so callers think the send failed even though it landed.

**Why escalate now:** Combined with the "auto-execute doesn't seem to be working" report from one of the workers, this points at a real dogfood-blocker — the auto-execute inject side of hera_send is exactly the path the decode error sits on. Fix in `internal/argus/input.go` (field type, likely `json.Number` or `string` with parse) plus a regression test.

**Escalate when:** Already — slated as the next change after `hera-settings` archive.

### Possible auto-execute regression / idle-gate misclassification

**Status:** Reported but unconfirmed. Flagged by a worker during hera-settings.

**What:** A worker reported "auto-execute doesn't seem to be working" — the trailing-`\n` submit on idle did not auto-submit their buffered hera_send message. Observed direction unclear; coordinator-side auto-execute was confirmed working (every worker→coord message arrived as a new turn). Two suspects: (1) the response-decode bug above masking a partial-fail in the inject path, (2) the idle gate misreading the worker session as still-busy when it had quieted. Needs reproduction.

**Why deferred from hera-settings:** Out of scope; settings ships the *knob* for the debounce, not a behavior change.

**Escalate when:** Same change as the decode-error fix — both live in the inject path.

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

- **`Roles.Create` write-once semantics** – documented in `internal/db/roles.go` doc-comment. Mission/constraints/argus_project on a re-Create are silently dropped; this is intentional (role identity is established at first creation, subsequent agents inherit). Spec design D1 says role identity outlives task lifecycle; this is part of that.
- **Status meta-mirror best-effort** – spec amendment landed in the previous commit; `hera_status` returning success with `meta_mirrored: false` on argus failure is now explicit, not ambiguous.
- **`StreamEvents` resync requirement** – doc-comment now tells callers they MUST handle resync events; previously this was implicit.

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
