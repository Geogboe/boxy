$ErrorActionPreference = "Stop"

# Betterleaks' Git source deliberately isolates the user's system and global
# Git configuration. On Windows, Betterleaks maps Go's os.DevNull to the DOS
# device name NUL for GIT_CONFIG_GLOBAL and GIT_CONFIG_SYSTEM. Native ARM64
# Git for Windows (the clangarm64 build) rejects NUL when it is supplied as a
# config-file path, including Git 2.55.0.windows.3. This is an interoperability
# issue between the two tools, not a repository credential or SSH configuration
# issue. An x64 mingw64 Git installation did not reproduce it in testing.
#
# Keep this workaround local to child Git processes. The real Betterleaks
# process still performs the full history scan; the shim only replaces the
# invalid config-path values before delegating to Git. Empty values disable the
# global/system config scopes, while the repository-local config remains usable.

$git = Get-Command git.exe -CommandType Application | Select-Object -First 1
if ($null -eq $git) {
    throw "git.exe is required to run the Betterleaks history scan"
}

$shimDirectory = Join-Path $PSScriptRoot "betterleaks-git-shim"
$env:BOXY_REAL_GIT = $git.Source
$env:PATH = "$shimDirectory$([IO.Path]::PathSeparator)$env:PATH"

& betterleaks git . --no-banner --redact
exit $LASTEXITCODE
