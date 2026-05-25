## Why

Hera is a daemon that must be running for any orchestrator/worker session to use the `hera_*` MCP tools. Today the only install path leaves users running `hera start --foreground` in a terminal – close the terminal and coordination dies. There is no setup-time option to make hera durable across logins or crashes, so users either babysit a terminal or write their own LaunchAgent by hand.

A candidate setup.sh extension already exists that wires up a per-user macOS LaunchAgent; this change pulls that work behind a proposal and delta so the install path has spec coverage.

## What Changes

- Add a fifth step to `setup.sh` (on macOS) that installs a per-user LaunchAgent at `~/Library/LaunchAgents/com.anutron.hera.plist` and bootstraps it into the gui domain so hera runs at login and auto-restarts on crash.
- Add a `--uninstall-launchagent` flag to `setup.sh` for clean removal (bootout, remove plist, remove stable symlink).
- Create a stable symlink at `~/.hera/herad` pointing at the built binary so the launchd-managed process appears as `herad` in Activity Monitor (separable from foreground `hera` invocations).
- Capture launchd-managed stdout/stderr to `~/.hera/launchd.log`.
- Detect and SIGTERM any pre-existing `hera start --foreground` process before bootstrapping, so the new launchd-managed instance can bind cleanly.
- Platform guard: on non-Darwin systems, the LaunchAgent step is skipped with a one-line note (the rest of setup.sh still runs).
- Final setup.sh summary message branches on whether the LaunchAgent is loaded – tells the user `hera_new_orchestrator(...)` is ready vs. "run `hera start --foreground` to start the daemon."

## Capabilities

### New Capabilities

- `hera-install`: covers the `setup.sh` install path – build, binary install, state dir, scope token mint, and (on macOS) LaunchAgent install/uninstall. This capability scopes operational lifecycle separately from `hera-coordination`, which owns runtime semantics.

### Modified Capabilities

<!-- None. The existing hera-coordination capability is unaffected; daemon runtime behavior is unchanged. -->

## Impact

- **Modified:** `setup.sh` (adds step 5, the `--uninstall-launchagent` flag, helper functions, and the platform guard).
- **New filesystem artifacts created by setup.sh on macOS:**
  - `~/Library/LaunchAgents/com.anutron.hera.plist`
  - `~/.hera/herad` (symlink → `<repo>/bin/hera`)
  - `~/.hera/launchd.log`
- **No changes to:** the hera binary itself, the `hera-coordination` spec, the MCP tool surface, or daemon runtime semantics. This is a setup-script-only change.
- **Backwards compatibility:** the LaunchAgent step is opt-in via the interactive prompt; `--yes` mode accepts it. Users on Linux are unaffected (the step is skipped). Users who already run hera manually can ignore the prompt.
