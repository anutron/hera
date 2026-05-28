**Design doc:** `openspec/changes/add-agent-facing-skill/design.md`

## 1. Verification approach

The install/uninstall scripts are verified manually against a temp `HOME` (matches how the existing daemon installer scenarios are checked — no automated installer suite in this repo). No bats harness.

- [x] 1.1 Confirm the manual verification matrix up front (Stage 6 executes it): skill symlink first-install / already-current / non-symlink-skip; snippet append / marker-replace-on-rerun / symlink-warning / decline-prints-path; uninstall removes-repo-owned / leaves-foreign / strips-block-preserving-rest / idempotent.

## 2. Author the hera skill

**Depends on:** Stage 1

- [x] 2.1 Write `claude/skills/hera/SKILL.md` frontmatter — `name: hera`, `description` leading with the argus-awareness gate.
- [x] 2.2 Write the gate section (inert outside argus → "use the `hera` CLI directly"), the "what hera is" paragraph, and the role/binding model (orchestrator, coordinator, worker, freelance, binding).
- [x] 2.3 Write the tool surface — all six `mcp__argus__hera_*` tools, each with a when-to-call line and key params (`cwd`, when `orchestrator` is needed).
- [x] 2.4 Write the decision rules (claim vs attach; `orchestrator=` required at 2+ bindings; coordinator `to:` rule; cross-orchestrator prohibition; don't message the human via the bus).
- [x] 2.5 Write the sibling composition section (plannotator-argus = review UI) and the common-mistakes section (MCP-only messaging, missing `cwd=$PWD`).
- [x] 2.6 Write the three worked workflows (orchestrator bootstrap; worker intake; freelance attach) as ordered tool calls.

## 3. Author the pointer snippet

**Depends on:** Stage 1

- [x] 3.1 Write `claude/snippets/hera.md` with `tags: [argus]` / `audience: [shared]` frontmatter, the gate as the first content line, and a pointer to the `hera` skill (no per-tool duplication).

## 4. Write install-claude-skills.sh

**Depends on:** Stage 1

- [x] 4.1 Skill step (Y/n; auto under `--yes`): loop over `claude/skills/*/` and symlink each into `~/.claude/skills/<name>`; report already-current when correct; warn-and-skip when a non-symlink or foreign-target symlink occupies the path. Cross-platform.
- [x] 4.2 Snippet step (separate Y/n; auto under `--yes`): loop over `claude/snippets/*.md` and append each between per-snippet managed markers in `~/.claude/CLAUDE.md`, replacing the block in place on re-run; warn when CLAUDE.md is a symlink; print snippet paths on decline.

## 5. Write uninstall-claude-skills.sh

**Depends on:** Stage 1

- [x] 5.1 Skill step (Y/n): remove each `~/.claude/skills/<name>` only when it is a symlink pointing back at this repo; leave foreign symlinks and real dirs untouched.
- [x] 5.2 Snippet step (Y/n): strip each per-snippet managed block from `~/.claude/CLAUDE.md`, preserving the rest; idempotent, exit 0 with nothing to remove.

## 6. Verify end-to-end

**Depends on:** Stage 2, Stage 3, Stage 4, Stage 5

- [x] 6.1 Execute the manual verification matrix from Stage 1 against a temp `HOME` (skill symlink: first-install/already-current/non-symlink-skip; snippet: append/marker-replace/symlink-warning/decline).
- [x] 6.2 Drive the REAL `install-claude-skills.sh` + `uninstall-claude-skills.sh` via `--yes` against a temp `HOME` (full install → idempotent re-run → uninstall → idempotent re-run, plus foreign-symlink and real-dir safety). NB: a real-`HOME` install should be run from a canonical hera checkout, not this ephemeral argus worktree (the symlink target would dangle once the worktree is cleaned).
- [ ] 6.3 Test-drive: open a fresh Claude session in an argus worktree and confirm the model reaches for the `hera` skill when asked how to coordinate other agents, without extra prompting.
