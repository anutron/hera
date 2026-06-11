## ADDED Requirements

### Requirement: Deadness is classified only from a fresh state-cache snapshot

A rail row SHALL be classified `Dead` (the argus task RECORD no longer exists) ONLY when the argus state cache is BOTH ready AND FRESH — a successful poll has landed recently. The readiness latch alone (one successful poll ever) SHALL NOT authorize the `Dead` classification once the snapshot has gone STALE.

The state cache snapshot goes STALE when polling stops succeeding while the previous snapshot is retained: argus bounced, hung, or is slow, so the poll errors and the cache keeps serving its last good snapshot. In that window a task created or changed AFTER the freeze is simply absent from the frozen snapshot even though argus's own state (e.g. its MCP `task_list`) still reports it live and non-archived. Classifying such a task `Dead` strands its row (and, for a sub-coordinator parent-link row, its whole child subtree).

While the cache is stale, a cache MISS SHALL be treated as "unknown", not "gone": the row MUST remain in the ACTIVE tree, driven by its live or most-recent binding, until a fresh successful poll can confirm the task's true state. The cache MUST tolerate a brief polling blip (a transient failure within the staleness window) without flipping stale, so momentary hiccups do not churn the rail. This strengthens — and does not loosen — the existing rule that a cold cache MUST NOT transiently classify rows dead, extending it from the cold (never-polled) case to the stale (was-ready, now-frozen) case.

The genuine record-gone case is unaffected: while polling keeps succeeding (the cache is fresh), a task truly absent from the snapshot (pruned / 404) SHALL still be classified `Dead` and bucket into its coordinator's Archive expando.

#### Scenario: Stale cache does not classify a live task dead

- **WHEN** a role's bound argus task is absent from the state cache, the cache is READY (it has polled successfully at least once) but STALE (polling has since stopped succeeding so the snapshot is frozen), and argus still reports the task live and non-archived
- **THEN** the row MUST NOT be classified `Dead` — it MUST remain in the active tree driven by its live / most-recent binding — until a fresh successful poll can confirm the task's true state

#### Scenario: Fresh cache still buckets a genuinely-gone task

- **WHEN** a role's bound argus task is absent from the state cache and the cache is READY and FRESH (polling is succeeding — the task was genuinely pruned / returns 404)
- **THEN** the row MUST be classified `Dead` and bucket into its coordinator's Archive expando, exactly as before

#### Scenario: A brief polling blip does not flip the cache stale

- **WHEN** a single poll fails within the staleness window after a recent successful poll
- **THEN** the cache MUST remain fresh, so a transient hiccup does not momentarily suppress deadness classification or churn the rail
