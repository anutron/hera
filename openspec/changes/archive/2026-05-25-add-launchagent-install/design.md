## Context

Hera is a small Go daemon that brokers MCP coordination between argus tasks. Today it runs only when the user manually invokes `hera start --foreground` from a terminal. The existing `setup.sh` covers build, binary install to `~/bin`, state-dir creation, and scope-token mint, but stops short of arranging for the daemon to persist across terminal closures, logouts, or crashes.

A candidate `setup.sh` extension already exists (`/tmp/hera-setup.sh`, 8.9K vs. the committed 4.8K) that wires up a per-user macOS LaunchAgent. It is the raw material this design formalizes.

## Goals / Non-Goals

**Goals:**

- Give users a one-shot, opt-in install step that makes hera durable: starts at login, restarts on crash, captures logs.
- Keep the install reversible – a single `setup.sh --uninstall-launchagent` removes the LaunchAgent without touching the binary, state dir, or token.
- Preserve the existing manual path. Users who prefer `hera start --foreground` in a terminal can decline the LaunchAgent prompt and lose nothing.
- Make the launchd-managed process visually distinct in Activity Monitor (so a user running both a foreground hera and the LaunchAgent-managed one can tell them apart).

**Non-Goals:**

- **Linux/systemd parity.** The candidate is macOS-only; this change ships macOS-only. The capability surface is written so a future change can add `linux-systemd` requirements without restructuring.
- **Daemon runtime changes.** The hera binary is unchanged. This is a `setup.sh`-only change.
- **Multi-user / system-wide install.** The LaunchAgent lives under `~/Library/LaunchAgents/` and runs in the user's gui domain only.
- **CLI subcommand for install/uninstall.** All lifecycle is driven by `setup.sh`. We're not adding `hera install` or `hera uninstall` subcommands at this time.

## Decisions

### Decision 1: LaunchAgent over launchd daemon (system-level)

We install a **LaunchAgent** (`~/Library/LaunchAgents/com.anutron.hera.plist`, gui domain) rather than a launchd **daemon** (`/Library/LaunchDaemons/`, system domain).

- **Rationale:** Hera reads `~/.hera/api-token` (per the existing `hera-coordination` spec) and is conceptually a per-user coordinator. A system daemon would have to handle multiple users, multiple state dirs, and per-user tokens. None of that is in scope.
- **Trade-off:** Hera only runs when the user is logged in. Acceptable – there is no use case for hera coordinating tasks for a logged-out user.
- **Alternative considered:** brew services. Rejected because hera is not (yet) installed via Homebrew and we don't want to take on that distribution channel for one feature.

### Decision 2: Stable symlink at `~/.hera/herad`

The plist's `ProgramArguments` points at `~/.hera/herad`, a symlink to the built binary (`<repo>/bin/hera`), not at `~/bin/hera`.

- **Rationale 1 (visual):** Activity Monitor shows the symlink target name. Using `herad` makes the launchd-managed process visually distinct from a foreground `hera` invocation – useful when debugging "why are two heras fighting for the port?"
- **Rationale 2 (stability):** If the user later moves the repo, they can `ln -sf` the symlink to the new build location without editing the plist or rebootstrapping the agent.
- **Alternative considered:** Point the plist directly at `~/bin/hera`. Simpler, but loses the visual distinction and ties the plist to the install location.

### Decision 3: `KeepAlive` only on non-zero exit

```xml
<key>KeepAlive</key>
<dict>
    <key>SuccessfulExit</key>
    <false/>
</dict>
```

This restarts hera on crash but NOT on clean exit. A user who runs `hera stop` (or equivalent SIGTERM) should be able to stop hera without launchd immediately resurrecting it.

- **Alternative considered:** Plain `<key>KeepAlive</key><true/>`. Rejected – it'd make graceful shutdown impossible without first booting out the agent.

### Decision 4: Pre-bootstrap cleanup of foreground hera

Before bootstrapping the agent, `setup.sh` greps for any `hera start --foreground` process and SIGTERMs it. Otherwise the new launchd-managed process and the old foreground one will fight over the MCP port.

- **Trade-off:** The user loses any in-flight state in the foreground daemon. Acceptable because the daemon persists all coordination state in argus/SQLite (per `hera-coordination` spec); the worst case is a momentary blip while the LaunchAgent-managed hera reconnects.
- The grep is specifically `hera start --foreground` so we don't accidentally SIGTERM the LaunchAgent-managed process during a re-run.

### Decision 5: Platform guard

On non-Darwin systems, step 5 prints `skipping LaunchAgent install (not macOS)` and returns success. The rest of `setup.sh` (build, install, state dir, token mint) still runs.

- **Rationale:** Keeps `setup.sh` cross-platform-friendly for the non-LaunchAgent steps. A Linux user can still build and install hera; they just don't get the daemonization.
- **Future:** When systemd support arrives, this branch becomes `if Darwin → LaunchAgent path; elif Linux → systemd path; else skip`.

### Decision 6: `--uninstall-launchagent` is a short-circuit flag

Passing `--uninstall-launchagent` to `setup.sh` runs ONLY the uninstall path (bootout + remove plist + remove symlink) and exits. It does NOT also rebuild, reinstall, or touch the token.

- **Rationale:** Uninstall is rare and high-trust; keeping it a separate flag (vs. an interactive prompt at the end of full setup) makes it explicit.
- **Alternative considered:** A `--uninstall` flag that nukes everything (binary, state dir, token, LaunchAgent). Rejected – too many ways to lose state. The LaunchAgent is the only reversible-by-script piece; users who want a full nuke can `rm` the rest themselves.

## Risks / Trade-offs

- **Risk:** User upgrades hera (rebuilds the binary) but the LaunchAgent keeps running the old code until reboot or manual bootout/bootstrap.
  - **Mitigation:** `setup.sh` re-run boots out and re-bootstraps the agent every time step 5 runs. So `./setup.sh --yes` after a `git pull` picks up the new binary. Document this in the final summary message.

- **Risk:** User has a stale `com.anutron.hera.plist` from a previous version of the candidate script with different keys.
  - **Mitigation:** `setup.sh` always overwrites the plist before bootstrap. No merge logic; the plist is fully managed.

- **Risk:** `launchctl bootstrap` fails (e.g., SIP, permission issues, malformed plist).
  - **Mitigation:** Surface the launchctl exit code; do NOT swallow it. User has to fix and re-run.

- **Risk:** Two hera repos on one machine both install the LaunchAgent and clobber each other.
  - **Mitigation (informal, not blocking):** Single plist label `com.anutron.hera` means the second install overwrites the first. We document this; we don't add multi-instance support.

- **Trade-off:** The launchd-managed hera logs go to `~/.hera/launchd.log` (combined stdout+stderr), not to stdout of any terminal. Users debugging will need to `tail -f` the log. Documented in the final summary message.

## Migration Plan

1. Land this change behind the new `hera-install` capability.
2. Existing users keep running `hera start --foreground` until they choose to re-run `./setup.sh` and accept the LaunchAgent prompt.
3. No data migration. No state-dir changes. Token file is untouched.
4. **Rollback:** `./setup.sh --uninstall-launchagent` removes the LaunchAgent. The binary, state dir, and token are untouched, so manual `hera start --foreground` resumes working immediately.
