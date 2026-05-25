#!/usr/bin/env bash
# Hera setup: build the binary, install it to ~/bin, create ~/.hera, mint
# the scope token, and (optionally, on macOS) install a per-user LaunchAgent
# so hera runs at login and restarts on crash. Idempotent — safe to re-run.
#
# Usage:
#   ./setup.sh                          # interactive, prompts before mutating
#   ./setup.sh --yes                    # non-interactive, accept all defaults
#   ./setup.sh --uninstall-launchagent  # remove the LaunchAgent only, then exit
#
# Prereqs: argus on PATH, go on PATH.

set -euo pipefail

# --- config ------------------------------------------------------------------

STATE_DIR="${HOME}/.hera"
TOKEN_PATH="${STATE_DIR}/api-token"
BIN_DIR="${HOME}/bin"
BIN_NAME="hera"
INSTALL_PATH="${BIN_DIR}/${BIN_NAME}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BUILT_BIN="${SCRIPT_DIR}/bin/${BIN_NAME}"
STABLE_LINK="${STATE_DIR}/herad"
PLIST_LABEL="com.anutron.hera"
PLIST_PATH="${HOME}/Library/LaunchAgents/${PLIST_LABEL}.plist"
LAUNCH_TARGET="gui/$(id -u)/${PLIST_LABEL}"
LOG_PATH="${STATE_DIR}/launchd.log"

PLATFORM="$(uname)"

NON_INTERACTIVE=false
UNINSTALL_ONLY=false
for arg in "$@"; do
  case "$arg" in
    --yes|-y) NON_INTERACTIVE=true ;;
    --uninstall-launchagent) UNINSTALL_ONLY=true ;;
    *) echo "unknown flag: $arg" >&2; exit 1 ;;
  esac
done

# --- helpers ----------------------------------------------------------------

bold()  { printf '\033[1m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
red()   { printf '\033[31m%s\033[0m\n' "$*" >&2; }
warn()  { printf '\033[33m%s\033[0m\n' "$*"; }

confirm() {
  if $NON_INTERACTIVE; then
    return 0
  fi
  read -r -p "$1 [Y/n] " reply
  [[ -z "$reply" || "$reply" =~ ^[Yy] ]]
}

launchagent_loaded() {
  # On non-Darwin systems launchctl isn't present; the 2>&1 swallow makes this
  # safely return non-zero so the rest of the script branches "not loaded".
  launchctl print "${LAUNCH_TARGET}" >/dev/null 2>&1
}

bootout_if_loaded() {
  if launchagent_loaded; then
    echo "  booting out ${PLIST_LABEL}…"
    launchctl bootout "${LAUNCH_TARGET}"
  fi
}

stop_foreground_hera() {
  # Only match `hera start --foreground` so we don't SIGTERM the LaunchAgent's
  # own herad process during a re-run.
  if pgrep -f "${BIN_NAME} start --foreground" >/dev/null 2>&1; then
    warn "  detected a running ${BIN_NAME} start --foreground; sending SIGTERM…"
    pkill -TERM -f "${BIN_NAME} start --foreground" || true
    sleep 1
  fi
}

write_plist() {
  mkdir -p "$(dirname "${PLIST_PATH}")"
  # KeepAlive uses SuccessfulExit=false: restart on crash, but let a graceful
  # SIGTERM (clean exit 0) stay down so the user can actually stop hera.
  cat > "${PLIST_PATH}" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>${PLIST_LABEL}</string>
	<key>ProgramArguments</key>
	<array>
		<string>${STABLE_LINK}</string>
		<string>start</string>
		<string>--foreground</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>${HOME}/.local/bin:${HOME}/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
		<key>HOME</key>
		<string>${HOME}</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>StandardOutPath</key>
	<string>${LOG_PATH}</string>
	<key>StandardErrorPath</key>
	<string>${LOG_PATH}</string>
	<key>WorkingDirectory</key>
	<string>${HOME}</string>
	<key>ProcessType</key>
	<string>Interactive</string>
</dict>
</plist>
EOF
}

uninstall_launchagent() {
  bold "Uninstalling LaunchAgent"
  local did_something=false

  if launchagent_loaded; then
    echo "  booting out ${PLIST_LABEL}…"
    launchctl bootout "${LAUNCH_TARGET}"
    green "  ✓ booted out ${LAUNCH_TARGET}"
    did_something=true
  fi

  if [[ -f "${PLIST_PATH}" ]]; then
    rm "${PLIST_PATH}"
    green "  ✓ removed ${PLIST_PATH}"
    did_something=true
  fi

  if [[ -L "${STABLE_LINK}" || -e "${STABLE_LINK}" ]]; then
    rm "${STABLE_LINK}"
    green "  ✓ removed symlink ${STABLE_LINK}"
    did_something=true
  fi

  echo
  if $did_something; then
    bold "Uninstall complete."
  else
    bold "Nothing to remove — no LaunchAgent, plist, or symlink found."
  fi
}

# --- uninstall short-circuit ------------------------------------------------

if $UNINSTALL_ONLY; then
  uninstall_launchagent
  exit 0
fi

# --- preflight --------------------------------------------------------------

bold "hera setup"
echo

if ! command -v argus >/dev/null 2>&1; then
  red "argus not found on PATH."
  red "Install argus first (https://github.com/drn/argus), then re-run."
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  red "go not found on PATH."
  red "Install Go 1.22+ (https://go.dev/dl/), then re-run."
  exit 1
fi

# --- 1. build hera -----------------------------------------------------------

bold "1/5  Build"
if [[ -x "${BUILT_BIN}" ]]; then
  green "  ✓ ${BUILT_BIN} already exists"
else
  echo "  building ${BUILT_BIN}…"
  (cd "${SCRIPT_DIR}" && go build -o "${BUILT_BIN}" ./cmd/hera)
  green "  ✓ built ${BUILT_BIN}"
fi
echo

# --- 2. install to ~/bin -----------------------------------------------------

bold "2/5  Install to ${BIN_DIR}"
mkdir -p "${BIN_DIR}"
if [[ -x "${INSTALL_PATH}" ]] && cmp -s "${BUILT_BIN}" "${INSTALL_PATH}"; then
  green "  ✓ ${INSTALL_PATH} is already current"
else
  if [[ -x "${INSTALL_PATH}" ]]; then
    if ! confirm "  Overwrite existing ${INSTALL_PATH}?"; then
      warn "  skipped install; ${INSTALL_PATH} unchanged"
    else
      cp "${BUILT_BIN}" "${INSTALL_PATH}"
      green "  ✓ installed ${INSTALL_PATH}"
    fi
  else
    cp "${BUILT_BIN}" "${INSTALL_PATH}"
    green "  ✓ installed ${INSTALL_PATH}"
  fi
fi
if ! echo ":$PATH:" | grep -q ":${BIN_DIR}:"; then
  warn "  note: ${BIN_DIR} is not on your PATH. Add it to your shell rc:"
  warn "        export PATH=\"\$HOME/bin:\$PATH\""
fi
echo

# --- 3. state dir -----------------------------------------------------------

bold "3/5  State directory ${STATE_DIR}"
if [[ -d "${STATE_DIR}" ]]; then
  green "  ✓ ${STATE_DIR} already exists"
else
  mkdir -p "${STATE_DIR}"
  green "  ✓ created ${STATE_DIR}"
fi
chmod 700 "${STATE_DIR}"
echo

# --- 4. scope token ---------------------------------------------------------

bold "4/5  Scope token"
if [[ -s "${TOKEN_PATH}" ]]; then
  green "  ✓ ${TOKEN_PATH} already populated; leaving alone"
  echo "    (delete it and re-run to mint a fresh one)"
else
  if [[ -f "${TOKEN_PATH}" ]]; then
    warn "  ${TOKEN_PATH} exists but is empty; will overwrite"
  fi
  if ! confirm "  Mint a hera scope token via 'argus token mint --scope hera'?"; then
    warn "  skipped token mint. You'll need to populate ${TOKEN_PATH} yourself before 'hera start' works."
  else
    # argus token mint --scope hera prints a few lines; we want the token: <token> line.
    if ! argus token mint --scope hera | awk '/^token:/ {print $2}' > "${TOKEN_PATH}"; then
      red "  failed to mint token. Check that the argus daemon is running and reachable."
      exit 1
    fi
    if [[ ! -s "${TOKEN_PATH}" ]]; then
      red "  argus token mint succeeded but the token line was not captured at ${TOKEN_PATH}."
      red "  Try: argus token mint --scope hera > ${TOKEN_PATH} and edit out the surrounding lines manually."
      exit 1
    fi
    chmod 600 "${TOKEN_PATH}"
    green "  ✓ minted scope token at ${TOKEN_PATH}"
  fi
fi
echo

# --- 5. LaunchAgent ---------------------------------------------------------

bold "5/5  LaunchAgent (runs at login, restarts on crash)"
if [[ "${PLATFORM}" != "Darwin" ]]; then
  warn "  skipping LaunchAgent install (not macOS: ${PLATFORM})"
elif ! confirm "  Install ~/Library/LaunchAgents/${PLIST_LABEL}.plist?"; then
  warn "  skipped LaunchAgent install. Run hera manually with: ${INSTALL_PATH} start --foreground"
else
  stop_foreground_hera

  # If already loaded, bootout first so the new plist takes effect cleanly.
  bootout_if_loaded

  # rm first because `ln -sf` cannot overwrite a regular file (only a symlink).
  rm -f "${STABLE_LINK}"
  ln -s "${BUILT_BIN}" "${STABLE_LINK}"
  green "  ✓ symlink ${STABLE_LINK} → ${BUILT_BIN}"

  write_plist
  green "  ✓ wrote ${PLIST_PATH}"

  launchctl bootstrap "gui/$(id -u)" "${PLIST_PATH}"
  green "  ✓ bootstrapped into launchd"
  echo
  echo "  Verify with:"
  echo "    launchctl print ${LAUNCH_TARGET} | head"
  echo "    tail -f ${LOG_PATH}"
fi
echo

# --- done ------------------------------------------------------------------

bold "Setup complete."
echo
if launchagent_loaded; then
  echo "Hera is running under launchd. From any argus task with MCP access:"
  echo
  echo "    hera_new_orchestrator(cwd=\$PWD, name=\"my-project\", coordinator_role_name=\"coord\", mission=\"...\")"
  echo
  echo "Useful commands:"
  echo
  echo "    hera status                          # daemon health"
  echo "    hera list                            # orchestrator/role tree"
  echo "    tail -f ${LOG_PATH}                  # launchd-captured stdout/stderr"
  echo "    ./setup.sh --uninstall-launchagent   # remove the LaunchAgent only"
else
  echo "Start hera in the foreground (keep this terminal open):"
  echo
  echo "    hera start --foreground"
  echo
  echo "Then from any argus task with MCP access, bootstrap an orchestrator:"
  echo
  echo "    hera_new_orchestrator(cwd=\$PWD, name=\"my-project\", coordinator_role_name=\"coord\", mission=\"...\")"
  echo
  echo "Check daemon state at any time:"
  echo
  echo "    hera status"
  echo "    hera list"
fi
echo
