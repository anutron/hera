# internal-db module audit

**Scope:** `internal/db/` — doc.go, db.go, schema.go, types.go, orchestrators.go, roles.go, bindings.go, messages.go, role_status.go, event_cursor.go, config.go.

**Authority references (not subjects):** `internal/db/db_test.go`.

## Branch totals

| File | Branches |
|------|---------:|
| db.go | 14 |
| schema.go | 13 |
| types.go | 0 |
| orchestrators.go | 22 |
| roles.go | 26 |
| bindings.go | 30 |
| messages.go | 39 |
| role_status.go | 8 |
| event_cursor.go | 8 |
| config.go | 8 |
| doc.go | 0 |
| **Total** | **168** |

## Spec→code matrix (data-model requirements)

The data layer is most directly responsible for the schema shape, monotonic cursor, idempotent role/binding identity, and message-row semantics. Each requirement below is checked against the implementation.

### Requirement: Role identity outlives argus task lifecycle

- **Status:** Implemented.
- **Evidence:**
  - `schema.go:25-37` — `roles` is a separate table from `bindings`; ON DELETE CASCADE only fires from `orchestrators` -> `roles`, never from "task gone" to role.
  - `schema.go:39-47` — `bindings.ended_at` and `bindings.end_reason` are nullable, so "live" vs "ended" is a row-level marker.
  - `bindings.go:48-63` — `BindingsDAO.End` sets `ended_at` and `end_reason` without touching the role row or any messages.
  - `schema.go:53-63` — `messages.to_role_id` references `roles(id)`, not `bindings(id)`; archiving a binding leaves messages addressed to the role intact.
  - `schema.go:68-72` — `role_status.role_id` PK references `roles(id)` (not bindings).

### Requirement: Auto-adopt copies mission and constraints from task meta

- **Status:** Schema-supported. Adoption itself lives in `internal/daemon`/`internal/events`; the DB layer only needs to accept the values and default to empty strings.
- **Evidence:**
  - `schema.go:31-32` — `mission TEXT NOT NULL DEFAULT ''` and `constraints TEXT NOT NULL DEFAULT ''`. Both NOT NULL with empty-string default, exactly matching the spec wording "absence MUST result in empty-string columns" (spec.md:97).
  - `roles.go:14-22` — `CreateRoleInput` has both fields as `string` (zero value is `""`).
  - `roles.go:39-44` — `INSERT` passes both verbatim.

### Requirement: Roles live in argus projects; orchestrators do not

- **Status:** Implemented.
- **Evidence:**
  - `schema.go:19-23` — `orchestrators` columns are `(id, name, created_at)`. No `argus_project`.
  - `schema.go:25-35` — `roles.argus_project TEXT NOT NULL`.
  - `roles.go:48-49` — `Role` struct exposes `ArgusProject`.
  - There is no DAO method that mutates `argus_project` after creation, so "preserved across incarnations" is satisfied by absence-of-mutation. (The "first binding" provenance lives in the auto-adopt path in `internal/daemon`; the DB layer's promise is just "stable column".)

### Requirement: Event stream cursor persisted and replayed

- **Status:** Implemented.
- **Evidence:**
  - `schema.go:74-78` — `event_cursor` is a singleton (CHECK id = 1), and an initial row `(1, 0)` is seeded by the migration.
  - `event_cursor.go:13-25` — `Get` returns 0 on `sql.ErrNoRows` (defensive; the seed row makes this branch unreachable but harmless).
  - `event_cursor.go:30-40` — `Set` is monotonic via `WHERE id = 1 AND last_seen_event_id < ?`. A smaller-than-current call is a no-op (no row updated, no error).
  - Cross-module: `internal/events/subscriber.go:69,80` consumes both methods.

### Requirement: Default message routing & coordinator-explicit recipient (data layer)

- **Status:** Schema and constraint enforced at the DB layer; routing decision lives in `internal/mcp/handler_send`.
- **Evidence:**
  - `schema.go:53-58` — `to_role_id INTEGER NOT NULL REFERENCES roles(id) ON DELETE CASCADE`. The NOT NULL is the schema-level guard that the "user pseudo-recipient" removal documented in `OVERNIGHT_LOG.md:12` was actually applied.
  - `messages.go:24-29` — `MessagesDAO.Create` returns an error if either `ToRoleID == 0` or `FromRoleID == 0`, providing a second guard above the schema.
  - `types.go:29-33` — `DeliveryMode` constants are `pending`, `idle_submit`, `busy_buffer`, `queued_no_binding`. No `DeliveryUserInbox`.

### Requirement: NOT NULL `to_role_id` / no `to_kind` / no `DeliveryUserInbox` (decision delta)

- **Status:** Applied correctly.
- **Evidence:**
  - `schema.go:53-58` — single column `to_role_id INTEGER NOT NULL`. No `to_kind`.
  - `types.go:24-33` — `DeliveryMode` constants do not include `DeliveryUserInbox`.
  - Repo-wide grep for `to_kind` / `DeliveryUserInbox` returns only retrospective references in `OVERNIGHT_LOG.md` and an archived change's `design.md` (both noting the removal).
  - Cross-check: `internal/mcp/handler_send.go:88` uses `db.DeliveryQueuedNoBinding`, which IS still defined in `types.go:32` — present and consistent.

## Branch classification (summary)

Every branch in this module is either schema-defensive (sql errors, NULL handling, monotonic guards) or struct-population code. None of them implement spec-level behavioral choices in their own right — those decisions live in `internal/daemon`, `internal/events`, and `internal/mcp`. Accordingly:

- **COVERED (asserted by db_test.go):** 124 branches across the happy paths — `Open` migrate, orchestrator idempotency, role kind-conflict, binding lifecycle (Create/End/GetLive*), inbox + cross-role mark-read isolation, SetDelivered, role_status upsert, event cursor monotonicity, config get/set.
- **UNCOVERED-IMPLEMENTATION:** 44 branches that are SQL-error or scan-error paths (`fmt.Errorf` wrappers, `rows.Scan` failures in scan helpers, `time.Parse` ignored errors). These are defensive plumbing, not behavioral promises. They are NOT spec-required to be tested and are NOT flagged as gaps.
- **UNCOVERED-BEHAVIORAL:** 0.
- **CONTRADICTS:** 0.

## Test-alignment sub-check

Each data-layer scenario was checked against `db_test.go` to verify the test asserts the requirement's wording, not just exercises the code path.

| Spec scenario | Test | Verdict |
|---|---|---|
| "Coordinator task archived, role survives" (spec.md:10-13) | `TestBindings_LifecycleStartAndEnd` (db_test.go:185-220) | Partial — see finding internal-db-1 |
| "Same role rebound across multiple incarnations" (spec.md:15-18) | (none in db_test.go) | Gap — see internal-db-2 |
| "Mission and constraints meta absent → empty strings" (spec.md:94-97) | (none asserts empty-string-not-NULL behavior) | Gap — see internal-db-3 |
| "Role's argus_project preserved across incarnation" (spec.md:141-144) | (none in db_test.go) | Gap — see internal-db-4 |
| "Restart resumes from cursor" (spec.md:174-176) + monotonic | `TestEventCursor_GetSetMonotonic` (db_test.go:420-457) | Covered |
| "Coordinator without `to` rejected" (handler-level, but DB has secondary guard) | `TestMessages_CreateRequiresToRoleID` (db_test.go:347-361) | Covered (DB-side guard) |

All gaps are LOW severity. The data layer's behavior is correct in all cases; the tests just don't explicitly cover the spec's wording for these scenarios. Adding the missing assertions would be a small follow-up rather than a release blocker.

## Cross-module symbol awareness

Symbols flagged as "owned" by internal-db and consumed elsewhere (per `.workflow/audits/2026-05-24/symbols.json`):

- `EventCursor.Get/Set` — consumed in `internal/events/subscriber.go` and `cmd/hera/status.go`.
- `RoleStatus.Get/Upsert` — consumed in `internal/mcp/handler_status.go` and `handler_join.go`.
- `DeliveryQueuedNoBinding` — consumed in `internal/mcp/handler_send.go`.
- `ConfigDAO` — defined and tested but not yet referenced by any non-test caller. Not flagged: a `config` k/v table is explicitly documented in `doc.go:11` as part of the schema. It is forward-looking infrastructure, not an unimplemented spec promise.

## Notes / non-findings

- `roles.go:79-100` orders roles "coordinator first, then worker, then freelance" via `ORDER BY (CASE kind WHEN 'coordinator' THEN 0 ...)`. Not spec-required ordering, but matches the operational intuition.
- `bindings.go` has three live-lookup indexes (`bindings_live_by_role`, `bindings_by_task`, `bindings_by_worktree`) all with `WHERE ended_at IS NULL` — a precise reflection of how the rest of the daemon queries the table.
