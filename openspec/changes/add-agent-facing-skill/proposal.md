## Why

When a Claude session starts inside an argus sandbox, the agent sees a list of `mcp__argus__hera_*` tool names and one-line descriptions — and nothing else. It does not know it is in a sandbox, when to reach for hera vs. plain `Bash`, how hera composes with sibling plugins, or the workflows hera was built for. Hera's design intent lives in the README and OpenSpec, which a sandboxed agent never reads.

The fix is to ship installable, agent-facing discoverability artifacts in this repo and gate them at runtime so they only activate inside argus sandboxes — rather than extending argus core to inject context at worktree-create time.

## What Changes

- **New `hera` skill** at `claude/skills/hera/SKILL.md` — agent-facing orientation for the six `mcp__argus__hera_*` tools. Its `description` leads with the argus-awareness gate so the model only reaches for it inside sandboxes. The body covers what hera is, the role/binding model, every tool, when-to-use decision rules, composition with sibling plugins, common Bash/skill mistakes, and three worked workflows.
- **New pointer snippet** at `claude/snippets/hera.md` — an optional always-loaded orientation whose first content line is the gate. It does not duplicate the skill; it points the agent at the `hera` skill. Carries `tags`/`audience` frontmatter so it slots into a compile-pipeline snippets dir as well as a plain CLAUDE.md append.
- **New `install-claude-skills.sh`** — a standalone installer, separate from `setup.sh` (which stays the daemon installer). Two opt-in steps, each behind a Y/n prompt: (1) symlink every skill under `claude/skills/*` into `~/.claude/skills/<name>`; (2) append every snippet under `claude/snippets/*.md` into `~/.claude/CLAUDE.md` between per-snippet managed markers. Idempotent, `--yes` supported. Detects and skips already-correct symlinks, never clobbers a non-symlink, replaces the managed block on re-run rather than duplicating, and warns when `~/.claude/CLAUDE.md` is a symlink (compiled output).
- **New `uninstall-claude-skills.sh`** — the inverse, also two Y/n steps: remove each skill symlink **only when it points back at this repo** (never touches a real dir or foreign symlink); strip each per-snippet managed block from `~/.claude/CLAUDE.md`, leaving the rest of the file intact. Idempotent, `--yes` supported.
- **`setup.sh` opt-in delegation** — after its daemon-install stages, `setup.sh` offers a single Y/n prompt to run `install-claude-skills.sh` (passing `--yes` under non-interactive mode). The asset-install logic stays in the dedicated script; `setup.sh` only offers the convenience, mirroring how the sibling `iris` installer wires the same step. Declining changes nothing.

## Capabilities

### New Capabilities

- `hera-agent-skill`: the agent-facing skill and snippet artifacts, their argus-awareness gate, the content they must cover, and the install/uninstall scripts that wire them into `~/.claude/`.

## Impact

- New files: `claude/skills/hera/SKILL.md`, `claude/snippets/hera.md`, `install-claude-skills.sh`, `uninstall-claude-skills.sh`.
- `setup.sh` gains an opt-in step at the end that delegates to `install-claude-skills.sh`; the asset-install logic itself stays in the dedicated scripts.
- No Go code, daemon, or MCP tool changes. No schema migration.
- No change to argus core — discoverability ships in this repo and is gated at runtime.
