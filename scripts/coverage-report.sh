#!/usr/bin/env bash
set -euo pipefail

profile_path=.tmp/coverage.out
if [[ ! -f "$profile_path" ]]; then
  printf 'coverage profile is missing: %s\n' "$profile_path" >&2
  exit 1
fi

go tool cover -func="$profile_path" | tail -n 1
