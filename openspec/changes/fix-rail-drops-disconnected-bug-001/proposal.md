# Rail keeps disconnected-binding roles reachable in the active tree (BUG-001)

## Why

Roles whose hera binding has DISCONNECTED (session ended) were reported to disappear from the rail ENTIRELY — unreachable, NOT bucketed into Archive — even though their argus task records still exist and are NOT archived. Observed on orchestrator `sherlock-3b`: ~30 roles across sub-coordinators, but the rail rendered only the ONE row whose session was still attached.

Dropping a row strands recoverable work: a completed/disconnected worker's worktree + transcript persist (only `^d` runs `git worktree remove`), and the intended recovery path is to resume an argus session from that worktree and rebind via `hera_join`, which depends on the row being SELECTABLE.

Investigation (TDD, `internal/view`) found the primary hypothesis — role enumeration via LIVE bindings only — is ALREADY FIXED: `populateRail` falls back to a role's most-recent binding (the BUG-027 `CoordLinkTaskID` / latest-binding work), so a plain disconnected worker whose record exists stays in the active tree. A regression test confirms this.

The SURVIVING drop is transitive and one level up, in the sub-coordinator NESTING recursion. hera's model is flat, so a sub-coordinator is a multi-binding: a worker role under a parent orchestrator whose bound task equals a child orchestrator's coord task. `resolveSubCoordinators` CONSUMES the child orchestrator (removes it from the top level) and nests it beneath that parent-link worker row. But `appendOrchChildren` recursed into `childOrch` ONLY for ACTIVE roles. When the parent-link row buckets (its bound coord task is dead / argus-archived / the role is hera-archived), it renders inside the Archive expando as a plain leaf row with NO `childOrch` recursion — so the entire child subtree renders NOWHERE: not at the top level (consumed), not nested (parent bucketed), not in any Archive expando. Unreachable. This is the same "live-only lookup silently skips" anti-pattern, applied to the nesting recursion rather than enumeration.

## What Changes

- **`appendOrchChildren` recurses into a bucketed sub-coordinator's `childOrch`:** the archived-roles branch now nests a bucketed parent-link row's child subtree one level deeper (inside the Archive expando), mirroring the active branch — same cycle guard and fold check. Wherever the parent-link row renders, its child subtree renders beneath it, so the subtree stays REACHABLE (via folding the Archive expando open, or `l` / an active filter force-open). This restores the existing reachability invariant ("archiving a row never makes it unreachable") for the transitive sub-coordinator case.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: makes explicit, and fixes, the reachability invariant for a sub-coordinator whose parent-link worker row buckets — its child subtree MUST remain reachable nested beneath the bucketed row rather than dropping out of the rail entirely.

## Impact

- `internal/view/rail_list.go` — `appendOrchChildren` archived-roles branch recurses into `childOrch` (one block, mirroring the active branch).
- `internal/view/bug001_rail_drops_test.go` — new regression tests: a disconnected worker whose record exists stays reachable (locks the already-fixed primary case), and a sub-coordinator's grandchild stays reachable when the parent-link row buckets (the surviving drop).

## Notes

- The originally-filed PRIMARY hypothesis (live-only role enumeration) does NOT reproduce on current code — it was resolved by the BUG-027 latest-binding fallback. The root cause DIVERGES from the hypothesis; the actual surviving defect is the nesting-recursion gap above.
- Classification: CODE-DRIFT. The fix restores the existing reachability invariant (`openspec/specs/hera-view/spec.md`: "archiving a row never makes it unreachable"); the delta only ADDS the previously-implicit transitive scenario so the invariant is locked against regression. No requirement wording is loosened.
