# hera

Coordinator/overlay daemon for [argus](https://github.com/drn/argus). Provides role-as-identity coordination over argus's plugin substrate: roles persist across argus task lifetimes, messages flow between roles via an idle-gated injection bus, and worker tasks spawned by a coordinator are auto-adopted into the orchestrator graph.

**Status:** v1.0

## What hera does

- **Roles outlive tasks.** Argus tasks come and go (worktrees get archived, branches merge); hera roles persist with their decisions, messages, and status intact.
- **Message bus with auto-delivery.** `hera_send` injects messages directly into the recipient's PTY – with `\n` (auto-submit) when the recipient is idle, without `\n` (user submits) when the recipient is busy.
- **Auto-adopted worker tasks.** Coordinators spawn workers via argus's existing `task_create`; hera watches the event stream and adopts new tasks into the orchestrator graph automatically.
- **Cross-repo orchestration.** Roles can live in different argus projects; the orchestrator binds them logically without forcing co-location.
- **TUI operator view.** `Ctrl+H` inside argus opens hera-view: a live three-panel terminal showing your coordinator and agent PTYs side by side, with a navigable rail of all orchestrators, agents, and freelancers.

## hera-view

hera ships a built-in TUI launched from argus with **Ctrl+H**. It gives you a live view of all your coordinated agents without leaving argus.

### Layout

The view has three regions: a fixed-width left **rail** and two right panes. The right side adapts to what the rail has selected:

- **Coordinator mode** (a coordinator row selected): rail + **HERA** pane (coordinator PTY) + **Details** pane (live agent roster, activity, status)
- **Agent mode** (a worker row selected): rail + **HERA** pane (that agent's coordinator PTY) + **AGENT** pane (the agent's own PTY)
- **Freelance mode** (an unmanaged argus task selected): rail + full-width **AGENT** pane

The rail lists active orchestrators with their agents nested below each one. Sub-coordinators sort before leaf workers. Freelancers – argus tasks hera has never bound to a role – appear below all project rows in a "Freelance" section grouped by repo. Archived items collect in an "Archive" section at the bottom of the rail (hidden by default; `l` reveals them).

### Focus and navigation

Focus starts in RAIL on open. Move between regions with the focus ladder or stay in a pane to type directly into the agent's terminal.

| Key | Where | What |
|-----|-------|------|
| `j` / `k` | RAIL | Move selection up / down |
| `Enter` | RAIL | Enter the selection's primary pane (coordinator → HERA, agent or freelancer → AGENT) |
| `Ctrl-→` | any | Advance focus ladder (RAIL → COORD → AGENT) |
| `Ctrl-←` | any | Retreat focus ladder (AGENT → COORD → RAIL) |
| `Ctrl-Q` | COORD or AGENT | Return focus to RAIL |
| `Ctrl-Z` | COORD or AGENT | Toggle pane fullscreen |
| `Shift-↑` / `Shift-↓` | COORD or AGENT | Scroll pane scrollback |
| `Cmd/Ctrl-↑` / `Cmd/Ctrl-↓` | COORD or AGENT | Flip rail selection while staying in the pane |
| `Esc` | RAIL | Hand keyboard back to argus |

### Rail mutation keys (RAIL focus only)

When focus is inside a pane, all keystrokes are forwarded verbatim to the bound task's PTY. The keys below only fire when focus is RAIL; in a pane they reach the agent as ordinary bytes.

| Key | What |
|-----|------|
| `n` | New coordinator – modal with Name / Project / Branch / Backend / Prompt (prompt auto-runs on launch) |
| `w` | Spawn worker under the selected coordinator |
| `J` | Adopt a freelancer into a coordinator |
| `r` | Rename the selected row |
| `a` | Archive / unarchive (reversible, no confirmation needed) |
| `P` | Pin / unpin |
| `s` / `S` | Advance / revert the selected row's argus task status |
| `/` | Search / filter the rail |
| `Space` | Fold / unfold a coordinator or Archive section |
| `l` | Toggle visibility of the Archive section |
| `^d` | Delete the selection, removing its worktree and branch (shows confirmation) |
| `^r` | Prune all done coords and agents (shows confirmation) |
| `^p` | Open a GitHub PR for the selected row's worktree |
| `?` | Open the argus help overlay with the full keyset |

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
hera_new_orchestrator(cwd=$PWD, name="my-project", coordinator_role_name="coord", prompt="...")
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
  settings/         # operator settings section (idle_debounce, auto_inject knobs)
  view/             # hera-view TUI (WebSocket plugin view, rail, PTY panes, keyset)
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
