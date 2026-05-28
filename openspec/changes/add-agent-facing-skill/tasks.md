**Design doc:** `openspec/changes/add-agent-facing-skill/design.md`

## 1. Tests / verification harness

- [ ] 1.1 Write a bats (or shell) test for the setup.sh skill-symlink step: first-install creates the symlink, re-run reports "already current", a pre-existing non-symlink is warned-and-skipped (not clobbered). Tests run against a temp `HOME`.
- [ ] 1.2 Write a test for the setup.sh snippet step: `--yes` appends between markers, a second `--yes` replaces the block in place (exactly one managed block), a symlinked `~/.claude/CLAUDE.md` triggers the warning, decline prints the snippet path. Tests run against a temp `HOME`.
- [ ] 1.3 Write a structural test asserting the artifacts exist and gate correctly: `claude/skills/hera/SKILL.md` has `name`+`description` frontmatter whose description leads with the gate; `claude/snippets/hera.md`'s first content line is the gate and carries `tags`+`audience`. Confirm all tests fail before implementation (Prove-It).

## 2. Author the hera skill

**Depends on:** Stage 1

- [ ] 2.1 Write `claude/skills/hera/SKILL.md` frontmatter — `name: hera`, `description` leading with the argus-awareness gate.
- [ ] 2.2 Write the gate section (inert outside argus → "use the `hera` CLI directly"), the "what hera is" paragraph, and the role/binding model (orchestrator, coordinator, worker, freelance, binding).
- [ ] 2.3 Write the tool surface — all six `mcp__argus__hera_*` tools, each with a when-to-call line and key params (`cwd`, when `orchestrator` is needed).
- [ ] 2.4 Write the decision rules (claim vs attach; `orchestrator=` required at 2+ bindings; coordinator `to:` rule; cross-orchestrator prohibition; don't message the human via the bus).
- [ ] 2.5 Write the sibling composition section (iris = host git/GitHub; plannotator-argus = review UI) and the common-mistakes section (MCP-only messaging, legacy `ccc orchestrator` confusion, missing `cwd=$PWD`).
- [ ] 2.6 Write the three worked workflows (orchestrator bootstrap; worker intake; freelance attach) as ordered tool calls.

## 3. Author the pointer snippet

**Depends on:** Stage 1

- [ ] 3.1 Write `claude/snippets/hera.md` with `tags: [argus]` / `audience: [shared]` frontmatter, the gate as the first content line, and a pointer to the `hera` skill (no per-tool duplication; warn off the legacy `ccc orchestrator` path).

## 4. Extend setup.sh — skill symlink

**Depends on:** Stage 1

- [ ] 4.1 Add a step that `mkdir -p ~/.claude/skills` and symlinks `claude/skills/hera` → `~/.claude/skills/hera`; report already-current when correct; warn-and-skip when a non-symlink occupies the path. Runs on all platforms.
- [ ] 4.2 Update setup.sh step numbering / labels and the final summary so the new step is reflected in user-facing output.

## 5. Extend setup.sh — snippet wiring

**Depends on:** Stage 1

- [ ] 5.1 Add an opt-in step (Y/n; auto-yes under `--yes`) that appends `claude/snippets/hera.md` between managed markers in `~/.claude/CLAUDE.md`, replacing the managed block in place on re-run.
- [ ] 5.2 Warn before writing when `~/.claude/CLAUDE.md` is a symlink; on decline, print the snippet's absolute path.

## 6. Verify end-to-end

**Depends on:** Stage 2, Stage 3, Stage 4, Stage 5

- [ ] 6.1 Run the full test suite from Stage 1; confirm all pass.
- [ ] 6.2 Run `./setup.sh` locally against the real `HOME`; verify `~/.claude/skills/hera` resolves to the repo skill and the skill loads. Re-run to confirm idempotency.
- [ ] 6.3 Test-drive: open a fresh Claude session in an argus worktree and confirm the model reaches for the `hera` skill when asked how to coordinate other agents, without extra prompting.
