#Requires -Version 5.1
# Environment variable overrides (params take higher precedence):
#   BOXY_VERSION / INSTALLER_VERSION    — pin a release tag (e.g. v0.1.8)
#   BOXY_INSTALL_DIR / INSTALLER_INSTALL_DIR — override install directory
#   BOXY_FORCE / INSTALLER_FORCE        — set to 1 to force reinstall
#   BOXY_DEBUG / INSTALLER_DEBUG        — set to 1 for verbose output
[CmdletBinding()]
param(
    [string]$Version = $(
        if ($env:BOXY_VERSION) { $env:BOXY_VERSION }
        elseif ($env:INSTALLER_VERSION) { $env:INSTALLER_VERSION }
        elseif ($env:BOXY_INSTALL_VERSION) { $env:BOXY_INSTALL_VERSION }
        else { "latest" }
    ),
    [string]$InstallDir = $(
        if ($env:BOXY_INSTALL_DIR) { $env:BOXY_INSTALL_DIR }
        elseif ($env:INSTALLER_INSTALL_DIR) { $env:INSTALLER_INSTALL_DIR }
        else { Join-Path $env:USERPROFILE ".local\bin" }
    ),
    [switch]$Force = [bool]($env:BOXY_FORCE -eq "1" -or $env:INSTALLER_FORCE -eq "1"),
    [switch]$Debug = [bool]($env:BOXY_DEBUG -eq "1" -or $env:INSTALLER_DEBUG -eq "1")
)

$ErrorActionPreference = "Stop"
$repo = "Geogboe/boxy"
$defaultInstallDir = Join-Path $env:USERPROFILE ".local\bin"
$assetArch = "amd64"

function Write-Step {
    param([string]$Message)
    Write-Host ""
    Write-Host $Message -ForegroundColor Cyan -NoNewline
    Write-Host ""
}

function Write-Info {
    param([string]$Message)
    Write-Host "  " -NoNewline
    Write-Host "→" -ForegroundColor Cyan -NoNewline
    Write-Host " $Message"
}

function Write-Success {
    param([string]$Message)
    Write-Host "  " -NoNewline
    Write-Host "✓" -ForegroundColor Green -NoNewline
    Write-Host " $Message"
}

function Write-Warn {
    param([string]$Message)
    Write-Host "  " -NoNewline
    Write-Host "!" -ForegroundColor Yellow -NoNewline
    Write-Host " $Message"
}

function Write-DebugLog {
    param([string]$Message)
    if ($Debug) { Write-Host "  debug: $Message" -ForegroundColor DarkGray }
    Write-Verbose $Message
}

function Fail {
    param([string]$Message)
    Write-Host "  " -NoNewline
    Write-Host "✗ error:" -ForegroundColor Red -NoNewline
    Write-Host " $Message"
    throw $Message
}

function Get-ProcessorArchitecture {
    if ($env:PROCESSOR_ARCHITEW6432) {
        return $env:PROCESSOR_ARCHITEW6432
    }
    return $env:PROCESSOR_ARCHITECTURE
}

function Resolve-LatestTag {
    param([string]$Repo)

    Write-DebugLog "Resolving latest release tag"
    $releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases?per_page=1"
    if (-not $releases) {
        Fail "Could not resolve latest release tag."
    }

    if ($releases -is [System.Array]) {
        return $releases[0].tag_name
    }
    return $releases.tag_name
}

function Ensure-UserPathContains {
    param([string]$PathEntry)

    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @()
    if ($userPath) {
        $parts = $userPath.Split(';', [System.StringSplitOptions]::RemoveEmptyEntries)
    }

    if ($parts -contains $PathEntry) {
        return $false
    }

    $newPath = if ($userPath) { "$userPath;$PathEntry" } else { $PathEntry }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    if (-not (($env:Path -split ';') -contains $PathEntry)) {
        $env:Path = "$env:Path;$PathEntry"
    }
    return $true
}

Write-Host ""
Write-Host "boxy installer" -ForegroundColor Cyan -NoNewline
Write-Host ""
Write-Host ("─" * 38)

Write-Step "Detecting platform..."

$arch = Get-ProcessorArchitecture
if ($arch -notin @("AMD64", "x86_64", "ARM64")) {
    Fail "Unsupported architecture: $arch. Windows release assets are currently available for amd64 only."
}
if ($arch -eq "ARM64") {
    Write-DebugLog "ARM64 host detected; using the amd64 Windows release asset."
}
Write-Info "platform: windows/$assetArch"

Write-Step "Resolving version..."

if ($Version -eq "latest") {
    Write-Info "fetching latest release tag..."
    $Version = Resolve-LatestTag -Repo $repo
}
Write-Info "version: $Version"

# GoReleaser archive naming: boxy_0.1.9_windows_amd64.zip
# Strip leading 'v' from the tag to get the version number
$versionNum = $Version.TrimStart('v')
$asset = "boxy_${versionNum}_windows_${assetArch}.zip"
$baseUrl = "https://github.com/$repo/releases/download/$Version"
$downloadUrl = "$baseUrl/$asset"
$checksumsUrl = "$baseUrl/checksums.txt"

$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("boxy-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tempDir | Out-Null

try {
    Write-DebugLog "Version: $Version"
    Write-DebugLog "InstallDir: $InstallDir"
    Write-DebugLog "Asset: $asset"

    $downloadedArchive = Join-Path $tempDir $asset
    $checksumsPath = Join-Path $tempDir "checksums.txt"

    Write-Step "Downloading..."
    Write-Info $downloadUrl
    Invoke-WebRequest -Uri $downloadUrl -OutFile $downloadedArchive -UseBasicParsing
    Write-Info "checksums.txt"
    Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksumsPath -UseBasicParsing

    Write-Step "Verifying checksum..."
    $checksumLine = Select-String -Path $checksumsPath -Pattern ([regex]::Escape($asset)) | Select-Object -First 1
    if (-not $checksumLine) {
        Fail "Checksum for $asset not found."
    }

    $expectedChecksum = ($checksumLine.Line -split '\s+')[0].Trim()
    $actualChecksum = (Get-FileHash -Path $downloadedArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($expectedChecksum.ToLowerInvariant() -ne $actualChecksum) {
        Fail "Checksum mismatch for $asset."
    }
    Write-Success "checksum ok"

    Write-Step "Installing..."
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $destination = Join-Path $InstallDir "boxy.exe"
    if ((Test-Path $destination) -and -not $Force) {
        Fail "$destination already exists. Re-run with -Force to overwrite."
    }

    # Extract only boxy.exe from the zip archive
    $extractDir = Join-Path $tempDir "extract"
    Expand-Archive -Path $downloadedArchive -DestinationPath $extractDir -Force
    $extracted = Join-Path $extractDir "boxy.exe"
    Copy-Item -Path $extracted -Destination $destination -Force
    Write-Info "installed to: $destination"

    Write-Step "Verifying install..."
    $versionOutput = $null
    try {
        $versionOutput = & $destination --version 2>$null
    }
    catch {
        $versionOutput = $null
    }

    if ($versionOutput) {
        Write-Success $versionOutput
    }
    else {
        & $destination --help *> $null
        if ($LASTEXITCODE -ne 0) {
            Fail "Installed binary did not execute successfully."
        }
        Write-Success "boxy installed successfully"
    }

    $pathAdded = $false
    if ($InstallDir -eq $defaultInstallDir) {
        if (Ensure-UserPathContains -PathEntry $InstallDir) {
            $pathAdded = $true
        }
    }

    $inPath = ($env:Path -split ';') -contains $InstallDir
    if (-not $inPath -and -not $pathAdded) {
        Write-Host ""
        Write-Warn "$InstallDir is not in your PATH."
        Write-Host "  Run this command, then restart your terminal:" -ForegroundColor Yellow
        Write-Host ""
        $escapedDir = $InstallDir -replace "'", "''"
        Write-Host "  [Environment]::SetEnvironmentVariable('Path', `"`$([Environment]::GetEnvironmentVariable('Path','User'));$escapedDir`", 'User')" -ForegroundColor Cyan
        Write-Host ""
    } elseif ($pathAdded) {
        Write-Warn "Added $InstallDir to your user PATH. Restart your terminal to pick it up."
    }

    Write-Host ""
    Write-Host "Done! " -ForegroundColor Green -NoNewline
    Write-Host "Run 'boxy' to get started."
    Write-Host ""
}
finally {
    if (Test-Path $tempDir) {
        Remove-Item -Recurse -Force -Path $tempDir
    }
}
