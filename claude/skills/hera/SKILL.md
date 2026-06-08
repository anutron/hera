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

You don't manage any of that plumbing. You call nine MCP tools.

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

All nine tools require `cwd`. The `orchestrator` parameter is optional with one binding and
**required** when this task holds 2+ live bindings.

- **`hera_new_orchestrator(cwd, name, coordinator_role_name, [prompt], [constraints])`** — "I am
  the coordinator." Bootstraps a brand-new orchestrator, creates its coordinator role, and binds
  this task to it. The canonical "be an orchestrator" entry point.
- **`hera_join(cwd, …)`** — two modes:
  - *Claim mode* — `hera_join(cwd)` returns this task's single live binding (use right after a
    worker terminal opens to adopt the role the coordinator assigned). `hera_join(cwd, orchestrator=X)`
    when the task has 2+ bindings.
  - *Attach mode* — `hera_join(cwd, orchestrator, role_name, kind=worker|freelance, [prompt],
    [constraints], [status])` creates a **new** role under an existing orchestrator and binds this
    task to it. Use to join a team nobody spawned you into.
- **`hera_send(cwd, body, tldr, [to], [orchestrator], [in_reply_to])`** — message another role in
  the same orchestrator. `tldr` is **required** — a one-line summary ≤120 chars written by the
  sender (see TLDR discipline below). Worker/freelance senders may omit `to` (default-routes to
  the coordinator). Coordinators **must** supply `to`. Delivery is idle-gated automatically.
- **`hera_inbox(cwd, [orchestrator])`** — fetch all unread messages addressed to your role, oldest
  first. **Marks messages as read on fetch** — no separate `hera_mark_read` call needed for normal
  consumption. Call this whenever you receive a doorbell.
- **`hera_mark_read(cwd, message_ids, [orchestrator])`** — explicit mark-read for specific IDs
  (e.g. after reading via `hera_get_messages` instead of `hera_inbox`).
- **`hera_status(cwd, status, [orchestrator])`** — set your role status: `idle` | `working` |
  `blocked` | `done`. Mirrored to argus task metadata, so the coordinator sees it without asking.
- **`hera_spawn_worker(cwd, project, [prompt], [orchestrator])`** — spawn a new worker argus task
  under the caller's orchestrator. The worker is born-bound (no `hera_join` needed on the worker
  side). Caller must hold a live coordinator binding.
- **`hera_tree_updates(cwd, [orchestrator], [since])`** — scan the caller's orchestrator subtree
  for new messages since a cursor. Returns **TLDR-only subject lines** — no bodies. Cursor is
  stored per-role and auto-advances; pass `since` to override. Call periodically or when the human
  asks "what's the state of things?" — returns a compact view of all descendant activity without
  flooding context with full message bodies.
- **`hera_get_messages(cwd, ids, [orchestrator])`** — fetch full message bodies by ID list.
  Returns per-ID results; inaccessible or missing IDs get an `error` field rather than a top-level
  error. Use after scanning `hera_tree_updates` to drill into messages of interest.

## TLDR discipline

Every `hera_send` requires a `tldr` — a single line ≤120 chars summarising the message's intent.
Write it from the recipient's perspective: what do they need to know at a glance?

Good examples:

- `tldr="PR #47 open, tests green, needs review"`
- `tldr="Blocked on missing API key — need you to rotate it"`
- `tldr="Cart API done, branched off main, merging now"`

Bad examples (too vague, or multi-line):

- `tldr="update"` — says nothing
- `tldr="Done with the work you asked me to do"` — no specifics

The TLDR is:
- shown in the **doorbell** the recipient sees before they call `hera_inbox`
- returned by **`hera_tree_updates`** so parent coordinators can scan activity without reading bodies
- stored permanently alongside the message body

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
- **You're the coordinator and want to message a worker?** `hera_send(cwd, body, tldr="...", to="wave-1a")` —
  both `tldr` and `to` are mandatory for coordinators.
- **You received a doorbell?** Call `hera_inbox(cwd=$PWD)` immediately. Don't wait or try to reply
  before reading — the actual message content is in the inbox, not the doorbell.
- **You want to see what's happening across your whole team?** `hera_tree_updates(cwd=$PWD)` returns
  TLDR-only subject lines from all descendants since your last call. Then `hera_get_messages(ids=[…])`
  to drill into the ones that need attention.
- **Don't** try to message a role in a *different* orchestrator — cross-orchestrator messaging is
  forbidden; `to` always resolves within your own orchestrator.
- **Don't** use `hera_send` to talk to the human. The human reads the coordinator's own Claude
  pane; the bus is role-to-role only.
- **Finished?** `hera_status(done)` and send a closing note to the coordinator.

## Worker promotion: becoming a sub-coordinator

If a worker realizes mid-task that it needs another agent (cross-repo changes, parallelism, a longer-running sub-task), it can promote itself:

1. Call `hera_new_orchestrator(cwd=$PWD, name="...", coordinator_role_name="coord", prompt="...")` – this makes the current session a coordinator of a nested orchestrator while still being a worker in the parent (multi-binding covers both roles).
2. Use `hera_spawn_worker(project="TARGET-PROJECT", ...)` to dispatch workers in the right project for the sub-task.
3. Report the sub-orchestrator name back to the parent coord via `hera_send` so it can track progress.

A worker should always feel empowered to escalate this way rather than blocking or failing. The whole point of hera is to let agents solve problems – including problems that require other agents.

## Composition with sibling argus plugins

Hera owns **identity, messaging, and coordination** — and nothing else. Its siblings own the rest;
reach them through *their* MCP tools, not hera's.

- **plannotator-argus** (`mcp__argus__plannotator_*`) — review UI. A coordinator routes a worker's
  output to review with `plannotator_review`/`plannotator_annotate`; hera carries the *decision* and
  the *handoff message*, plannotator carries the *review surface*.
- **iris** (`mcp__argus__iris_*`) — host-side git/gh. A worker does the coding and local commits in
  its worktree, then uses **iris** to push / open a PR / merge its branch back to the canonical repo.
  hera coordinates *who does what*; iris performs the *host-side landing*. Independent plugins — pick
  per op: iris when an action touches the host, hera when it's about roles or messaging.

These are orthogonal plugins. Hera does not wrap them and they do not wrap hera.

## Receiving messages and the doorbell

Hera delivers messages as lightweight pointers, not full bodies. When a role sends you a message,
you see something like this injected into your PTY:

```
[hera from wave-1a] msg #42 — PR #47 open, tests green, needs review
```

**When you see a `[hera from …]` line as a turn: call `hera_inbox(cwd=$PWD)` immediately.**
`hera_inbox` fetches the full body and marks the message as read in one call — no separate
`hera_mark_read` needed.

**Doorbell re-delivery:** if hera detects that an injected message was never read (within ~30s),
it fires a re-nudge directly into your PTY:

```
[hera doorbell] msg #42 — PR #47 open, tests green, needs review — call hera_inbox
```

or, when multiple messages are pending:

```
[hera doorbell] 3 unread messages — call hera_inbox
```

Same response: call `hera_inbox` immediately. Hera re-fires at ~30-second intervals up to 5 nudges
per message.

## Common mistakes (reach for the tool, not these)

- **`Bash(hera send …)` / `Bash(hera join …)`** — no such CLI verbs exist. The `hera` binary only
  has `start`/`stop`/`status`/`list`/`resume`. All coordination is **MCP-only**: `hera_send`,
  `hera_join`, etc.
- **Forgetting `cwd=$PWD`** — every hera tool needs it to resolve your role.
- **Omitting `orchestrator=` on a multi-binding task** — returns an ambiguity error. Pass it.

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
hera_send(cwd=$PWD, body="Cart API done, PR up, tests green.")   # default-routes to coord
hera_status(cwd=$PWD, status="done")
```

### 3. Attach as a freelance helper

```
hera_join(cwd=$PWD, orchestrator="checkout-rebuild", role_name="perf-helper",
          kind="freelance", prompt="Profile the hot path nobody owns")
hera_status(cwd=$PWD, status="working")
hera_send(cwd=$PWD, tldr="Found N+1 in cart serialization", body="Found an N+1 in cart serialization — want me to take it?")
```

### 4. Scan subtree for activity (root coordinator pattern)

```
# Get TLDR-only subject lines from all descendants since last check.
hera_tree_updates(cwd=$PWD)
# → returns list of {id, from_role, from_orchestrator, tldr, sent_at}

# Identify IDs worth reading in full, then drill in:
hera_get_messages(cwd=$PWD, ids=[42, 47, 51])
# → returns full bodies for those three messages

# Synthesise for the human: who's blocked, what needs attention.
```

### 5. Send a message with TLDR

```
hera_send(
    cwd=$PWD,
    to="wave-1a",
    tldr="Cart API spec clarification: use PATCH not PUT for partial updates",
    body="For the cart API endpoints, please use PATCH not PUT. PUT implies full replacement and the client doesn't always send the full state. Check the OpenAPI spec at docs/api/cart.yaml for the contract."
)
```
