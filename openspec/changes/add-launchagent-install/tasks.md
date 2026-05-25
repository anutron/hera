## 1. setup.sh skeleton

- [x] 1.1 Add `--uninstall-launchagent` to the argument parser, alongside the existing `--yes`/`-y` flag. Unknown flags MUST exit with status 1 and an error message.
- [x] 1.2 Add platform detection: capture `uname` once at the top of the script and store in a variable for later use.
- [x] 1.3 Wire up the new constants (PLIST_LABEL, PLIST_PATH, LAUNCH_TARGET, STABLE_LINK, LOG_PATH) near the existing constants block.
- [x] 1.4 Renumber step headers from `1/4`..`4/4` to `1/5`..`5/5` to make room for the LaunchAgent step.

## 2. Helper functions

- [x] 2.1 Add `launchagent_loaded()` – returns 0 if `launchctl print <target>` succeeds.
- [x] 2.2 Add `bootout_if_loaded()` – calls `launchctl bootout` when `launchagent_loaded` is true.
- [x] 2.3 Add `stop_foreground_hera()` – greps for `hera start --foreground` (specifically that string, not `herad`) and SIGTERMs the match; sleeps 1s.
- [x] 2.4 Add `write_plist()` – writes the plist file with the exact XML in design.md Decision 3 (KeepAlive + SuccessfulExit=false), embedding the user's HOME, the stable symlink path, and the log path.
- [x] 2.5 Add `uninstall_launchagent()` – bootout, remove plist, remove symlink, print success.

## 3. Uninstall short-circuit

- [x] 3.1 After argument parsing, if `--uninstall-launchagent` was passed, call `uninstall_launchagent()` and `exit 0`.
- [x] 3.2 The short-circuit MUST run BEFORE the build/install/state-dir/token steps (so it does not rebuild or touch unrelated state).

## 4. Step 5 implementation

- [x] 4.1 Add the step 5 header (`bold "5/5  LaunchAgent ..."`).
- [x] 4.2 Platform guard: if `uname` is not `Darwin`, print a one-line skip message and continue to the final summary. Do NOT exit; the rest of setup must complete.
- [x] 4.3 On Darwin, prompt the user via the existing `confirm()` helper. If declined, print a skip message and continue.
- [x] 4.4 On accept: call `stop_foreground_hera`, then `bootout_if_loaded`, then `ln -sf "${BUILT_BIN}" "${STABLE_LINK}"`, then `write_plist`, then `launchctl bootstrap "gui/$(id -u)" "${PLIST_PATH}"`.
- [x] 4.5 Print verification hints (`launchctl print ...`, `tail -f ~/.hera/launchd.log`) on successful bootstrap.

## 5. Final summary message

- [x] 5.1 Branch the trailing summary based on `launchagent_loaded`: if true, point at `hera_new_orchestrator(...)` directly; if false, instruct the user to run `hera start --foreground`.
- [x] 5.2 In the launchd-loaded branch, surface the useful commands: `hera status`, `hera list`, `tail -f ~/.hera/launchd.log`, and `./setup.sh --uninstall-launchagent`.

## 6. Smoke tests (manual, on macOS)

- [ ] 6.1 Fresh install: from a state with no plist and no symlink, run `./setup.sh --yes` and verify the agent boots, `hera_status` works, `Activity Monitor` shows `herad`.
- [ ] 6.2 Re-run idempotency: run `./setup.sh --yes` a second time; verify the agent reboots cleanly (bootout + bootstrap), no error, no duplicate processes.
- [ ] 6.3 Crash restart: `pkill -9 herad`; verify launchd restarts it within a few seconds.
- [ ] 6.4 Clean exit no-restart: send SIGTERM to the agent process (`launchctl kill TERM <target>` or equivalent); verify it does NOT auto-restart.
- [ ] 6.5 Uninstall: run `./setup.sh --uninstall-launchagent` and verify plist + symlink gone, binary + token untouched, `launchctl print` fails.
- [ ] 6.6 Decline path: run `./setup.sh` interactively, answer `n` at step 5, verify no plist written, final message says "run `hera start --foreground`".
- [ ] 6.7 Foreground conflict: start `hera start --foreground` in a terminal, then run `./setup.sh --yes`; verify the foreground process is SIGTERM'd before bootstrap and the agent succeeds.

## 7. Cross-platform smoke (manual, on Linux if available)

- [ ] 7.1 Run `./setup.sh --yes` on Linux; verify steps 1-4 complete, step 5 prints skip message, exit status is 0, no `launchctl` errors surface.

## 8. Documentation

- [x] 8.1 Update README install path to mention the LaunchAgent option in step 5 of setup.sh and the `--uninstall-launchagent` flag (one short paragraph; the script's prompts are the primary documentation).
- [x] 8.2 Verify NEXT.md / handoff docs don't need updates (this change is contained to setup.sh and the new spec).

## 9. Archive

- [ ] 9.1 After implementation lands and smoke tests pass, run `openspec archive add-launchagent-install` to merge the delta into the base `hera-install` capability spec.
