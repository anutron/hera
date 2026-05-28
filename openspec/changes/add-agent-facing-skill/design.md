## Context

Hera's tools surface to a sandboxed Claude session as `mcp__argus__hera_*` with terse one-line descriptions auto-generated from argus's REST registration. That is the agent's entire view. The richer design intent — the role-as-identity model, the idle-gated message bus, auto-adopted workers, multi-binding — lives in the README and `openspec/`, which a sandboxed agent never reads.

The sibling plugin `plannotator-argus` solved the parallel problem with a PreToolUse Bash guard that redirects `Bash(plannotator …)` to the MCP tools. That guard exists specifically because `plannotator annotate` writes a session file that EPERMs inside the sandbox. Hera has no such failing Bash verb, so the primary mechanism here is a **skill** (agent-facing surface) plus an optional **snippet** (always-loaded pointer), both gated at runtime to argus sandboxes.

## Goals / Non-Goals

**Goals:**

- A fresh sandboxed Claude session can answer "how do I coordinate other agents / message another role here" by reaching for the `hera` skill with no extra prompting.
- The skill and snippet are inert outside argus sandboxes (runtime gate), so they don't pollute unrelated sessions.
- `install-claude-skills.sh` installs the skill into `~/.claude/skills/` idempotently and offers to wire the snippet, for any developer — not scoped to one user; `uninstall-claude-skills.sh` cleanly reverses it.
- The snippet does not duplicate the skill, so there is nothing to drift.

**Non-Goals:**

- No modification of argus core to assemble context at worktree-create time.
- No top-level `CLAUDE.md` for agents in this repo (that file is for plugin developers).
- No PreToolUse Bash guard for hera (no destructive/EPERM-ing Bash verb to intercept; decided during brainstorm).
- No new Go/daemon/MCP behavior, and no change to `setup.sh` (assets install through dedicated scripts).

## Decisions

### Skill is the primary surface; snippet is a thin pointer

The skill carries all depth (model, tools, decision rules, workflows). The snippet is a ~6-line pointer: gate first, then "inside argus, consult the `hera` skill for multi-agent coordination; don't hand-roll it or use the legacy `ccc orchestrator` CLI."

- **Why over a full duplicate snippet:** a content-bearing snippet would duplicate the skill and drift. A pointer keeps the skill as the single source of truth while still giving users an always-loaded option.
- **Why a snippet at all:** the skill only loads when the model decides to reach for it. The snippet guarantees orientation is in context for users who want it always-on.

### Argus-awareness gate on both artifacts

Gate condition: active iff `ARGUS_TASK_ID` is set **or** `$PWD` is under `~/.argus/worktrees/`. The skill's `description` leads with this so the model self-selects. The snippet's first content line states the inverse ("if neither holds, ignore this section"). When the gate fails, the skill explicitly says: *"Not in an argus sandbox — these MCP tools are not registered here. Use the `hera` CLI directly instead."*

- **Why both env var and cwd:** either alone can be absent (cwd check covers sessions where the env var isn't exported; env var covers non-standard worktree roots). OR-ing them is the most permissive correct gate.

### Distinguish hera from the legacy `ccc orchestrator` system

The skills `orchestrate` / `orchestrator` / `ask-orchestrator` / `check-messages` belong to an older `ccc orchestrator` system (inbox.jsonl files, session-topic identity, clipboard handoffs) — not hera. The skill's "common mistakes" section names this explicitly so the agent uses hera's MCP tools rather than shelling out to `ccc orchestrator`.

### Dedicated install/uninstall scripts, separate from setup.sh

The agent-facing assets install through `install-claude-skills.sh` and `uninstall-claude-skills.sh`, **not** through `setup.sh`. `setup.sh` stays the daemon installer (build, binary, token, LaunchAgent); the Claude assets are a distinct concern with their own lifecycle.

- **Why separate scripts over folding into setup.sh:** decided mid-implementation. `setup.sh` requires argus + go and mints a token; the Claude assets need none of that and a user may want them without (re)running the daemon installer. Separate scripts also make uninstall clean and let the two install steps be independently prompted.
- **Generic over the repo, not hera-hardcoded:** the scripts loop over every `claude/skills/*/` directory and every `claude/snippets/*.md` file, so the repo can add more assets later with no script change.
- **Two Y/n steps each:** skill-symlink and snippet-append are each their own prompt (per user request). `--yes` accepts both.
- **Skill symlink:** `ln -s <repo>/claude/skills/<name> ~/.claude/skills/<name>`. Idempotent: already-current link → "already current" and skip; a non-symlink or foreign-target symlink → warn and skip, never clobber.
- **Snippet append:** append each snippet between per-snippet managed markers (`# >>> claude-snippet:<name> >>>` … `# <<< claude-snippet:<name> <<<`) in `~/.claude/CLAUDE.md`. Re-run replaces each managed block in place (idempotent, no duplication). If `~/.claude/CLAUDE.md` is a symlink (likely compiled output), warn before writing. On decline, print the snippet paths.
- **Uninstall safety:** a skill symlink is removed only when it points back at this repo; foreign symlinks and real directories are left alone. Snippet blocks are stripped by marker, preserving the rest of `~/.claude/CLAUDE.md`.
- **Why append-with-markers over symlinking a snippet file:** markers make each block idempotently replaceable and removable, and work for users with no snippet pipeline. The symlink-warning protects pipeline users from a footgun.

## Risks / Trade-offs

- **Appending to a symlinked `~/.claude/CLAUDE.md` clobbers compiled output** → install-claude-skills.sh detects the symlink and warns before writing; the step is opt-in (Y/n), so the user can decline and wire it through their pipeline.
- **Skill description gate is advisory, not enforced** → if the model loads the skill outside argus anyway, the gate is restated as the skill's first body section and tells the agent to stop. Low blast radius (read-only orientation text).
- **Tool descriptions drift from the skill** → the skill rewrites tool descriptions for agent-facing clarity rather than copying them verbatim, and the snippet points at the skill rather than restating, minimizing surfaces that can drift.

## Migration Plan

Additive. New files only (`claude/skills/hera/SKILL.md`, `claude/snippets/hera.md`, `install-claude-skills.sh`, `uninstall-claude-skills.sh`); `setup.sh` is untouched. Rollback: `./uninstall-claude-skills.sh` (removes repo-owned skill symlinks and strips the managed snippet blocks). No data or schema involved.

## Acceptance criteria

**Skill gate & content (`hera-agent-skill`):**

- it should be skipped when neither `ARGUS_TASK_ID` is set nor `$PWD` is under `~/.argus/worktrees/`, stating the tools are not registered and to use the CLI directly
- it should lead its `description` frontmatter with the argus-awareness gate
- it should document each of the six `mcp__argus__hera_*` tools with a when-to-call line
- it should give decision rules for claim-vs-attach, when `orchestrator=` is required, the coordinator `to:` rule, and the cross-orchestrator prohibition
- it should describe composition with iris and plannotator-argus
- it should list the common Bash/skill mistakes including the legacy `ccc orchestrator` confusion
- it should include at least three worked workflows (orchestrator, worker intake, freelance attach)

**Snippet (`hera-agent-skill`):**

- it should have the argus-awareness gate as its first content line
- it should point at the `hera` skill rather than duplicating its content
- it should carry `tags` and `audience` frontmatter

**install-claude-skills.sh (`hera-agent-skill`):**

- it should symlink each `claude/skills/*` into `~/.claude/skills/<name>` behind a Y/n prompt
- it should report "already current" and make no change when a skill symlink already points at the repo path
- it should warn and skip rather than clobber when a non-symlink occupies a skill path
- it should offer the snippet append behind a separate Y/n prompt and accept both under `--yes`
- it should append each snippet between per-snippet managed markers and replace the block in place on re-run rather than duplicate it
- it should preserve unrelated `~/.claude/CLAUDE.md` content and warn when it is a symlink before appending
- it should print the snippet paths when the user declines the snippet step

**uninstall-claude-skills.sh (`hera-agent-skill`):**

- it should remove each repo-owned skill symlink behind a Y/n prompt
- it should leave foreign symlinks and real directories untouched
- it should strip each per-snippet managed block while preserving the rest of `~/.claude/CLAUDE.md`
- it should exit 0 and report nothing to remove when run with nothing installed
