# hera

Coordinator/overlay daemon for [argus](https://github.com/drn/argus). Provides role-as-identity coordination over argus's plugin substrate: roles persist across argus task lifetimes, messages flow between roles via an idle-gated injection bus, and worker tasks spawned by a coordinator are auto-adopted into the orchestrator graph.

**Status:** v1 in development. The OpenSpec change folder at `openspec/changes/hera-v1/` is the source of truth for the current design and implementation plan.

## What hera does

- **Roles outlive tasks.** Argus tasks come and go (worktrees get archived, branches merge); hera roles persist with their decisions, messages, and status intact.
- **Message bus with auto-delivery.** `hera_send` injects messages directly into the recipient's PTY – with `\n` (auto-submit) when the recipient is idle, without `\n` (user submits) when the recipient is busy.
- **Auto-adopted worker tasks.** Coordinators spawn workers via argus's existing `task_create`; hera watches the event stream and adopts new tasks into the orchestrator graph automatically.
- **Cross-repo orchestration.** Roles can live in different argus projects; the orchestrator binds them logically without forcing co-location.

## What hera is not (in v1)

- **Not a chat surface for the user.** Users talk to coordinator agents; coordinators talk to hera via MCP tools.
- **Not a plugin view yet.** The embedded-terminal split view is deferred to a follow-up change.
- **Not a `hera install` CLI subcommand.** Daemon lifecycle is driven by `setup.sh` (which can install a per-user macOS LaunchAgent — see below). Linux/systemd is deferred.

## Getting started

```sh
./setup.sh
```

That's the whole install. The script:

1. Builds the binary
2. Copies it to `~/bin/hera`
3. Creates `~/.hera/` (mode 0700)
4. Mints a scope token via `argus token mint --scope hera` (saved to `~/.hera/api-token`, mode 0600)
5. **(macOS only)** Offers to install a per-user LaunchAgent at `~/Library/LaunchAgents/com.anutron.hera.plist` so hera runs at login and auto-restarts on crash. Decline to keep the manual `hera start --foreground` flow.

It's idempotent — safe to re-run any time. To remove just the LaunchAgent without touching anything else:

```sh
./setup.sh --uninstall-launchagent
```

If you accepted the LaunchAgent prompt, hera is already running. Otherwise, start it in the foreground:

```sh
hera start --foreground
```

Keep that terminal open (foreground mode). From any argus task with MCP access, bootstrap an orchestrator:

```
hera_new_orchestrator(cwd=$PWD, name="my-project", coordinator_role_name="coord", mission="...")
```

Inspect state any time:

```sh
hera status   # daemon + orchestrator counts
hera list     # orchestrators + roles + live binding state
```

## Agent-facing discoverability (the hera skill)

A Claude session running inside an argus sandbox sees only the `mcp__argus__hera_*` tool names — not when to use them, how they compose with sibling plugins, or the workflows hera was built for. This repo ships that orientation as installable Claude assets, gated at runtime so they're inert outside argus sandboxes:

- `claude/skills/hera/SKILL.md` — the agent-facing skill (tool surface, decision rules, sibling composition, worked workflows). Its description leads with the argus-awareness gate so the model only reaches for it in-sandbox.
- `claude/snippets/hera.md` — an optional always-loaded pointer snippet that directs the agent to the skill.

Install them (separate from `setup.sh`, which is the daemon installer):

```sh
./install-claude-skills.sh        # prompts (Y/n) for the skill symlink, then again for the snippet
./install-claude-skills.sh --yes  # accept both without prompting
```

This symlinks each skill under `claude/skills/*` into `~/.claude/skills/<name>` and (optionally) appends each snippet under `claude/snippets/*.md` into `~/.claude/CLAUDE.md` between managed markers. It's idempotent — re-runs report what's already current and replace snippet blocks in place rather than duplicating them. Remove everything later:

```sh
./uninstall-claude-skills.sh      # removes repo-owned skill symlinks + strips the snippet blocks
```

Uninstall only removes a `~/.claude/skills/<name>` symlink when it points back at this repo, and strips snippet blocks by their markers — your own CLAUDE.md content is left intact.

## Building from source (without setup.sh)

```sh
make build         # produces ./bin/hera
make test          # runs the full test suite with -race
make install-dev   # copies ./bin/hera to ~/bin/
```

Then create `~/.hera/`, mint a token (`argus token mint --scope hera | awk '/^token:/ {print $2}' > ~/.hera/api-token`), and `chmod 600` it. `setup.sh` does all of this for you.

## Repository layout

```
cmd/hera/         # Cobra CLI verbs (main + start + stop + status + list + resume)
internal/
  config/           # config loading + token reading
  db/               # SQLite schema, migrations, DAOs
  argus/            # typed HTTP client for argus REST API
  events/           # SSE subscriber + dispatcher + auto-adopt
  mcp/              # MCP tool registration + callback HTTP listener + handlers
  inject/           # message injection (idle gate, formatting)
  idle/             # session.idle tracking
  daemon/           # main loop wiring everything together
  log/              # structured logging helpers
openspec/           # OpenSpec specs + change folders (source of truth for design)
claude/
  skills/hera/       # agent-facing skill installed into ~/.claude/skills
  snippets/          # optional always-loaded CLAUDE.md pointer snippet(s)
install-claude-skills.sh    # install the agent-facing assets (skill + snippet)
uninstall-claude-skills.sh  # reverse the above
```

## License

See LICENSE (TBD).
