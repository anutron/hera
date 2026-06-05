## ADDED Requirements

### Requirement: `J` adopts a freelancer into a chosen coordinator

While the RAIL is focused and the selection is a freelancer row (an unmanaged argus task with no hera role or live binding, rendered in the Freelance section), pressing `J` SHALL open a target picker listing the active (non-archived) orchestrators. The picker SHALL be a themed, focusable, dismissable modal in which `Enter` selects the highlighted orchestrator and `Esc` cancels without change. The picker SHALL identify each orchestrator by its name and SHALL name the freelancer being adopted in its title. When no active (non-archived) orchestrator exists, pressing `J` SHALL surface visible feedback that a coordinator must be created first and SHALL NOT open the picker or create any role or binding.

Selecting an orchestrator SHALL adopt the freelancer into it by creating, server-side and without any agent action:

- a `worker` role under the chosen orchestrator, whose name defaults to the freelancer's argus task name and is de-collided (a numeric suffix is appended) when an active role of that name already exists under the orchestrator. The role SHALL record the freelancer's argus repo as its `argus_project` (write-once, consistent with roles created via the bootstrap flow), carried from the rail selection; and
- a live binding from the freelancer's argus task to that role. The binding SHALL record the freelancer's argus-task worktree path, so the adopted row's `^p` open-PR and pane operations resolve the same worktree the freelancer used.

This role-and-binding creation SHALL reuse the same creation path that `hera_join`'s attach-mode uses (the shared DAO `Roles.Create` + `Bindings.Create`), not a duplicate implementation. The freelancer's argus task SHALL be best-effort stamped `meta:hera.role=worker` for parity with `hera_join`; a transient failure to stamp the meta SHALL NOT undo or fail the binding.

After adoption the row SHALL leave the Freelance section and render as a worker under the chosen coordinator. The adopted agent SHALL remain independent: the binding exists immediately without the agent acting, and the agent MAY later `hera_join(cwd)` to claim the role and receive coordinator messages.

The binding operation SHALL run off the tview event loop (the async-mutate pattern), so `J` never blocks or deadlocks the loop; while one adopt is in flight a second SHALL no-op with visible feedback rather than running concurrently.

`J` SHALL be RAIL-focus-only. In a pane (COORD/AGENT focus) the `J` rune SHALL forward to the bound task's PTY like any other character. The lowercase `j` navigation key SHALL be unaffected.

#### Scenario: `J` on a freelancer creates a worker binding under the chosen coordinator

- **WHEN** the operator selects a freelancer, presses `J`, and picks an orchestrator from the picker
- **THEN** a `worker` role and a live binding from the freelancer's argus task to that role MUST be created under the chosen orchestrator, and the row MUST re-render as a worker under that coordinator (out of the Freelance section)

#### Scenario: The default role name is de-collided

- **WHEN** the freelancer's task name matches an existing active role name under the chosen orchestrator
- **THEN** the adopted role MUST be created under a de-collided name (a numeric suffix appended) rather than failing or colliding with the existing role

#### Scenario: `J` on a non-freelancer row surfaces feedback

- **WHEN** the operator presses `J` while a coordinator, a managed agent, an orchestrator header, or a section row is selected
- **THEN** the view MUST surface visible feedback that only freelancers can be adopted, and MUST NOT create any role or binding (never a silent no-op)

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
