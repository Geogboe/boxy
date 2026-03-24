#!/usr/bin/env bash
set -euo pipefail

REPO="Geogboe/boxy"
DEFAULT_INSTALL_DIR="${HOME}/.local/bin"

# env var overrides (flags below take higher precedence)
# BOXY_VERSION / INSTALLER_VERSION    — pin a release tag
# BOXY_INSTALL_DIR / INSTALLER_INSTALL_DIR — override install directory
# BOXY_FORCE / INSTALLER_FORCE        — set to 1 to force reinstall
# BOXY_DEBUG / INSTALLER_DEBUG        — set to 1 for verbose output
VERSION="${BOXY_VERSION:-${INSTALLER_VERSION:-${BOXY_INSTALL_VERSION:-latest}}}"
INSTALL_DIR="${BOXY_INSTALL_DIR:-${INSTALLER_INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}}"
FORCE="${BOXY_FORCE:-${INSTALLER_FORCE:-0}}"
DEBUG="${BOXY_DEBUG:-${INSTALLER_DEBUG:-0}}"

# ── colours ───────────────────────────────────────────────────────────────────
if [ -t 1 ]; then
  BOLD='\033[1m'
  GREEN='\033[0;32m'
  YELLOW='\033[0;33m'
  RED='\033[0;31m'
  CYAN='\033[0;36m'
  RESET='\033[0m'
else
  BOLD='' GREEN='' YELLOW='' RED='' CYAN='' RESET=''
fi

log()     { printf '%s\n' "$*"; }
step()    { printf "\n${BOLD}%s${RESET}\n" "$*"; }
info()    { printf "  ${CYAN}→${RESET} %s\n" "$*"; }
success() { printf "  ${GREEN}✓${RESET} %s\n" "$*"; }
warn()    { printf "  ${YELLOW}!${RESET} %s\n" "$*" >&2; }

debug() {
  if [[ "${DEBUG}" -eq 1 ]]; then
    printf 'debug: %s\n' "$*" >&2
  fi
}

fail() {
  printf "  ${RED}✗ error:${RESET} %s\n" "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: install.sh [--version <latest|tag>] [--install-dir <path>] [--force] [--debug]

Environment variables (flags take precedence):
  BOXY_VERSION      pin a release tag (e.g. v0.1.8)
  BOXY_INSTALL_DIR  override install directory
  BOXY_FORCE        set to 1 to reinstall even if boxy already exists
  BOXY_DEBUG        set to 1 for verbose output
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      [[ $# -ge 2 ]] || fail "--version requires a value"
      VERSION="$2"
      shift 2
      ;;
    --install-dir)
      [[ $# -ge 2 ]] || fail "--install-dir requires a value"
      INSTALL_DIR="$2"
      shift 2
      ;;
    --force)
      FORCE=1
      shift
      ;;
    --debug)
      DEBUG=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

if [[ "${DEBUG}" -eq 1 ]]; then
  set -x
fi

printf "\n${BOLD}${CYAN}boxy installer${RESET}\n"
printf "%s\n" "──────────────────────────────────────"

step "Detecting platform..."

case "$(uname -s)" in
  Linux*)  OS="linux" ;;
  Darwin*) OS="darwin" ;;
  *)       fail "unsupported OS: $(uname -s)" ;;
esac

case "$(uname -m)" in
  x86_64|amd64)
    ARCH="amd64"
    ;;
  aarch64|arm64)
    ARCH="arm64"
    ;;
  *)
    fail "unsupported architecture: $(uname -m)"
    ;;
esac

info "platform: ${OS}/${ARCH}"

if ! command -v curl >/dev/null 2>&1; then
  fail "curl is required"
fi

checksum_file() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
    return
  fi
  fail "sha256sum or shasum is required"
}

resolve_latest_tag() {
  debug "resolving latest release tag"
  local api="https://api.github.com/repos/${REPO}/releases?per_page=1"
  local response
  response="$(curl -fsSL "${api}")" || fail "failed to query GitHub releases API"

  local tag
  tag="$(
    printf '%s\n' "${response}" \
      | grep -m1 '"tag_name"' \
      | sed -E 's/.*"tag_name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/'
  )"
  [[ -n "${tag}" ]] || fail "could not resolve latest release tag"
  printf '%s\n' "${tag}"
}

step "Resolving version..."

if [[ "${VERSION}" == "latest" ]]; then
  info "fetching latest release tag..."
  VERSION="$(resolve_latest_tag)"
fi
info "version: ${VERSION}"

# GoReleaser archive naming: boxy_0.1.9_linux_amd64.tar.gz
# Strip leading 'v' from the tag to get the version number
VERSION_NUM="${VERSION#v}"
ARCHIVE="boxy_${VERSION_NUM}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
DOWNLOAD_URL="${BASE_URL}/${ARCHIVE}"
CHECKSUMS_URL="${BASE_URL}/checksums.txt"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

debug "version=${VERSION}"
debug "install_dir=${INSTALL_DIR}"
debug "archive=${ARCHIVE}"

step "Downloading..."
info "${DOWNLOAD_URL}"
curl -fsSL "${DOWNLOAD_URL}" -o "${TMP_DIR}/${ARCHIVE}" || fail "failed to download ${ARCHIVE}"
info "checksums.txt"
curl -fsSL "${CHECKSUMS_URL}" -o "${TMP_DIR}/checksums.txt" || fail "failed to download checksums.txt"

step "Verifying checksum..."
EXPECTED_CHECKSUM="$(awk -v asset="${ARCHIVE}" '$2 == asset { print $1 }' "${TMP_DIR}/checksums.txt")"
[[ -n "${EXPECTED_CHECKSUM}" ]] || fail "checksum for ${ARCHIVE} not found"

ACTUAL_CHECKSUM="$(checksum_file "${TMP_DIR}/${ARCHIVE}")"
if [[ "${EXPECTED_CHECKSUM}" != "${ACTUAL_CHECKSUM}" ]]; then
  fail "checksum mismatch for ${ARCHIVE}"
fi
success "checksum ok"

step "Installing..."
mkdir -p "${INSTALL_DIR}"
DEST="${INSTALL_DIR}/boxy"
if [[ -e "${DEST}" && "${FORCE}" -ne 1 ]]; then
  fail "${DEST} already exists; rerun with --force to overwrite"
fi

# extract only the binary from the archive
tar -xzf "${TMP_DIR}/${ARCHIVE}" -C "${TMP_DIR}" boxy
install -m 0755 "${TMP_DIR}/boxy" "${DEST}"
info "installed to: ${DEST}"

step "Verifying install..."
if VERSION_OUTPUT="$("${DEST}" --version 2>/dev/null)"; then
  success "${VERSION_OUTPUT}"
else
  "${DEST}" --help >/dev/null 2>&1 || fail "installed binary did not execute successfully"
  success "boxy installed successfully"
fi

case ":${PATH}:" in
  *":${INSTALL_DIR}:"*)
    ;;
  *)
    warn "${INSTALL_DIR} is not in your PATH."
    SHELL_NAME="$(basename "${SHELL:-sh}")"
    case "${SHELL_NAME}" in
      zsh)  PROFILE="${HOME}/.zshrc" ;;
      fish) PROFILE="" ;;
      *)    PROFILE="${HOME}/.bashrc" ;;
    esac
    printf "\n${YELLOW}  Run the command below, then open a new shell:${RESET}\n\n"
    if [[ "${SHELL_NAME}" == "fish" ]]; then
      printf "  ${CYAN}set -U fish_user_paths \$HOME/.local/bin \$fish_user_paths${RESET}\n\n"
    else
      printf "  ${CYAN}echo 'export PATH=\"%s:\$PATH\"' >> %s${RESET}\n\n" "${INSTALL_DIR}" "${PROFILE}"
    fi
    ;;
esac

printf "\n${BOLD}${GREEN}Done!${RESET} Run '${CYAN}boxy${RESET}' to get started.\n\n"
