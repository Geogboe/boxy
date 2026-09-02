[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [string]$Name,

    [Parameter(Mandatory = $true)]
    [string]$Executable,

    [Parameter(Mandatory = $true, Position = 2, ValueFromRemainingArguments = $true)]
    [string[]]$Arguments
)

$reportDir = Join-Path (Get-Location) '.tmp'
New-Item -ItemType Directory -Force -Path $reportDir | Out-Null
$safeName = $Name -replace '[^A-Za-z0-9_.-]', '-'
$reportPath = Join-Path $reportDir "$safeName.log"

& $Executable @Arguments *> $reportPath
$exitCode = $LASTEXITCODE
if ($exitCode -ne 0) {
    $diagnosticPattern = '--- FAIL|FAIL\s|panic|undefined|cannot|fatal'
    $diagnostics = & rg -n -C 5 -- $diagnosticPattern $reportPath
    if ($LASTEXITCODE -eq 0) {
        foreach ($line in $diagnostics) {
            if ($line.Length -gt 400) {
                Write-Output ($line.Substring(0, 400) + ' ...[truncated]')
            } else {
                Write-Output $line
            }
        }
    } else {
        Get-Content -Tail 80 $reportPath
    }
    exit $exitCode
}
