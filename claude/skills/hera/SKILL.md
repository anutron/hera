---
name: hera
description: >-
  Inside an argus sandbox (cwd under ~/.argus/worktrees/ or ARGUS_TASK_ID set), coordinate
  multi-agent work via hera's mcp__argus__hera_* tools — bootstrap an orchestrator, claim or
  attach a worker/freelance role, and message other roles over the idle-gated bus. Use when you
  need to spawn and coordinate other agent sessions, run a large multi-session project, or
  message another role. NOT for non-argus sessions, where these MCP tools are not registered.
---

# Hera — multi-agent coordination inside argus

## Gate: are you in an argus sandbox?

This skill only applies inside an argus task sandbox. You are in one if **either** holds:

- `ARGUS_TASK_ID` is set, **or**
- the current working directory is under `~/.argus/worktrees/`.

**If neither holds, stop.** You are not in an argus sandbox — the `mcp__argus__hera_*` tools are
not registered in this session. Use the `hera` CLI directly instead (`hera status`, `hera list`),
or run the `hera` binary. Nothing below applies.

Every hera MCP tool takes `cwd` — always pass `cwd=$PWD`. That is how hera resolves which role
this session is.

## What hera is

Hera is a coordinator/overlay daemon that runs on top of argus. It gives multi-agent work
**role-as-identity**: a *role* (like `coord`, `wave-1a`, `reviewer`) persists with its mission,
status, and message history even as the underlying argus tasks come and go (worktrees archive,
branches merge). Messages flow between roles over a bus that injects directly into the
recipient's terminal — auto-submitting when the recipient is idle, waiting for the user to submit
when it's busy. Worker tasks a coordinator spawns through argus are auto-adopted into the
orchestrator graph.

You don't manage any of that plumbing. You call six MCP tools.

## The model in five terms

- **Orchestrator** — a named coordination graph (one per project/feature/wave). Roles live under it.
- **Coordinator role** — the orchestrator's own driver. Created by `hera_new_orchestrator`. Talks
  to the human in its own Claude pane; talks to other roles via `hera_send`.
- **Worker role** — does the actual work. Usually spawned by a coordinator (via argus `task_create`)
  and auto-adopted, then claimed by the worker session with `hera_join`.
- **Freelance role** — a helper that attaches itself to an existing orchestrator on its own
  initiative (no coordinator spawned it).
- **Binding** — the live link between *this argus task* and *one role*. A task normally holds one
  binding; with nested orchestration it can hold several (one per orchestrator). When it holds
  2+, you must say which one you mean with the `orchestrator` parameter.

## Tool surface

All six tools require `cwd`. The `orchestrator` parameter is optional with one binding and
**required** when this task holds 2+ live bindings.

- **`hera_new_orchestrator(cwd, name, coordinator_role_name, [mission], [constraints])`** — "I am
  the coordinator." Bootstraps a brand-new orchestrator, creates its coordinator role, and binds
  this task to it. The canonical "be an orchestrator" entry point.
- **`hera_join(cwd, …)`** — two modes:
  - *Claim mode* — `hera_join(cwd)` returns this task's single live binding (use right after a
    worker terminal opens to adopt the role the coordinator assigned). `hera_join(cwd, orchestrator=X)`
    when the task has 2+ bindings.
  - *Attach mode* — `hera_join(cwd, orchestrator, role_name, kind=worker|freelance, [mission],
    [constraints], [status])` creates a **new** role under an existing orchestrator and binds this
    task to it. Use to join a team nobody spawned you into.
- **`hera_send(cwd, body, [to], [orchestrator], [in_reply_to])`** — message another role in the
  same orchestrator. Worker/freelance senders may omit `to` (default-routes to the coordinator).
  Coordinators **must** supply `to`. Delivery is idle-gated automatically.
- **`hera_inbox(cwd, [orchestrator])`** — list unread messages addressed to your role, oldest first.
- **`hera_mark_read(cwd, message_ids, [orchestrator])`** — clear messages once handled.
- **`hera_status(cwd, status, [orchestrator])`** — set your role status: `idle` | `working` |
  `blocked` | `done`. Mirrored to argus task metadata, so the coordinator sees it without asking.

## When to use what

- **Starting a coordination effort?** `hera_new_orchestrator`. Don't `hera_join` first — there's
  nothing to join yet.
- **A coordinator spawned you (you opened a fresh worker terminal)?** `hera_join(cwd)` to claim the
  binding, read the mission, then `hera_status(working)`.
- **Joining a team that didn't spawn you?** `hera_join` in *attach mode* with an explicit `role_name`
  and `kind`.
- **This task holds 2+ bindings (nested orchestration)?** Every `hera_send` / `hera_inbox` /
  `hera_mark_read` / `hera_status` / claim-mode `hera_join` **must** pass `orchestrator=` to say
  which role you're acting as. Omitting it returns an ambiguity error listing your options.
- **You're the coordinator and want to message a worker?** `hera_send(cwd, body, to="wave-1a")` —
  `to` is mandatory for coordinators.
- **Don't** try to message a role in a *different* orchestrator — cross-orchestrator messaging is
  forbidden; `to` always resolves within your own orchestrator.
- **Don't** use `hera_send` to talk to the human. The human reads the coordinator's own Claude
  pane; the bus is role-to-role only.
- **Finished?** `hera_status(done)` and send a closing note to the coordinator.

## Composition with sibling argus plugins

Hera owns **identity, messaging, and coordination** — and nothing else. Its siblings own the rest;
reach them through *their* MCP tools, not hera's.

- **iris** (`mcp__argus__iris_*`) — host-side git/GitHub. When a worker finishes and needs to push,
  open a PR, or merge, that's iris (`iris_push`, `iris_gh_pr_create`, `iris_merge_to_master`), not
  hera and not raw `Bash(git push)` (which fails across the sandbox boundary). Pattern: hera decides
  *who ships what*; iris does the *git plumbing*.
- **plannotator-argus** (`mcp__argus__plannotator_*`) — review UI. A coordinator routes a worker's
  output to review with `plannotator_review`/`plannotator_annotate`; hera carries the *decision* and
  the *handoff message*, plannotator carries the *review surface*.

These are orthogonal plugins. Hera does not wrap them and they do not wrap hera.

## Common mistakes (reach for the tool, not these)

- **`Bash(hera send …)` / `Bash(hera join …)`** — no such CLI verbs exist. The `hera` binary only
  has `start`/`stop`/`status`/`list`/`resume`. All coordination is **MCP-only**: `hera_send`,
  `hera_join`, etc.
- **Using the legacy `ccc orchestrator` CLI or the `/orchestrate`, `/ask-orchestrator`,
  `/check-messages` skills** — that's a different, older system (inbox.jsonl files, session-topic
  identity, clipboard handoffs). Inside argus with hera installed, use the `mcp__argus__hera_*`
  tools instead.
- **Forgetting `cwd=$PWD`** — every hera tool needs it to resolve your role.
- **Omitting `orchestrator=` on a multi-binding task** — returns an ambiguity error. Pass it.
- **`Bash(git push)` / `Bash(gh pr create)` to ship a worker's branch** — use iris tools; raw git
  to a remote fails inside the sandbox.

## Worked workflows

### 1. Be an orchestrator

```
hera_new_orchestrator(cwd=$PWD, name="checkout-rebuild", coordinator_role_name="coord",
                      mission="Rebuild checkout across 3 parallel workers")
# → spawn workers with argus task_create (they auto-adopt into this orchestrator)
# → hand each a mission:
hera_send(cwd=$PWD, to="wave-1a", body="Your slice: the cart API. Branch off main. Ping me when green.")
# → watch for replies:
hera_inbox(cwd=$PWD)          # read what workers report
hera_mark_read(cwd=$PWD, message_ids=[12,13])
```

### 2. Worker intake (a coordinator spawned you)

```
hera_join(cwd=$PWD)                       # claim the binding the coordinator assigned
# → read the mission/handoff that comes back, then:
hera_status(cwd=$PWD, status="working")
# … do the work …
hera_send(cwd=$PWD, body="Cart API done, PR up via iris. Tests green.")   # default-routes to coord
hera_status(cwd=$PWD, status="done")
```

### 3. Attach as a freelance helper

```
hera_join(cwd=$PWD, orchestrator="checkout-rebuild", role_name="perf-helper",
          kind="freelance", mission="Profile the hot path nobody owns")
hera_status(cwd=$PWD, status="working")
hera_send(cwd=$PWD, body="Found an N+1 in cart serialization — want me to take it?")
```
