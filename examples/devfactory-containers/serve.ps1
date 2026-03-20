# Start boxy serve with this example's config.
# Can be run from any directory.
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot  = Split-Path -Parent (Split-Path -Parent $scriptDir)
& go run "$repoRoot\cmd\boxy" serve --config "$scriptDir\boxy.yaml" @args
