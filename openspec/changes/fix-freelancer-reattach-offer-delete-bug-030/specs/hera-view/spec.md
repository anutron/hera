## ADDED Requirements

### Requirement: Worktree-missing reattach on a freelancer offers to delete its argus task

The system SHALL, when `OnReattach` hits the worktree-missing condition (the argus restart fails because the task's worktree path is gone) on a dead-session FREELANCE row — an unmanaged argus task with no hera role or binding (`RoleID == 0`) — fall back to the BUG-020 delete-only confirmation rather than surfacing the raw argus 500. A freelancer has no durable hera role to rebind, so revive (BUG-028) is impossible; the operator MUST instead be offered to delete the orphan.

Choosing delete MUST destroy the argus task DIRECTLY by id (the freelancer IS the task — there is no hera role or orchestrator row to delete), removing the task's git worktree AND branch server-side. A task argus reports as already gone (HTTP 404) MUST be treated as success, and a worktree-missing delete failure (the orphan's worktree is exactly what is already gone) MUST be tolerated as success so the orphan clears from the rail; any other delete failure MUST surface as an error. The confirmation and the delete MUST run off the event loop and MUST refresh the rail on success. Declining the confirmation MUST delete nothing.

This branch MUST NOT open the revive-or-delete picker (revive needs a durable role) and MUST NOT surface the raw argus 500 that the freelance case previously fell through to. The managed-role path (revive-or-delete) and the mixed-coord-header path (revive-or-delete, or delete-only when no coord role exists) are unchanged.

#### Scenario: Worktree-missing freelancer reattach offers delete-only

- **WHEN** the operator presses Enter on a dead-session freelance row (`RoleID == 0`) whose argus task is `T9`, and the restart fails because argus reports `worktree path missing`
- **THEN** hera MUST NOT show the raw argus 500 AND MUST NOT open the revive picker; it MUST open a delete-only confirmation naming the freelancer

#### Scenario: Confirming the freelancer delete removes its argus task

- **WHEN** the operator confirms the freelancer worktree-missing delete confirmation for task `T9`
- **THEN** hera MUST issue a direct delete of argus task `T9` (worktree + branch), tolerating an already-gone or worktree-missing task as success, AND MUST refresh the rail so the orphan clears — without touching any hera role or orchestrator row

#### Scenario: Declining the freelancer delete does nothing

- **WHEN** the operator declines (No / Esc) the freelancer worktree-missing delete confirmation
- **THEN** hera MUST NOT delete the argus task and MUST leave the rail unchanged
