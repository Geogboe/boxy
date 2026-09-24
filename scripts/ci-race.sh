#!/usr/bin/env bash
set -euo pipefail

# Windows ARM64 cannot run Go's race detector. Run from a Linux-native WSL
# temporary checkout so the Windows-mounted repository does not turn ordinary
# refusal-timeout tests into filesystem/timing failures.
repo_dir=$(pwd)
work_dir=$(mktemp -d)
trap 'rm -rf "$work_dir"' EXIT

# A linked worktree created by Windows git records its git directory as a
# Windows path (gitdir: D:/...), which Linux git reads as relative and can't
# resolve. Fall back to Windows git through WSL interop for that case.
archive_head() {
  if git -C "$repo_dir" rev-parse --git-dir >/dev/null 2>&1; then
    git -C "$repo_dir" archive --format=tar HEAD
  elif command -v git.exe >/dev/null 2>&1; then
    git.exe -C "$(wslpath -w "$repo_dir")" archive --format=tar HEAD
  else
    echo "ci-race: $repo_dir is not readable by git, and git.exe is not available" >&2
    return 1
  fi
}

archive_head | tar -C "$work_dir" -xf -
cd "$work_dir"

go test -race -short ./...
go test -race -short -tags devtools ./internal/cli/...
