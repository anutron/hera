# hera-agent-skill Specification

## ADDED Requirements

### Requirement: Agent-facing hera skill exists and self-gates to argus sandboxes

The repo SHALL ship a skill at `claude/skills/hera/SKILL.md` with `name` and `description` frontmatter. The `description` MUST lead with the argus-awareness gate so the model only reaches for the skill inside argus sandboxes.

The argus-awareness gate is satisfied when `ARGUS_TASK_ID` is set OR the current working directory is under `~/.argus/worktrees/`. When neither holds, the skill MUST state that it is not in an argus sandbox, that the `mcp__argus__hera_*` tools are not registered there, and that the `hera` CLI should be used directly instead.

#### Scenario: Skill is inert outside an argus sandbox

- **WHEN** the skill is consulted in a session where `ARGUS_TASK_ID` is unset and the cwd is not under `~/.argus/worktrees/`
- **THEN** the skill SHALL state that the session is not in an argus sandbox and the `mcp__argus__hera_*` tools are not registered
- **AND** SHALL direct the agent to use the `hera` CLI directly instead of the MCP tools

#### Scenario: Skill description leads with the gate

- **WHEN** the skill frontmatter `description` is read
- **THEN** it SHALL lead with the argus-awareness condition (argus sandbox / `~/.argus/worktrees/`) before describing capability

### Requirement: Skill documents the hera tool surface

The skill SHALL document each of the six `mcp__argus__hera_*` tools — `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status` — with an agent-facing line describing when to call it, not merely what it does.

#### Scenario: Every hera tool is covered

- **WHEN** the skill body is read
- **THEN** each of `hera_new_orchestrator`, `hera_join`, `hera_send`, `hera_inbox`, `hera_mark_read`, and `hera_status` SHALL appear with a one-line when-to-call description

### Requirement: Skill gives when-to-use decision rules

The skill SHALL provide decision rules covering: `hera_join` claim mode vs attach mode; when the `orchestrator` parameter is required (a task holding two or more live bindings); the rule that a coordinator sender MUST supply an explicit `to`; and the prohibition on cross-orchestrator messaging.

#### Scenario: Decision rules are present

- **WHEN** the skill body is read
- **THEN** it SHALL distinguish claim mode from attach mode for `hera_join`
- **AND** SHALL state that `orchestrator` is required when the calling task holds two or more live bindings
- **AND** SHALL state that a coordinator must supply an explicit `to` when sending
- **AND** SHALL state that messaging a role in a different orchestrator is forbidden

### Requirement: Skill describes composition with sibling plugins

The skill SHALL describe how hera composes with sibling argus plugins — iris (host-side git/GitHub operations) and plannotator-argus (review UI) — and make clear that hera covers identity, messaging, and coordination while those plugins are reached through their own MCP tools.

#### Scenario: Sibling composition is documented

- **WHEN** the skill body is read
- **THEN** it SHALL describe the seam between hera and iris and between hera and plannotator-argus

### Requirement: Skill lists common Bash and skill mistakes

The skill SHALL list common mistakes a sandboxed agent might make instead of using hera's tools — including that hera messaging has no `Bash`/CLI verb (it is MCP-only), that the legacy `ccc orchestrator` CLI and `/ask-orchestrator`-style skills are a different system, and that every tool requires `cwd=$PWD`.

#### Scenario: Common mistakes are enumerated

- **WHEN** the skill body is read
- **THEN** it SHALL state that hera messaging is MCP-only with no `Bash`/CLI equivalent
- **AND** SHALL distinguish hera from the legacy `ccc orchestrator` system
- **AND** SHALL note that every hera tool requires `cwd=$PWD`

### Requirement: Skill includes worked workflows

The skill SHALL include at least three worked workflows expressed as ordered hera tool calls, covering becoming an orchestrator, worker intake, and attaching as a freelance helper.

#### Scenario: Three workflows are present

- **WHEN** the skill body is read
- **THEN** it SHALL contain at least three worked workflows, including an orchestrator-bootstrap flow, a worker-intake flow, and a freelance-attach flow

### Requirement: Pointer snippet exists and gates first

The repo SHALL ship a snippet at `claude/snippets/hera.md` whose first content line is the argus-awareness gate. The snippet MUST point the agent at the `hera` skill rather than duplicating the skill's content, and MUST carry `tags` and `audience` frontmatter so it slots into a compile-pipeline snippets directory as well as a plain CLAUDE.md append.

#### Scenario: Snippet gate is the first content line

- **WHEN** the snippet is read
- **THEN** its first content line (after frontmatter and the section heading) SHALL state that the section is to be ignored when `ARGUS_TASK_ID` is unset and the cwd is not under `~/.argus/worktrees/`

#### Scenario: Snippet points at the skill rather than duplicating it

- **WHEN** the snippet body is read
- **THEN** it SHALL direct the agent to consult the `hera` skill for the tool surface and workflows
- **AND** SHALL NOT restate the per-tool documentation that lives in the skill

#### Scenario: Snippet carries compile-pipeline frontmatter

- **WHEN** the snippet frontmatter is read
- **THEN** it SHALL contain `tags` and `audience` fields
