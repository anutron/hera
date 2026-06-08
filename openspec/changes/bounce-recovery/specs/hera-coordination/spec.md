## ADDED Requirements

### Requirement: Managed workers receive resume message after argus bounce

After argus bounces and hera's link recovery succeeds, hera SHALL automatically send a static resume message to every active managed worker (kind=worker) with a live binding under every active coordinator orchestrator. The message is sent as if from the coordinator role, directed to each qualifying worker role.

Resume message content:

- **Tldr**: `argus bounced — please resume`
- **Body**: `argus bounced — please resume any unfinished work and report back to your coordinator`

Worker selection criteria — a worker qualifies if ALL hold:

1. The orchestrator is active (not archived).
2. The role kind is `worker` (not `coordinator`, not `freelance`).
3. The role is active (not archived).
4. The role has a live binding (ended_at IS NULL).

#### Scenario: Active workers under a coordinator receive resume messages

- **WHEN** argus bounces AND link recovery succeeds AND an orchestrator has an active coordinator role and two active worker roles each with live bindings
- **THEN** hera MUST send a resume message with the static body to each of the two workers, from the coordinator role

#### Scenario: Freelancers excluded from resume sweep

- **WHEN** argus bounces AND link recovery succeeds AND an orchestrator has an active coordinator and an active freelance role with a live binding
- **THEN** hera MUST NOT send a resume message to the freelance role

#### Scenario: Workers without live bindings are skipped

- **WHEN** argus bounces AND link recovery succeeds AND a worker role has no live binding (ended_at is set)
- **THEN** hera MUST NOT send a resume message to that worker role

#### Scenario: Resume message delivery falls back to queued when argus notify fails

- **WHEN** hera delegates the resume message to argus via the notify endpoint AND the notify call fails (e.g. no active session)
- **THEN** hera MUST persist the message with `delivery_mode = queued_no_binding` so the worker picks it up via `hera_inbox` on reconnect

#### Scenario: No orchestrators result in no resume messages

- **WHEN** argus bounces AND link recovery succeeds AND there are no active orchestrators
- **THEN** `ResumeWorkers` MUST be a no-op (no messages sent, no errors)
