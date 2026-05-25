# internal/events spec audit

- **Module:** `internal/events`
- **Date:** 2026-05-24
- **Files audited:** doc.go, types.go, payloads.go, subscriber.go, adopt.go, resync.go (+ adopt_test.go, subscriber_test.go for alignment)

## Branch coverage summary

| File | Branches | Covered | Notes |
|------|----------|---------|-------|
| doc.go | 0 | 0 | doc-only file |
| types.go | 0 | 0 | constants only |
| payloads.go | 4 | 4 | exercised indirectly via adopt + subscriber tests |
| subscriber.go | 8 | 6 | error branches (cursor.Get / cursor.Set errors) not directly tested |
| adopt.go | 42 | 18 | happy + 4 negative paths covered; ~24 warn/log error branches untested |
| resync.go | 18 | 0 | **no tests exist for ResyncHandler in this module** |
| **Total** | **72** | **28** | ~39% direct branch coverage |

## Spec→code mapping

### Requirement: Auto-adopt coordinator-spawned worker tasks
- **Impl:** `adopt.go:handleLinkCreated` enforces both conditions:
  - (1) parent task has a live hera binding AND that role's `Kind == KindCoordinator` (lines 52–70)
  - (2) `meta:hera.role == "worker"` on child task (lines 73–89)
- **Scenarios:**
  - "Both conditions met, task adopted" → `TestAdopt_HappyPath_RoleAndBindingCreated` (covers role + binding creation)
  - "Parent link present, meta absent" → `TestAdopt_MissingMeta_NotAdopted`
  - "Meta present, parent link absent" → `TestAdopt_ParentNotBound_NotAdopted` (parent has no binding) covers the "parent link absent" semantics
  - Bonus: `TestAdopt_MetaSaysNotWorker_NotAdopted` covers the meta-says-freelance path; `TestAdopt_ParentNotCoordinator_NotAdopted` covers the parent-kind-not-coordinator path

### Requirement: Auto-adopt copies mission and constraints from task meta
- **Impl:** `adopt.go:pickAdoptMeta` extracts `mission`/`constraints`; `Roles.Create` is called with both values (lines 99–110)
- **Scenarios:**
  - "Mission and constraints meta present" → `TestAdopt_HappyPath_RoleAndBindingCreated` asserts `role.Mission == "implement F2"` and `role.Constraints == "no breaking changes"`
  - "Mission and constraints meta absent" → **NOT covered by a dedicated test in this module**; only the missing-role-meta case is tested, which short-circuits before reaching `Roles.Create`. There is no test that confirms empty-string (not NULL) columns when role meta is present but mission/constraints are absent.

### Requirement: Stricter rule on auto-adoption logged
- **Impl:** `adopt.go:81` and `adopt.go:86` both emit `log.Info` for skipped adoptions
- **Spec wording:** log entry MUST include "the new task's id, the parent task's id, and the missing meta key"
- **Gap:**
  - Line 82 (missing role meta) includes `child` and `parent`, but the "missing meta key" is embedded only in the human-readable message, not as a structured field
  - **Line 86–88 (meta value != "worker") includes `child` and `value` but OMITS the parent task id.** This is the only INFO log for a skipped adoption that violates the spec's "MUST include the parent task's id" wording.
  - No test asserts log content (`adopt_test.go` does not capture or check log lines)

### Requirement: Event stream cursor persisted and replayed
- **Impl:** `subscriber.go:Run` loads cursor via `EventCursor.Get` (line 69), passes it as `since=` to `StreamEvents` (line 75), and persists `ev.ID` after every event (lines 79–82)
- **Scenarios:**
  - "Restart resumes from cursor" → `TestSubscriber_AdvancesCursor` (verifies `since=` is sent and cursor advances)
  - "Resync triggers task snapshot" → `resync.go:reconcile` implements it correctly, **but no test exists for it in this module**

### Requirement: Role identity outlives argus task lifecycle (task.archived ends binding)
- **Impl:** `adopt.go:handleTaskArchived` looks up live binding by `ev.TaskID` and ends it with reason `"argus_archived"` (lines 134–151)
- **Scenarios:**
  - "Coordinator task archived, role survives" → `TestAdopt_TaskArchived_BindingEnds` covers the binding-ends portion; the role-survives assertion is not made explicitly in this module (DAO-level guarantee).

### Requirement: Role metadata mirrored to argus task_meta (binding-create write)
- **Impl:** `adopt.go:128` calls `client.PutTaskMeta(child.ID, MetaKeyRole, "worker")` after binding creation
- **Scenarios:**
  - "Role meta written on binding" → `TestAdopt_HappyPath_RoleAndBindingCreated` asserts the put-write was recorded

### Requirement: Resync triggers task snapshot
- **Impl:** `resync.go:reconcile` calls `ListTasks`, walks every orchestrator's roles, and ends bindings whose `ArgusTaskID` is not in the live set with reason `"resync_missing"`
- **Scenarios:**
  - "Resync triggers task snapshot" → behaviorally correct, but **no test in `internal/events/` exercises this handler**

## Findings

### Behavioral gaps

1. **resync.go has zero test coverage.** No `TestResync_*` exists in this module. Every spec scenario tied to the resync requirement is unverified at the events-handler layer. The DAO-level `Bindings.End` is tested in `internal/db`, but the handler's reconciliation walk (orchestrators → roles → live bindings → end if vanished) is not.

2. **Mission/constraints absent path not directly tested.** Spec "Mission and constraints meta absent" requires empty-string (not NULL) columns. The happy-path test sets both, the missing-meta test exits before `Roles.Create`. No test confirms the role row's `Mission` and `Constraints` are empty strings when `meta:hera.role=worker` is present but `meta:hera.mission`/`meta:hera.constraints` are absent.

### Contradictions

1. **Skipped-adoption INFO log omits parent task id when meta value is wrong.** Spec ("Stricter rule on auto-adoption logged") requires the log to include "the new task's id, the parent task's id, and the missing meta key." `adopt.go:86–88` emits only `child` and `value`, dropping `parent`. The missing-meta branch (line 81–83) does include parent. Either rewrite to include `parent` (and a structured `missing_key` field) on both branches, or interpret the spec as applying only to the strictly "missing meta" case.

### Unimplemented promises

None at the spec-requirement level. Every "MUST" tied to this module has implementation. The contradiction above is a wording-fidelity issue, not an unimplemented promise.

### Test-alignment gaps

1. **No ResyncHandler test.** Spec requires snapshot + binding-end on resync; handler is wired in `daemon/run.go:97` but never exercised in unit tests. A test should:
   - Stand up a fake argus that serves `GET /api/tasks` with a subset of tasks.
   - Seed hera with bindings for both present and absent tasks.
   - Fire a `resync` event and assert the absent-task binding was ended with `end_reason="resync_missing"`.

2. **No log-capture for "skipped adoption" INFO.** Spec is explicit that an INFO log MUST fire on missing meta. Neither `TestAdopt_MissingMeta_NotAdopted` nor `TestAdopt_MetaSaysNotWorker_NotAdopted` asserts the log was emitted or what fields it carried. Without a `slog` capture handler in the test, the spec wording for log content is unverified.

3. **No test for mission/constraints empty-string fallback.** See behavioral gap #2.

4. **`TestAdopt_ParentNotBound_NotAdopted` semantics drift.** Spec scenario "Meta present, parent link absent" describes `task.created` for T4 with no subsequent `link.created` naming T4. The test instead fires `link.created` with a parent that has no binding — this exercises a different code branch (the `ErrNotFound` early return on parent lookup) and never reaches the meta check. The end result (no adoption) matches, but the path tested isn't the spec scenario.

5. **Cursor error-path branches in `subscriber.go:Run` untested.** `EventCursor.Get` failure (line 70) and `EventCursor.Set` failure (line 80) are not exercised. These are warn-only logs in the Set case and a hard return in the Get case; coverage would be defensive.

## Branch classifications

Branches in `adopt.go` and `resync.go` are dominated by error-handling early returns from DAO calls and argus HTTP calls. They are correctly placed and uniformly warn-log + return — no missing else clauses or fallthroughs detected.

## Cross-module symbol awareness

- Depends on `internal/argus`: `Client`, `Event`, `Task`, `MetaEntry`, `GetTaskMeta`, `GetTask`, `ListTasks`, `PutTaskMeta`, `StreamEvents` — all present.
- Depends on `internal/db`: `DB`, `Bindings`, `Roles`, `Orchestrators`, `EventCursor`, `KindCoordinator`, `KindWorker`, `CreateRoleInput`, `CreateBindingInput`, `ErrNotFound` — all present.
- `ResyncHandler` is consumed by `internal/daemon` (`run.go:97`); `AdoptHandler` is consumed there as well (verified via grep).
- `Subscriber` is the dispatch surface used by `internal/daemon`.

No dangling symbol references.
