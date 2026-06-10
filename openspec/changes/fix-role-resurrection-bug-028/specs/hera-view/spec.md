## ADDED Requirements

### Requirement: Resurrecting a role whose worktree is gone offers a fresh instance

The system SHALL provide a RESURRECT operation distinct from reattach. Reattach (`claude --resume <session>`) works only while the role's worktree still exists; resurrect handles the case where the worktree/argus task is GONE (e.g. the operator pruned argus `:check:` worktrees to reclaim disk), leaving the role dormant and uninteractable.

Resurrect SHALL be fully programmatic and BORN-BOUND (the same pattern as spawn-worker / new-coordinator): it MUST create a FRESH argus task in the role's stored `argus_project`, MUST end the role's stale live binding (the dead instance's binding, never ended because the worktree vanished out-of-band), and MUST insert a NEW binding tying the fresh argus task to the EXISTING role id. Role identity MUST be preserved — the role's id, name, kind, prompt, and orchestrator MUST be unchanged, and NO new role row may be created. The new argus session MUST be seeded with the role's stored (verbatim) prompt re-wrapped in the kind-appropriate orientation (a coordinator orientation naming the orchestrator, or a worker orientation naming the role's coordinator), and the prompt MUST be auto-submitted via a carriage return so the session starts without a manual Enter.

Resurrect MUST work for both workers and coordinators. A resurrected coordinator role MUST come live in its existing place in the tree (its `orchestrator_id` is unchanged), so a dormant sub-coordinator whose worktree was pruned reappears live under its parent.

A role with an empty `argus_project` MUST be rejected as a system error (the project is write-once; an empty value is an internal-consistency bug), distinct from a user-correctable validation error, and no argus task may be created in that case. A failed worktree-path lookup MUST soft-degrade (the binding is still inserted, possibly with an empty path); a failed end-of-stale-binding MUST be logged but MUST NOT abort the resurrection.

The system SHALL upgrade the worktree-missing reattach recovery (added by BUG-020) from a delete-only confirmation to a THREE-way choice: when `OnReattach` hits the worktree-missing condition on a managed role row, OR on a mixed-coord header that HAS a coordinator role, hera MUST open a picker offering "revive a fresh instance" (listed FIRST) and "delete permanently", with cancel (Esc) doing neither. Choosing revive MUST route to the resurrect operation against the role's id (the worker role, or the header's coordinator role), and on success MUST show the REATTACHING splash on the fresh task's pane, notify the pane-reattach notifier with the fresh task id, and queue-select the revived row. Choosing delete MUST keep the BUG-020 behavior (orchestrator → delete orchestrator, role → delete role). A revive failure MUST surface its error and MUST NOT fall through to delete. When revive is impossible — a freelancer (no hera role) or a header with no coordinator role to rebind — hera MUST fall back to the prior BUG-020 delete-only confirmation. The picker and both terminal actions MUST run off the event loop and MUST refresh the rail on success.

#### Scenario: Revive a worktree-missing worker mints a fresh born-bound instance

- **WHEN** `ResurrectRole` is invoked against a worker role whose worktree is gone and which still carries a stale live binding to the dead task
- **THEN** hera MUST create a fresh argus task in the role's `argus_project` named after the role, end the stale binding, insert a NEW live binding tying the fresh task to the SAME role id, auto-submit the prompt via CR, and leave the role's id/name/kind/prompt unchanged

#### Scenario: Revive a worktree-missing coordinator comes live in place

- **WHEN** `ResurrectRole` is invoked against a coordinator role whose worktree is gone
- **THEN** hera MUST create a fresh argus task in the role's `argus_project` with the coordinator orientation, bind it to the SAME coord role id under the role's existing orchestrator, so the coordinator comes live in its existing place in the tree

#### Scenario: Resurrect rejects a role with no argus_project

- **WHEN** `ResurrectRole` is invoked against a role whose `argus_project` is empty
- **THEN** hera MUST return a system error (not a validation error) and MUST NOT create any argus task

#### Scenario: Worktree-missing reattach offers revive and delete

- **WHEN** the operator presses Enter against a dead-session worker row (or a mixed-coord header with a coord role) and the restart fails because argus reports `worktree path missing`
- **THEN** hera MUST NOT show the raw argus 500; it MUST open a picker listing "revive a fresh instance" first and "delete permanently" second

#### Scenario: Choosing revive routes to resurrect and shows the splash

- **WHEN** the operator chooses "revive a fresh instance" on the worktree-missing picker
- **THEN** hera MUST call the resurrect operation with the role's id, show the REATTACHING splash on the fresh task's pane, and queue-select the revived row — and MUST NOT delete the role

#### Scenario: Choosing delete keeps the BUG-020 behavior

- **WHEN** the operator chooses "delete permanently" on the worktree-missing picker
- **THEN** hera MUST route to the delete path (orchestrator → delete orchestrator, role → delete role) and MUST NOT resurrect

#### Scenario: Revive is impossible falls back to delete-only

- **WHEN** the worktree-missing row is a freelancer (no hera role) OR a header with no coordinator role to rebind
- **THEN** hera MUST fall back to the BUG-020 delete-only confirmation rather than offering revive
