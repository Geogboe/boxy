$ErrorActionPreference = "Stop"

$git = Get-Command git.exe -CommandType Application | Select-Object -First 1
if ($null -eq $git) {
    throw "git.exe is required to run the Betterleaks history scan"
}

$shimDirectory = Join-Path $PSScriptRoot "betterleaks-git-shim"
$env:BOXY_REAL_GIT = $git.Source
$env:PATH = "$shimDirectory$([IO.Path]::PathSeparator)$env:PATH"

& betterleaks git . --no-banner --redact
exit $LASTEXITCODE
