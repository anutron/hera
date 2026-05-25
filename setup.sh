#!/usr/bin/env bash
# Hera setup: build the binary, install it to ~/bin, create ~/.hera, and
# mint the scope token if one isn't already on disk. Idempotent — safe to
# re-run.
#
# Usage:
#   ./setup.sh           # interactive, prompts before mutating
#   ./setup.sh --yes     # non-interactive, accept all defaults
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

NON_INTERACTIVE=false
if [[ "${1:-}" == "--yes" || "${1:-}" == "-y" ]]; then
  NON_INTERACTIVE=true
fi

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

bold "1/4  Build"
if [[ -x "${BUILT_BIN}" ]]; then
  green "  ✓ ${BUILT_BIN} already exists"
else
  echo "  building ${BUILT_BIN}…"
  (cd "${SCRIPT_DIR}" && go build -o "${BUILT_BIN}" ./cmd/hera)
  green "  ✓ built ${BUILT_BIN}"
fi
echo

# --- 2. install to ~/bin -----------------------------------------------------

bold "2/4  Install to ${BIN_DIR}"
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

bold "3/4  State directory ${STATE_DIR}"
if [[ -d "${STATE_DIR}" ]]; then
  green "  ✓ ${STATE_DIR} already exists"
else
  mkdir -p "${STATE_DIR}"
  green "  ✓ created ${STATE_DIR}"
fi
chmod 700 "${STATE_DIR}"
echo

# --- 4. scope token ---------------------------------------------------------

bold "4/4  Scope token"
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

# --- done ------------------------------------------------------------------

bold "Setup complete."
echo
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
echo
