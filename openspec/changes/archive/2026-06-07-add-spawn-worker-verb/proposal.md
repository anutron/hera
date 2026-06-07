# Proposal: hera_spawn_worker MCP verb (born-bound workers)

## Problem

Today a coordinator spawns a worker via three awkward steps:

1. Call `mcp__argus__task_create` manually with a minimal prompt.
2. The worker self-calls `hera_join(kind=worker)` inside its own session.
3. The initial prompt often arrives unsubmitted (needs a manual Enter – BUG-030 regression risk).

This creates a transient window where the new task is an unbound "freelancer" visible in the rail, requires the coordinator to know argus internals, and duplicates plumbing that hera already owns.

## Solution

Add a 7th hera MCP verb `hera_spawn_worker` that performs all three steps atomically from the coordinator:

- Creates the argus task in the coordinator's project.
- Inserts a worker role + live binding pre-bound under the caller's orchestrator.
- Auto-submits the prompt via `PostTaskInput("\r")` (reusing the BUG-030 fix) so the worker starts immediately.

Returns the new task id and binding info. The v1 "six tools locked" decision is superseded for v1.x; this 7th verb is now sanctioned by Aaron.
