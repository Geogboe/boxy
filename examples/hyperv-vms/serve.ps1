# Start boxy serve with this example's config. Run from an elevated
# (Administrator) PowerShell -- Hyper-V management requires it.
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot  = Split-Path -Parent (Split-Path -Parent $scriptDir)
& go run "$repoRoot\cmd\boxy" serve --config "$scriptDir\boxy.yaml" @args
