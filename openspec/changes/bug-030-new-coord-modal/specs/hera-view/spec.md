## MODIFIED Requirements

### Requirement: New-coordinator modal mirrors argus New Task

The system MUST open a five-field "New coordinator" modal (mirroring argus's New Task
layout) when the operator presses `n`. The modal MUST present: Name (required, single-line),
Project (dropdown sourced from argus projects), Branch (optional single-line), Backend
(dropdown sourced from argus backends, defaulting to `claude`), and Prompt (multi-line
textarea for startup instructions). All field widths MUST be derived from
`formFieldWidth("Name","Project","Branch","Backend","Prompt")` so inputs stay inside the
frame (BUG-002). Empty Name submissions MUST be silently dropped. The Mission field is
removed; Prompt subsumes it. After `CreateTask` succeeds, a CR byte MUST be sent to the
new task's PTY via `PostTaskInput` (auto-submit); a failure MUST be logged and MUST NOT
propagate. `OnNew` MUST load projects and backends before opening the form, surfacing an
error modal and aborting on load failure.

#### Scenario: New coordinator form opens with five fields

- **GIVEN** the operator is focused on the RAIL
- **WHEN** the operator presses `n`
- **THEN** a "New coordinator" modal appears with five form items: Name, Project, Branch, Backend, and Prompt

#### Scenario: Submit with non-empty name creates coordinator

- **GIVEN** the "New coordinator" modal is open
- **WHEN** the operator enters a valid name, selects a project, and confirms with Enter or Submit
- **THEN** `NewOrchestrator` is called with all entered values
- **AND** a CR byte is sent to the new task's PTY to auto-submit the bootstrap prompt

#### Scenario: Empty name drops submission silently

- **GIVEN** the "New coordinator" modal is open with an empty Name field
- **WHEN** the operator confirms with Enter or Submit
- **THEN** `NewOrchestrator` is NOT called and the rail is NOT refreshed

#### Scenario: Auto-submit CR sent after task creation

- **GIVEN** `CreateTask` succeeds for a new coordinator
- **THEN** `PostTaskInput` MUST be called with the byte `\r` for the created task ID
- **AND** a `PostTaskInput` failure MUST NOT propagate as a create error

#### Scenario: Project list failure aborts form open

- **GIVEN** `ListProjects` returns an error
- **WHEN** the operator presses `n`
- **THEN** an error modal is shown and the "New coordinator" form does NOT open

#### Scenario: Field widths contained inside modal frame

- **GIVEN** labels "Name", "Project", "Branch", "Backend", "Prompt"
- **THEN** `formFieldWidth(labels...)` MUST return a positive value
- **AND** `longest_label + 3 + formFieldWidth` MUST NOT exceed `modalWidth - 2`
