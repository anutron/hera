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

## Building

```sh
make build         # produces ./bin/hera
make test          # runs the full test suite
make install-dev   # copies ./bin/hera to ~/bin/
```

## Running

1. Mint a scope token from argus (one time):

   ```sh
   argus token mint --scope hera > ~/.hera/api-token
   chmod 600 ~/.hera/api-token
   ```

2. Start hera in the foreground (for development):

   ```sh
   hera start --foreground
   ```

   For background mode (no foreground flag), hera writes its PID to `~/.hera/hera.pid`.

3. Verify:

   ```sh
   hera status
   ```

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
