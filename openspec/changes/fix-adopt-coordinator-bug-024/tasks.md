# Tasks

## 1. Implementation

- [x] 1.1 Add `OrchestratorID` to `ops.Binding` (`internal/view/ops/types.go`) and carry it in `adaptBinding` (`internal/view/ops_adapters.go`) so a binding's orchestrator is distinguishable.
- [x] 1.2 Implement `ReparentCoordinator(ctx, ReparentCoordInput) (*ReparentCoordResult, error)` (`internal/view/ops/reparent.go`): validate the coord task id, reject self (`parent == child`), reject a parent in `SubtreeOrchIDs(child)` (cycle), derive the child's coord worktree from its live coord binding, end+delete any prior parent link binding/role (`end_reason="reparented"`), then create a de-collided `worker` role + binding for the coord task under the parent.
- [x] 1.3 Add `ReparentCoordinator` to the `mutationService` interface (`internal/view/mutations.go`).
- [x] 1.4 Rewrite `OnAdopt` (`internal/view/mutations.go`): classify the selection via `coordAdoptTarget` (a live root orchestrator header or a promoted sub-coordinator role row); route coordinators to `adoptCoordinator` (the picker excludes the coord itself; selecting a parent calls `ReparentCoordinator`); leave the freelancer path unchanged; update the not-applicable notice to "select a freelancer or a live coordinator".

## 2. Tests

- [x] 2.1 `TestReparentCoordinator_CreatesNestingBinding`: a worker link role + binding for the child's coord task appears under the parent (reusing the child's coord worktree), and `SubtreeOrchIDs(parent)` then reaches the child.
- [x] 2.2 `TestReparentCoordinator_MovesFromExistingParent`: the old parent link binding ends (`reparented`) and its role is deleted; exactly one link binding remains, under the new parent.
- [x] 2.3 `TestReparentCoordinator_RejectsSelf` / `_RejectsDescendantCycle`: cycle guard rejects adopting a coord under itself or its own descendant, creating no link role.
- [x] 2.4 `TestReparentCoordinator_RejectsEmptyTaskID` / `_RejectsUnknownParent` / `_RejectsDormantCoordinator` / `_DeCollidesRoleName`.
- [x] 2.5 Bridge: `TestBridge_OnAdopt_LiveCoordHeader_Reparents` (picker excludes self, re-parents, refreshes), `_SubCoordRow_Reparents`, `_Coord_OnlySelfActive_Feedback`, `_Coord_ServiceError_ShowsErrorModal`, `_ArchivedCoord_NotApplicable`; update the managed-worker / coordless-header not-applicable assertions to the new message.
- [x] 2.6 `make test` passes with `-race`.
