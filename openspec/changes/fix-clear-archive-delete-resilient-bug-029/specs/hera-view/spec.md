## MODIFIED Requirements

### Requirement: Archive, delete, and prune are distinct removal verbs

The system SHALL provide four distinct removal actions on the rail: `a` toggles archive on the selected coordinator/agent (reversible; moves it into the appropriate Archive expando per the rail requirement). `^d` deletes the selected coordinator/agent — destroying its argus task, git worktree, and branch — behind a destructive confirmation that names the target (and warns when it has child agents). `^r` prunes all completed agents fleet-wide — removing finished tasks and cleaning their worktrees/branches — behind a confirmation, mirroring argus's prune-completed. `C` clears the ENTIRE archive under the selected coordinator — completing, deleting the argus task, removing the worktree, and pruning the hera row of every archived descendant — behind a confirmation. None of `^d`, `^r`, or `C` MUST perform any destructive operation without explicit confirmation.

`C` (clear-archive) MUST, for EVERY archived non-coordinator descendant under the target coordinator — regardless of completion state (complete, incomplete, or `○` fully-detached: no live session, worktree gone) — do the following, in order:

- Resolve the role's bound argus task id + worktree path from its LIVE binding when one exists, FALLING BACK to its most recent ENDED binding (an archived role's binding has ended while its `argus_task_id` is preserved). A binding-lookup failure MUST NOT abort the sweep — the hera row is still pruned.
- Mark the bound argus task `:checked:` (complete) ONLY when it is not already complete. This step is BEST-EFFORT: a status read or write that fails on a dead, pruned, or detached task MUST be logged and skipped, NEVER aborting — `C` clears the archive, it does not require completion first.
- DESTROY the underlying argus task via `DELETE /api/tasks/{id}` (argus removes the task's git worktree AND branch server-side), so the task can NEVER resurface as a freelancer — the same orphan class `^d` coord-delete fixed. A task argus reports as already gone (HTTP 404, treated as success by the client) or whose worktree was removed out-of-band (the worktree-missing error body) MUST be a clean skip; any OTHER delete failure MUST be logged and counted, NEVER aborting the sweep.
- Remove the worktree locally as a defensive fallback, BEST-EFFORT (the BUG-018 guard): an empty path, a missing directory, a worktree whose `.git` is gone, or a detached git admin entry MUST be a soft no-op, AND any other removal failure MUST be logged and counted rather than aborting.
- Delete the hera role row. A row-delete failure MUST be logged and counted, and the sweep MUST continue.

`C` MUST NEVER abort the batch on any single per-role failure — the `○` detached case in particular MUST NOT halt the sweep. Per-role failures MUST be collected into a summary (`Found`, `Pruned`, `WorktreeSkipped`, `Errors`) rather than propagated as an error that would suppress the rail refresh; the returned error is reserved for the top-level role enumeration failing. The caller fires "nothing to do" only when `Found == 0` (no archived descendants at all), so already-complete archived workers are cleared rather than short-circuited. The coordinator role itself MUST be skipped.

#### Scenario: Archive is reversible and moves to the fold

- **WHEN** the operator presses `a` on an active agent
- **THEN** the agent MUST move into its coordinator's Archive expando AND pressing `a` on it again MUST restore it to the active list

#### Scenario: Delete confirms before destroying the worktree

- **WHEN** the operator presses `^d` on an agent
- **THEN** a confirmation naming the agent MUST appear AND no task/worktree/branch deletion MUST occur until the operator confirms

#### Scenario: Prune confirms and targets only completed agents

- **WHEN** the operator presses `^r`
- **THEN** a confirmation listing the completed agents to remove MUST appear AND only agents in the completed state MUST be pruned on confirm

#### Scenario: `C` deletes the underlying argus task for every archived state

- **WHEN** the operator confirms `C` on a coordinator whose Archive holds a complete worker (`Tdone`), an incomplete worker (`Ttodo`), and a `○` fully-detached worker (`Tdetached`, no live session, worktree gone)
- **THEN** hera MUST issue `DELETE /api/tasks/{id}` for `Tdone`, `Ttodo`, AND `Tdetached`, prune all three hera role rows, AND report `Found == 3` / `Pruned == 3` — so none of the three resurfaces as a freelancer

#### Scenario: `C` does not abort on a `○` detached entry

- **WHEN** the operator confirms `C` and one archived worker is `○` fully-detached such that its argus delete fails with a worktree-missing error
- **THEN** hera MUST treat that failure as a soft skip (not counted as an error), prune that worker's hera row, AND continue clearing every remaining archived worker — the sweep MUST NOT halt

#### Scenario: `C` collects a genuine failure without aborting

- **WHEN** the operator confirms `C` and one archived worker's argus delete fails for a reason other than already-gone (e.g. a connection error)
- **THEN** hera MUST log and count that failure in the summary's `Errors`, still prune that worker's hera row, AND still clear every other archived worker — returning a summary rather than aborting
