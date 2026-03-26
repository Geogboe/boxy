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
    [switch]$InstallerDebug = [bool]($env:BOXY_DEBUG -eq "1" -or $env:INSTALLER_DEBUG -eq "1")
)

$ErrorActionPreference = "Stop"
$repo = "Geogboe/boxy"
$defaultInstallDir = Join-Path $env:LOCALAPPDATA "Programs\boxy\bin"
$apiBaseUrl = if ($env:BOXY_INSTALL_API_BASE_URL) { $env:BOXY_INSTALL_API_BASE_URL } else { "https://api.github.com" }
$releaseBaseUrl = if ($env:BOXY_INSTALL_RELEASE_BASE_URL) { $env:BOXY_INSTALL_RELEASE_BASE_URL } else { $null }

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
    if ($InstallerDebug) { Write-Host "  debug: $Message" -ForegroundColor DarkGray }
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

    # Prefer /releases/latest (excludes prereleases and drafts)
    try {
        $release = Invoke-RestMethod -Uri "$($apiBaseUrl.TrimEnd('/'))/repos/$Repo/releases/latest"
        if ($release.tag_name) {
            Write-DebugLog "Resolved via /releases/latest: $($release.tag_name)"
            return $release.tag_name
        }
    } catch {
        Write-DebugLog "releases/latest failed: $($_.Exception.Message); falling back to most recent release"
    }

    # Fallback: most recent release (may be a prerelease)
    $releases = Invoke-RestMethod -Uri "$($apiBaseUrl.TrimEnd('/'))/repos/$Repo/releases?per_page=1"
    if (-not $releases) {
        Fail "Could not resolve latest release tag."
    }

    if ($releases -is [System.Array]) {
        return $releases[0].tag_name
    }
    return $releases.tag_name
}

function Get-ReleaseAssetSource {
    param(
        [string]$ReleaseRoot,
        [string]$Tag,
        [string]$AssetName
    )

    if (Test-Path -LiteralPath $ReleaseRoot -PathType Container) {
        return Join-Path (Join-Path $ReleaseRoot $Tag) $AssetName
    }

    return "{0}/{1}/{2}" -f $ReleaseRoot.TrimEnd('/'), $Tag, $AssetName
}

function Copy-ReleaseAsset {
    param(
        [string]$ReleaseRoot,
        [string]$Tag,
        [string]$AssetName,
        [string]$Destination
    )

    $source = Get-ReleaseAssetSource -ReleaseRoot $ReleaseRoot -Tag $Tag -AssetName $AssetName
    if (Test-Path -LiteralPath $source -PathType Leaf) {
        Copy-Item -LiteralPath $source -Destination $Destination -Force
        return
    }

    try {
        Invoke-WebRequest -Uri $source -OutFile $Destination -UseBasicParsing
    } catch {
        $status = $_.Exception.Response.StatusCode.value__
        if ($status -eq 404) {
            Fail "Release asset not found: $source (HTTP 404). This version may not have compatible release assets."
        }
        Fail "Failed to download ${AssetName}: $($_.Exception.Message)"
    }
}

function Get-ExpectedChecksum {
    param(
        [string]$ChecksumsPath,
        [string]$AssetName
    )

    foreach ($line in Get-Content -LiteralPath $ChecksumsPath) {
        $parts = $line -split '\s+', 2
        if ($parts.Count -ge 2 -and $parts[1].Trim() -eq $AssetName) {
            return $parts[0].Trim()
        }
    }

    return $null
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

$detectedArch = Get-ProcessorArchitecture
if ($detectedArch -notin @("AMD64", "x86_64", "ARM64")) {
    Fail "Unsupported architecture: $detectedArch. Windows release assets are currently available for amd64 only."
}
$assetArch = "amd64"
if ($detectedArch -eq "ARM64") {
    Write-Info "platform: windows/arm64"
    Write-Warn "No native ARM64 build available; using amd64 binary (runs via Windows x64 emulation)."
} else {
    Write-Info "platform: windows/$assetArch"
}

Write-Step "Resolving version..."

if ($Version -eq "latest") {
    Write-Info "fetching latest release tag..."
    $Version = Resolve-LatestTag -Repo $repo
}
Write-Info "version: $Version"

if (-not $releaseBaseUrl) {
    $releaseBaseUrl = "https://github.com/$repo/releases/download"
}

# GoReleaser archive naming: boxy_0.1.9_windows_amd64.zip
# Strip leading 'v' from the tag to get the version number
$versionNum = $Version.TrimStart('v')
$asset = "boxy_${versionNum}_windows_${assetArch}.zip"

$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) ("boxy-install-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Path $tempDir | Out-Null

try {
    Write-DebugLog "Version: $Version"
    Write-DebugLog "InstallDir: $InstallDir"
    Write-DebugLog "Asset: $asset"

    $downloadedArchive = Join-Path $tempDir $asset
    $checksumsPath = Join-Path $tempDir "checksums.txt"

    Write-Step "Downloading..."
    Write-Info (Get-ReleaseAssetSource -ReleaseRoot $releaseBaseUrl -Tag $Version -AssetName $asset)
    Copy-ReleaseAsset -ReleaseRoot $releaseBaseUrl -Tag $Version -AssetName $asset -Destination $downloadedArchive
    Write-Info "checksums.txt"
    Copy-ReleaseAsset -ReleaseRoot $releaseBaseUrl -Tag $Version -AssetName "checksums.txt" -Destination $checksumsPath

    Write-Step "Verifying checksum..."
    $expectedChecksum = Get-ExpectedChecksum -ChecksumsPath $checksumsPath -AssetName $asset
    if (-not $expectedChecksum) {
        Fail "Checksum for $asset not found."
    }
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
