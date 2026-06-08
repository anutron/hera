## MODIFIED Requirements

### Requirement: Recovery routine sends worker resume messages on success

After completing all existing recovery steps (port re-discovery, baseURL update, MCP tool re-registration, settings re-registration), the recovery routine SHALL enumerate all active managed worker roles with live bindings and send each a static resume message if and only if the recovery succeeded (link state transitioned to `LinkHealthy`). If recovery fails (link state stays `LinkDown`), no resume messages are sent.

#### Scenario: Successful recovery triggers worker resume sweep

- **WHEN** the watcher fires `OnRestart` AND the full link recovery sequence completes successfully (link state = `LinkHealthy`)
- **THEN** hera MUST call `BounceRecoverer.ResumeWorkers` to send resume messages to all active managed workers

#### Scenario: Failed recovery does not send resume messages

- **WHEN** the watcher fires `OnRestart` AND link recovery fails (link state remains `LinkDown`)
- **THEN** hera MUST NOT call `BounceRecoverer.ResumeWorkers`; the next successful recovery attempt will send the messages
