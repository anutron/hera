## ADDED Requirements

### Requirement: setup.sh offers an opt-in macOS LaunchAgent install step

On macOS, `setup.sh` SHALL include a step that installs a per-user LaunchAgent at `~/Library/LaunchAgents/com.anutron.hera.plist` and bootstraps it into the user's gui domain via `launchctl bootstrap`, so hera starts automatically at login. The step MUST prompt the user before installing in interactive mode; `--yes`/`-y` mode MUST accept the install without prompting.

The plist's `ProgramArguments` SHALL invoke `hera start --foreground` via the stable symlink defined in [Stable symlink for launchd process identity].

The plist SHALL set `RunAtLoad` to true so the agent launches as soon as it is bootstrapped.

The plist SHALL set `StandardOutPath` and `StandardErrorPath` to `~/.hera/launchd.log` so launchd-managed output is captured to disk.

#### Scenario: User accepts the LaunchAgent prompt during interactive setup

- **WHEN** `./setup.sh` reaches step 5 and the user answers `y` (or accepts the default) at the install prompt
- **THEN** `~/Library/LaunchAgents/com.anutron.hera.plist` SHALL be written
- **AND** `launchctl bootstrap gui/<uid> <plist>` SHALL be invoked successfully
- **AND** `launchctl print gui/<uid>/com.anutron.hera` SHALL return successfully (the agent is loaded)
- **AND** hera SHALL be reachable via the MCP `hera_status` tool without the user having run `hera start --foreground`

#### Scenario: Non-interactive mode installs without prompting

- **WHEN** `./setup.sh --yes` is invoked on macOS
- **THEN** step 5 SHALL install the LaunchAgent without any user prompt
- **AND** the final summary message SHALL indicate that hera is running under launchd

#### Scenario: User declines the LaunchAgent prompt

- **WHEN** `./setup.sh` reaches step 5 and the user answers `n` at the install prompt
- **THEN** no plist SHALL be written
- **AND** no `launchctl bootstrap` SHALL be invoked
- **AND** the final summary message SHALL instruct the user to start hera manually via `hera start --foreground`

### Requirement: LaunchAgent restarts hera on crash but not on clean exit

The plist installed by `setup.sh` SHALL configure `KeepAlive` to restart hera only when the previous run exited with a non-zero status. A graceful shutdown (zero exit code) MUST NOT trigger an automatic restart.

This SHALL be expressed in the plist as:

```xml
<key>KeepAlive</key>
<dict>
    <key>SuccessfulExit</key>
    <false/>
</dict>
```

#### Scenario: Hera crashes with non-zero exit

- **WHEN** the launchd-managed hera process exits with status 1 (e.g., panic, kill -9)
- **THEN** launchd SHALL restart it within a few seconds

#### Scenario: Hera exits cleanly

- **WHEN** the user (or another process) sends SIGTERM and the launchd-managed hera shuts down cleanly with status 0
- **THEN** launchd SHALL NOT restart it
- **AND** subsequent `launchctl print gui/<uid>/com.anutron.hera` output SHALL show the agent loaded but not running

### Requirement: Stable symlink for launchd process identity

On macOS, the LaunchAgent install step SHALL create or update a symlink at `~/.hera/herad` pointing at the built hera binary at `<repo>/bin/hera`. The plist's `ProgramArguments` SHALL invoke this symlink path, not the binary directly.

The symlink SHALL be refreshed (via `ln -sf`) on every re-run of setup.sh's LaunchAgent step.

#### Scenario: Symlink is created on first install

- **WHEN** `./setup.sh --yes` runs step 5 for the first time
- **THEN** `~/.hera/herad` SHALL exist as a symlink
- **AND** `readlink ~/.hera/herad` SHALL return the absolute path to `<repo>/bin/hera`
- **AND** the launchd-managed process SHALL appear in Activity Monitor with process name `herad`

#### Scenario: Symlink is refreshed on re-run

- **WHEN** the user rebuilds hera (changing the binary at `<repo>/bin/hera`) and re-runs `./setup.sh --yes`
- **THEN** `~/.hera/herad` SHALL be re-pointed via `ln -sf`
- **AND** the agent SHALL be booted out and re-bootstrapped so the new binary is picked up without requiring a logout/reboot

### Requirement: LaunchAgent install detects and stops foreground hera

Before bootstrapping the LaunchAgent, setup.sh SHALL detect any running process matching `hera start --foreground` and send it SIGTERM. This prevents the launchd-managed instance and a pre-existing foreground instance from contending for the MCP port.

The detection MUST match only the `hera start --foreground` invocation pattern (not the launchd-managed `herad` invocation), so re-runs of setup.sh do not SIGTERM the agent's own process.

#### Scenario: Foreground hera is running when install runs

- **WHEN** `hera start --foreground` is running in a terminal and `./setup.sh --yes` reaches step 5
- **THEN** setup.sh SHALL warn the user that it is terminating the foreground instance
- **AND** SIGTERM SHALL be sent to the foreground process
- **AND** setup.sh SHALL proceed with bootstrap after a short delay

#### Scenario: No foreground hera is running

- **WHEN** no `hera start --foreground` process exists and `./setup.sh --yes` reaches step 5
- **THEN** setup.sh SHALL proceed directly to bootstrap without sending any signals

### Requirement: setup.sh provides --uninstall-launchagent flag

`setup.sh` SHALL accept a `--uninstall-launchagent` flag. When passed, setup.sh MUST:

1. Boot out the agent (`launchctl bootout gui/<uid>/com.anutron.hera`) if it is loaded
2. Remove `~/Library/LaunchAgents/com.anutron.hera.plist` if present
3. Remove the `~/.hera/herad` symlink if present
4. Exit with status 0 without running any other setup steps

The flag MUST NOT touch the hera binary at `~/bin/hera`, the state directory `~/.hera/`, or the scope token at `~/.hera/api-token`.

#### Scenario: Uninstall when the agent is loaded

- **WHEN** the LaunchAgent is loaded and `./setup.sh --uninstall-launchagent` is invoked
- **THEN** `launchctl print gui/<uid>/com.anutron.hera` SHALL fail (agent unloaded)
- **AND** `~/Library/LaunchAgents/com.anutron.hera.plist` SHALL NOT exist
- **AND** `~/.hera/herad` SHALL NOT exist
- **AND** `~/bin/hera` SHALL still exist and be executable
- **AND** `~/.hera/api-token` SHALL be unchanged

#### Scenario: Uninstall when nothing is installed

- **WHEN** no LaunchAgent is loaded and no plist exists and `./setup.sh --uninstall-launchagent` is invoked
- **THEN** setup.sh SHALL exit with status 0
- **AND** SHALL print a success message indicating nothing to remove

#### Scenario: Uninstall flag short-circuits other setup steps

- **WHEN** `./setup.sh --uninstall-launchagent` is invoked
- **THEN** the build, install-to-bin, state-dir, and scope-token steps SHALL NOT run

### Requirement: LaunchAgent step is skipped on non-Darwin systems

When `setup.sh` runs on a non-macOS host (i.e., `uname` does not report `Darwin`), step 5 SHALL be skipped with a one-line informational message. The skip MUST NOT cause setup.sh to exit non-zero, and the preceding steps (build, install, state dir, scope token) SHALL still run to completion.

#### Scenario: Setup runs on Linux

- **WHEN** `./setup.sh --yes` is invoked on a Linux host
- **THEN** steps 1-4 (build, install, state dir, scope token) SHALL run successfully
- **AND** step 5 SHALL print a message indicating LaunchAgent install is skipped on non-macOS
- **AND** setup.sh SHALL exit with status 0
- **AND** no `launchctl` invocations SHALL occur
- **AND** the final summary message SHALL instruct the user to start hera manually
