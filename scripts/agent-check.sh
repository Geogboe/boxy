#!/usr/bin/env bash
set -uo pipefail

if (($# < 2)); then
  printf 'usage: %s NAME COMMAND [ARGUMENTS...]\n' "$0" >&2
  exit 2
fi

name=$1
shift
report_dir=.tmp
mkdir -p "$report_dir"
safe_name=$(printf '%s' "$name" | tr -c 'A-Za-z0-9_.-' '-')
report_path="$report_dir/$safe_name.log"

"$@" >"$report_path" 2>&1
exit_code=$?
if ((exit_code != 0)); then
  if ! rg -n -C 5 -- '--- FAIL|FAIL\s|panic|undefined|cannot|fatal' "$report_path" | awk '{ if (length($0) > 400) print substr($0, 1, 400) " ...[truncated]"; else print }'; then
    tail -n 80 "$report_path"
  fi
  exit "$exit_code"
fi
