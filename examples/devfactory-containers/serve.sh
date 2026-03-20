#!/usr/bin/env bash
# Start boxy serve with this example's config.
# Can be run from any directory.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
exec go run "$REPO_ROOT/cmd/boxy" serve --config "$SCRIPT_DIR/boxy.yaml" "$@"
