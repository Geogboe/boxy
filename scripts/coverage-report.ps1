$ErrorActionPreference = 'Stop'

$profilePath = Join-Path (Get-Location) '.tmp/coverage.out'
if (-not (Test-Path -LiteralPath $profilePath)) {
    Write-Error "coverage profile is missing: $profilePath"
    exit 1
}

$summary = & go tool cover "-func=$profilePath" | Select-Object -Last 1
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($summary)) {
	Write-Error 'go tool cover failed or returned an empty report'
	exit 1
}

$summary
