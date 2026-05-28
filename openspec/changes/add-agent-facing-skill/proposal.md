## Why

When a Claude session starts inside an argus sandbox, the agent sees a list of `mcp__argus__hera_*` tool names and one-line descriptions — and nothing else. It does not know it is in a sandbox, when to reach for hera vs. plain `Bash`, how hera composes with sibling plugins (iris, plannotator-argus), or the workflows hera was built for. Hera's design intent lives in the README and OpenSpec, which a sandboxed agent never reads.

The fix is to ship installable, agent-facing discoverability artifacts in this repo and gate them at runtime so they only activate inside argus sandboxes — rather than extending argus core to inject context at worktree-create time.

## What Changes

- **New `hera` skill** at `claude/skills/hera/SKILL.md` — agent-facing orientation for the six `mcp__argus__hera_*` tools. Its `description` leads with the argus-awareness gate so the model only reaches for it inside sandboxes. The body covers what hera is, the role/binding model, every tool, when-to-use decision rules, composition with sibling plugins, common Bash/skill mistakes, and three worked workflows.
- **New pointer snippet** at `claude/snippets/hera.md` — an optional always-loaded orientation whose first content line is the gate. It does not duplicate the skill; it points the agent at the `hera` skill and warns off the legacy `ccc orchestrator` path. Carries `tags`/`audience` frontmatter so it slots into a compile-pipeline snippets dir as well as a plain CLAUDE.md append.
- **`setup.sh` gains a skill-install step** — idempotently symlinks `claude/skills/hera` into `~/.claude/skills/hera`, detecting and skipping an already-correct symlink and reporting what changed.
- **`setup.sh` gains an opt-in snippet-wiring step** — offers (Y/n) to append the snippet between managed markers in `~/.claude/CLAUDE.md`, idempotently replacing the managed block on re-run rather than duplicating it. Warns if `~/.claude/CLAUDE.md` is a symlink (compiled output) so a compile pipeline is not silently clobbered. On decline, prints the snippet path for manual wiring.

## Capabilities

### New Capabilities

- `hera-agent-skill`: the agent-facing skill and snippet artifacts, their argus-awareness gate, and the content they must cover.

### Modified Capabilities

- `hera-install`: `setup.sh` gains the idempotent skill-symlink step and the opt-in snippet-append step.

## Impact

- New files: `claude/skills/hera/SKILL.md`, `claude/snippets/hera.md`.
- Modified: `setup.sh` (two new steps; step numbering in user-facing output updates).
- No Go code, daemon, or MCP tool changes. No schema migration.
- No change to argus core — discoverability ships in this repo and is gated at runtime.
