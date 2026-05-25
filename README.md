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
- **Not a daemon supervisor.** `hera install` (launchd auto-start) is deferred.

## Getting started

```sh
./setup.sh
```

That's the whole install. The script builds the binary, copies it to `~/bin/hera`, creates `~/.hera/` (mode 0700), and mints a scope token via `argus token mint --scope hera` (saved to `~/.hera/api-token`, mode 0600). It's idempotent — safe to re-run any time.

Then start hera in the foreground:

```sh
hera start --foreground
```

Keep that terminal open. From any argus task with MCP access, bootstrap an orchestrator:

```
hera_new_orchestrator(cwd=$PWD, name="my-project", coordinator_role_name="coord", mission="...")
```

Inspect state any time:

```sh
hera status   # daemon + orchestrator counts
hera list     # orchestrators + roles + live binding state
```

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
```

## License

See LICENSE (TBD).
