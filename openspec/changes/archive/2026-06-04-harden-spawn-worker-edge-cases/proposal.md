## Why

The `add-spawn-worker-from-rail` change shipped and archived. A post-archive ralph-review and spec-audit confirmed zero contradictions and zero unimplemented promises, but surfaced six edge/failure-path items: one real UX gap (an empty prompt closes the modal silently instead of giving feedback) and five behaviors the code already implements per the design doc but the merged `hera-view` base spec never enumerated. This change closes all six — no behavioral debt left behind — by tightening one code path and documenting the failure-path contract with scenarios + tests.

## What Changes

- **Empty-prompt feedback (Q3):** confirming the spawn-worker modal with an empty/whitespace prompt MUST surface a dismissible "prompt is required" notice instead of silently closing. Code + scenario.
- **GetTask-fail soft-degrade (Gap 1):** spec-document that when `GET /api/tasks/{id}` fails after the task is created, the worker role+binding are still inserted (with an empty `worktree_path`) and the failure is logged — the spawn is not aborted.
- **Insert-failure orphan (Gap 2):** spec-document that when a role/binding insert fails after the argus task was created, hera does NOT delete the task (it logs the orphan, which surfaces as a freelancer) and surfaces the error. Add a test.
- **Archived/dead agent-row spawn (Q4):** spec-document + test that `w` on an archived or dead agent row still resolves to its (valid) coordinator and spawns.
- **Degenerate-coordinator feedback (Gap 3):** spec-document that `w` on a coordinator-shaped row that cannot resolve a coord role (or a sub-coordinator with no child orchestrator) gives a "not applicable" notice. Add tests.
- **Auto-select best-effort tail (Gap 4):** spec-document that the post-spawn auto-select is applied on the next broadcaster-driven repopulate in which the row appears, and is abandoned (logged) after a bounded number of misses if it never appears.

## Capabilities

### New Capabilities

<!-- None. -->

### Modified Capabilities

- `hera-view`: extend the "`w` spawns a worker under the selected coordinator" requirement to specify empty-prompt visible feedback and the failure/degraded-path contract (GetTask failure, insert-failure orphan, auto-select best-effort, archived-row resolution); extend the "Non-applicable mutation keys give visible feedback" requirement to cover `w` on a degenerate coordinator-shaped row.

## Impact

- **Specs:** `openspec/specs/hera-view/spec.md` (modified via delta — two MODIFIED requirements).
- **Code:** `internal/view/mutations.go` (`OnNewWorker` empty-prompt branch → visible notice). The other five items are already-correct behavior; this change adds the missing tests.
- **Tests:** `internal/view/mutations_test.go`, `internal/view/ops/spawn_worker_test.go`, `internal/view/app_test.go`.
- **No DB migration, no argus change.**
