## MODIFIED Requirements

### Requirement: `J` adopts a freelancer into a chosen coordinator

While the RAIL is focused and the selection is a freelancer row (an unmanaged argus task with no hera role or live binding, rendered in the Freelance section), pressing `J` SHALL open a target picker listing the active (non-archived) orchestrators. The picker SHALL be a themed, focusable, dismissable modal in which `Enter` selects the highlighted orchestrator and `Esc` cancels without change. The picker SHALL identify each orchestrator by its name and SHALL name the freelancer being adopted in its title. When no active (non-archived) orchestrator exists, pressing `J` SHALL surface visible feedback that a coordinator must be created first and SHALL NOT open the picker or create any role or binding.

Selecting an orchestrator SHALL adopt the freelancer into it by creating, server-side and without any agent action:

- a `worker` role under the chosen orchestrator, whose name defaults to the freelancer's argus task name and is de-collided (a numeric suffix is appended) when an active role of that name already exists under the orchestrator. The role SHALL record the freelancer's argus repo as its `argus_project` (write-once, consistent with roles created via the bootstrap flow), carried from the rail selection; and
- a live binding from the freelancer's argus task to that role. The binding SHALL record the freelancer's argus-task worktree path, so the adopted row's `^p` open-PR and pane operations resolve the same worktree the freelancer used.

This role-and-binding creation SHALL reuse the same creation path that `hera_join`'s attach-mode uses (the shared DAO `Roles.Create` + `Bindings.Create`), not a duplicate implementation. The freelancer's argus task SHALL be best-effort stamped `meta:hera.role=worker` for parity with `hera_join`; a transient failure to stamp the meta SHALL NOT undo or fail the binding.

After adoption the row SHALL leave the Freelance section and render as a worker under the chosen coordinator. The adopted agent SHALL remain independent: the binding exists immediately without the agent acting, and the agent MAY later `hera_join(cwd)` to claim the role and receive coordinator messages.

`J` SHALL ALSO re-parent a coordinator under another chosen coordinator, making it a sub-coordinator, REGARDLESS of whether the coordinator's coord session is currently live. A coordinator selection is EITHER a non-archived root orchestrator header that HAS a coordinator role (whether or not its coordinator argus task is currently live), OR a non-archived promoted sub-coordinator role row (a worker whose own task coordinates a child orchestrator) carrying its child orchestrator id and that task. An orchestrator that has NO coordinator role at all SHALL NOT be a re-parent target and SHALL fall through to the not-applicable feedback. An archived coordinator SHALL NOT be a re-parent target (it is handled by resurrect/unarchive flows).

Re-parenting SHALL resolve the coordinator's coordinator argus task id and coordinator worktree path from the coordinator role's LATEST binding — LIVE if one exists, otherwise the most-recent ENDED binding — because re-parenting is a structural link to the coordinator's argus task, which exists independent of session liveness. A coordinator whose coordinator role has never had a binding SHALL surface visible feedback and SHALL NOT create any role or binding.

On a coordinator selection, pressing `J` SHALL open the SAME target picker, EXCLUDING the coordinator being re-parented from the list (a coordinator cannot be nested under itself). When no OTHER active orchestrator exists, pressing `J` SHALL surface visible feedback that another coordinator must be created first and SHALL NOT create any role or binding. The picker title SHALL name the coordinator being re-parented.

Selecting a parent SHALL nest the coordinator under it by creating the SAME multi-binding the rail renders as a nested sub-coordinator: a `worker` role under the chosen parent (de-collided, defaulting to the coordinator's name) bound to the coordinator's coordinator argus task, reusing the coordinator's coordinator worktree path (resolved from the latest binding). The coordinator's whole subtree (its roles/workers) SHALL move with it, because the subtree is derived from the coordinator itself, which is left untouched — only its parent linkage changes. The coordinator's argus task `meta:hera.role` SHALL NOT be changed (it remains the coordinator of its own orchestrator).

Re-parenting SHALL be IDEMPOTENT. Before creating the new link, re-parenting SHALL tear down EVERY prior parent linkage for the coordinator's coord task — every binding of that task on a role OTHER than the coordinator's own coordinator role — whether the binding is LIVE or ENDED. A LIVE prior parent-link binding SHALL be ended with `end_reason="reparented"` before its role is removed; the parent-link role SHALL be deleted (its bindings removed by cascade) regardless of binding liveness. This guarantees both that the coordinator is never nested under two parents at once AND that pressing `J` repeatedly — including on a coordinator whose prior link binding was ended by the resync reconciler because its coord task is gone from argus (`end_reason="resync_missing"`) — never accumulates de-collided duplicate link rows. The coordinator's OWN coordinator role SHALL NEVER be torn down; it SHALL be identified by role id (robust against a legacy binding carrying a NULL orchestrator id).

Re-parenting SHALL be rejected, with visible feedback and no role or binding created, when the chosen parent is the coordinator itself or any of the coordinator's own descendants (reusing the subtree walk that backs `^d`/`SubtreeOrchIDs`): nesting a coordinator under its own subtree would create a cycle.

The binding operation SHALL run off the tview event loop (the async-mutate pattern), so `J` never blocks or deadlocks the loop; while one adopt or re-parent is in flight a second SHALL no-op with visible feedback rather than running concurrently.

`J` SHALL be RAIL-focus-only. In a pane (COORD/AGENT focus) the `J` rune SHALL forward to the bound task's PTY like any other character. The lowercase `j` navigation key SHALL be unaffected.

#### Scenario: `J` on a freelancer creates a worker binding under the chosen coordinator

- **WHEN** the operator selects a freelancer, presses `J`, and picks an orchestrator from the picker
- **THEN** a `worker` role and a live binding from the freelancer's argus task to that role MUST be created under the chosen orchestrator, and the row MUST re-render as a worker under that coordinator (out of the Freelance section)

#### Scenario: The default role name is de-collided

- **WHEN** the freelancer's task name matches an existing active role name under the chosen orchestrator
- **THEN** the adopted role MUST be created under a de-collided name (a numeric suffix appended) rather than failing or colliding with the existing role

#### Scenario: `J` on a coordinator re-parents it under the chosen coordinator

- **WHEN** the operator selects a coordinator (a root orchestrator header or a sub-coordinator role row), presses `J`, and picks a different coordinator from the picker
- **THEN** a `worker` role bound to the coordinator's coordinator argus task MUST be created under the chosen parent, and the coordinator (with its whole subtree) MUST render nested under that parent as a sub-coordinator

#### Scenario: `J` re-parents a coordinator whose coord session is not live

- **WHEN** the operator selects a coordinator whose coordinator argus task has ended (no live coord binding, so the header carries no live coord task), presses `J`, and picks a parent coordinator
- **THEN** the coordinator MUST be re-parented, with the new link binding reusing the coordinator argus task id and worktree path recovered from the coordinator role's most-recent ENDED binding, and MUST NOT fall through to the not-applicable notice

#### Scenario: Repeated re-parenting of a dormant coordinator does not accumulate duplicate links

- **WHEN** the operator presses `J` on a dormant coordinator (its coord task gone from argus) and picks a parent, the resync reconciler then ends the new link binding (`end_reason="resync_missing"`) leaving the link role behind, and the operator presses `J` again and picks a parent
- **THEN** exactly ONE worker link role MUST exist under the parent for the coordinator's coord task, carrying the clean (non-de-collided) name — the stale prior link role MUST have been deleted rather than left to de-collide a duplicate (`2a-team-2`, `2a-team-3`, …)

#### Scenario: The coordinator picker excludes the coordinator itself

- **WHEN** the operator presses `J` on a coordinator while that coordinator is among the active orchestrators
- **THEN** the picker MUST NOT list the coordinator being re-parented, and when it is the only active orchestrator the view MUST surface feedback that another coordinator must be created first and MUST NOT create any role or binding

#### Scenario: Re-parenting a coordinator under itself or a descendant is rejected

- **WHEN** the operator picks the coordinator itself, or one of the coordinator's own sub-coordinators, as the parent
- **THEN** the re-parent MUST be rejected with visible feedback and MUST NOT create any role or binding (cycle guard)

#### Scenario: Re-parenting a coordinator already nested elsewhere moves it cleanly

- **WHEN** the operator re-parents a coordinator that is already a sub-coordinator under one parent to a different parent
- **THEN** the prior parent's worker link binding MUST be ended (`end_reason="reparented"`) and its link role removed, leaving exactly one parent linkage (under the new parent)

#### Scenario: `J` on a non-adoptable row surfaces feedback

- **WHEN** the operator presses `J` while a managed leaf agent, an orchestrator header with no coordinator role, an archived coordinator header, or a section row is selected
- **THEN** the view MUST surface visible feedback that a freelancer or a coordinator must be selected, and MUST NOT create any role or binding (never a silent no-op)

#### Scenario: `J` on a coordinator whose coord role never had a binding surfaces feedback

- **WHEN** the operator presses `J` on a coordinator whose coordinator role has never been bound to an argus task, and picks a parent
- **THEN** the re-parent MUST be rejected with visible feedback and MUST NOT create any role or binding

#### Scenario: `J` on a freelancer with no argus task id surfaces feedback

- **WHEN** the operator presses `J` on a freelancer row that carries no argus task id
- **THEN** the view MUST surface visible feedback and MUST NOT create any role or binding

#### Scenario: An already-bound task is not adopted again

- **WHEN** the freelancer's argus task already has a live binding to some orchestrator (a race or mislabeled row)
- **THEN** the adopt MUST be rejected with visible feedback and MUST NOT create a second binding

#### Scenario: No active coordinator to adopt into surfaces feedback

- **WHEN** the operator presses `J` on a valid freelancer but no active (non-archived) orchestrator exists
- **THEN** the view MUST surface visible feedback that a coordinator must be created first, and MUST NOT open the picker or create any role or binding

#### Scenario: `J` in a pane forwards to the PTY

- **WHEN** focus is in a COORD or AGENT pane and the operator types `J`
- **THEN** the `J` rune MUST be forwarded to the bound task's PTY and MUST NOT open the picker
