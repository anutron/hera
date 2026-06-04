## Context

`add-spawn-worker-from-rail` is archived. Two post-archive quality gates (ralph-review, spec-audit) found no contradictions and no unimplemented promises, but six edge/failure-path items remain. Five are behaviors the original `design.md` (D6 worktree resolution, D7 / Risks: partial-failure no-rollback, best-effort auto-select) deliberately chose, but which never made it into a `hera-view` base-spec scenario; the sixth is a genuine UX gap (silent empty-prompt close). This change clears all six so the spec fully describes the shipped behavior and the failure paths are test-pinned.

## Goals / Non-Goals

**Goals:**

- One behavior change: empty-prompt confirm gives operator-visible feedback (consistent with hera's "never a silent no-op on a live surface" principle).
- Spec scenarios + tests that pin the five intended failure/degraded-path behaviors so future reviewers don't read them as regressions.

**Non-Goals:**

- Changing any of the five already-correct behaviors. The decisions (soft-degrade on GetTask failure, no-rollback on insert failure, best-effort auto-select, resolve archived rows to their coordinator) stand; this change documents and tests them, it does not alter them.
- Rolling back orphaned argus tasks on insert failure (explicitly rejected in the original D7/Risks — deletion is itself fallible and the orphan surfaces as a recoverable freelancer).

## Decisions

### D1: Empty-prompt confirm surfaces a dismissible notice (the one code change)

`OnNewWorker`'s modal-submit callback currently `return`s on an empty/whitespace prompt, closing the modal silently. Change it to surface a dismissible "w: prompt is required" notice (the same `notApplicable`/error-modal path the other non-applicable cases use). No argus or DB call is made either way; only the feedback changes. The base requirement already says "rejected with a validation error" — this makes the observable behavior match.

*Alternative considered:* keep the modal open and re-prompt — rejected, the input modal has no re-open hook and a dismissible notice is consistent with every other not-applicable path.

### D2: Document the failure/degraded paths as scenarios (no code change)

The following are already implemented correctly; this change adds scenarios + (where missing) tests:

- **GetTask failure → soft-degrade:** worker role+binding still inserted with empty `worktree_path`, logged; spawn not aborted. Worktree-dependent ops (`^d`/`^p`/resize) already soft-skip an empty path. (Tested: `TestSpawnWorker_GetTaskFailureSoftDegrades`.)
- **Insert failure after task creation → orphan, no rollback:** logs the orphan and returns the error (surfaced as an error modal); the live argus task survives and appears as a freelancer. Add a test asserting no `DeleteTask` call on a role/binding insert failure.
- **Auto-select best-effort:** the new row is selected on the next broadcaster-driven `populateRail` in which it exists; if it never appears within `maxPendingSelectMisses` repopulates the pending select is abandoned and logged. (Tested: `TestApp_QueueSelectRole_AbandonsUnresolvableAfterBound`.)
- **Archived/dead agent-row resolution:** `w` on an archived or dead agent row resolves to that agent's still-valid coordinator and spawns (the selected row's liveness does not matter; the coordinator is the target). Add a test.

### D3: Degenerate-coordinator feedback under the existing not-applicable requirement

`OnNewWorker` already surfaces a notice when a coordinator-shaped selection cannot resolve a target: an orchestrator header with no coord role (`CoordRoleID==0`), a sub-coordinator row with no child orchestrator (`ChildOrchestratorID==0`), or a leaf role row with no owning coordinator (`OrchestratorID==0`/`CoordRoleID==0`). Fold this into the "Non-applicable mutation keys give visible feedback" requirement by naming `w` and adding a scenario. Add bridge tests for the cases not already covered.

## Risks / Trade-offs

- **Over-specifying failure paths** → low; these are real observable behaviors with side effects on the persistent store and on argus, worth pinning.
- **The one code change (D1) could mask a real error** → no; it only replaces a silent `return` with a notice on the empty-prompt branch; the substrate-failure error path is untouched.

## Migration Plan

Additive. No schema change, no argus change. Rollback = revert. The empty-prompt notice is a pure UX addition.

## Open Questions

None.

## Acceptance criteria

- it should surface a dismissible "prompt is required" notice (not a silent close) when the spawn-worker modal is confirmed with an empty prompt
- it should still insert the worker role+binding with an empty worktree path (and log) when GetTask fails after task creation
- it should not delete the created argus task when a role/binding insert fails after creation (orphan logged, error surfaced)
- it should resolve an archived or dead agent-row selection to its coordinator and spawn
- it should give a "not applicable" notice when `w` is pressed on a coordinator-shaped row that cannot resolve a target coordinator
- it should abandon (and log) the post-spawn auto-select after a bounded number of repopulates if the new row never appears
