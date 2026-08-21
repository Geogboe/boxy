#!/usr/bin/env bash
set -euo pipefail

# Windows ARM64 cannot run Go's race detector. Run from a Linux-native WSL
# temporary checkout so the Windows-mounted repository does not turn ordinary
# refusal-timeout tests into filesystem/timing failures.
repo_dir=$(pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

git -C "$repo_dir" archive --format=tar HEAD | tar -C "$work_dir" -xf -
cd "$work_dir"

go test -race -short ./...
go test -race -short -tags devtools ./internal/cli/...
