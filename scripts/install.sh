#!/usr/bin/env bash
set -euo pipefail

REPO="Geogboe/boxy"
DEFAULT_INSTALL_DIR="${HOME}/.local/bin"
VERSION="${BOXY_INSTALL_VERSION:-latest}"
INSTALL_DIR="${DEFAULT_INSTALL_DIR}"
FORCE=0
DEBUG=0

log() {
  printf '%s\n' "$*"
}

debug() {
  if [[ "${DEBUG}" -eq 1 ]]; then
    printf 'debug: %s\n' "$*" >&2
  fi
}

fail() {
  printf 'error: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: install.sh [--version <latest|tag>] [--install-dir <path>] [--force] [--debug]
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

if [[ "$(uname -s)" != "Linux" ]]; then
  fail "this installer only supports Linux"
fi

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
  tag="$(printf '%s' "${response}" | sed -n 's/.*"tag_name":"\([^"]*\)".*/\1/p' | head -n1)"
  [[ -n "${tag}" ]] || fail "could not resolve latest release tag"
  printf '%s\n' "${tag}"
}

if [[ "${VERSION}" == "latest" ]]; then
  VERSION="$(resolve_latest_tag)"
fi

ASSET="boxy-linux-${ARCH}"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
DOWNLOAD_URL="${BASE_URL}/${ASSET}"
CHECKSUMS_URL="${BASE_URL}/checksums.txt"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

debug "version=${VERSION}"
debug "install_dir=${INSTALL_DIR}"
debug "asset=${ASSET}"

curl -fsSL "${DOWNLOAD_URL}" -o "${TMP_DIR}/${ASSET}" || fail "failed to download ${ASSET}"
curl -fsSL "${CHECKSUMS_URL}" -o "${TMP_DIR}/checksums.txt" || fail "failed to download checksums.txt"

EXPECTED_CHECKSUM="$(awk -v asset="${ASSET}" '$2 == asset { print $1 }' "${TMP_DIR}/checksums.txt")"
[[ -n "${EXPECTED_CHECKSUM}" ]] || fail "checksum for ${ASSET} not found"

ACTUAL_CHECKSUM="$(checksum_file "${TMP_DIR}/${ASSET}")"
if [[ "${EXPECTED_CHECKSUM}" != "${ACTUAL_CHECKSUM}" ]]; then
  fail "checksum mismatch for ${ASSET}"
fi

mkdir -p "${INSTALL_DIR}"
DEST="${INSTALL_DIR}/boxy"
if [[ -e "${DEST}" && "${FORCE}" -ne 1 ]]; then
  fail "${DEST} already exists; rerun with --force to overwrite"
fi

install -m 0755 "${TMP_DIR}/${ASSET}" "${DEST}"

VERSION_OUTPUT="$("${DEST}" --version)"
log "Installed ${VERSION_OUTPUT} to ${DEST}"

case ":${PATH}:" in
  *":${INSTALL_DIR}:"*)
    ;;
  *)
    log ""
    log "${INSTALL_DIR} is not on your PATH."
    log "Add it with:"
    log "export PATH=\"${INSTALL_DIR}:\$PATH\""
    ;;
esac
