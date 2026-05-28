# hera-install Specification

## ADDED Requirements

### Requirement: setup.sh installs the hera skill into ~/.claude/skills

`setup.sh` SHALL include a step that symlinks the repo's `claude/skills/hera` directory into `~/.claude/skills/hera`, creating `~/.claude/skills/` if it does not exist. The step MUST be idempotent: when `~/.claude/skills/hera` already exists as a symlink pointing at the repo's `claude/skills/hera`, the step MUST report that it is already current and make no change.

When `~/.claude/skills/hera` already exists but is NOT a symlink to the repo path (a regular file, directory, or a symlink to a different target), the step MUST warn and skip rather than overwrite it.

This step SHALL run on all platforms (it is not gated to macOS) and MUST NOT cause setup.sh to exit non-zero on the skip paths.

#### Scenario: First-time skill install

- **WHEN** `./setup.sh --yes` runs and `~/.claude/skills/hera` does not exist
- **THEN** `~/.claude/skills/hera` SHALL be created as a symlink
- **AND** `readlink ~/.claude/skills/hera` SHALL return the absolute path to the repo's `claude/skills/hera`

#### Scenario: Re-run when the symlink is already current

- **WHEN** `~/.claude/skills/hera` already points at the repo's `claude/skills/hera` and `./setup.sh --yes` runs
- **THEN** the step SHALL report that the skill symlink is already current
- **AND** SHALL make no filesystem change

#### Scenario: Existing non-symlink is not clobbered

- **WHEN** `~/.claude/skills/hera` exists as a regular directory (or a symlink to a different target) and `./setup.sh --yes` runs
- **THEN** the step SHALL warn that the path is occupied and SHALL NOT overwrite it
- **AND** setup.sh SHALL continue and exit with status 0

### Requirement: setup.sh offers to wire the hera snippet into ~/.claude/CLAUDE.md

`setup.sh` SHALL include a step that offers to append the repo's `claude/snippets/hera.md` content into `~/.claude/CLAUDE.md` between managed markers. The step MUST prompt before writing in interactive mode and MUST accept the append without prompting under `--yes`/`-y`.

The appended content MUST be delimited by managed begin/end markers so that a re-run replaces the managed block in place rather than appending a duplicate. When `~/.claude/CLAUDE.md` is a symlink, the step MUST warn — before writing — that the file appears to be compiled output and an append may be overwritten on recompile. When the user declines, the step MUST print the absolute path to the snippet so it can be wired in manually.

#### Scenario: User accepts the snippet append

- **WHEN** `./setup.sh` reaches the snippet step and the user accepts the prompt
- **THEN** the content of `claude/snippets/hera.md` SHALL be written into `~/.claude/CLAUDE.md` between the managed begin/end markers

#### Scenario: Re-run replaces the managed block rather than duplicating

- **WHEN** the managed markers already exist in `~/.claude/CLAUDE.md` and `./setup.sh --yes` runs the snippet step again
- **THEN** the content between the managed markers SHALL be replaced in place
- **AND** there SHALL be exactly one managed block in `~/.claude/CLAUDE.md`

#### Scenario: Symlinked CLAUDE.md triggers a warning

- **WHEN** `~/.claude/CLAUDE.md` is a symlink and the snippet step is about to write
- **THEN** the step SHALL warn that the file appears to be compiled output and the append may be overwritten on recompile

#### Scenario: User declines the snippet append

- **WHEN** `./setup.sh` reaches the snippet step and the user declines the prompt
- **THEN** no change SHALL be made to `~/.claude/CLAUDE.md`
- **AND** the step SHALL print the absolute path to `claude/snippets/hera.md` for manual wiring
